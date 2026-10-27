// The CortexDB end of the plugin: a gRPC client to the sidecar, the sidecar
// itself when nobody runs one, and the release binaries both need.
//
// Binaries are pinned to this package's own version. The release that
// publishes this package publishes the binaries beside it, so the two cannot
// drift apart the way a hard-coded version does.

import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { createWriteStream, readFileSync } from "node:fs";
import { chmod, mkdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { arch, homedir, platform } from "node:os";
import { basename, dirname, join } from "node:path";
import { Readable } from "node:stream";
import { pipeline } from "node:stream/promises";
import { gunzipSync } from "node:zlib";
import { createClient, parseToolResult } from "./grpc.js";

export const VERSION = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8")).version;

const DEFAULT_ENDPOINT = "127.0.0.1:47821";
const RELEASES = "https://github.com/liliang-cn/cortexdb/releases/download";

function target() {
  const goos = { darwin: "darwin", linux: "linux", win32: "windows" }[platform()];
  const goarch = { x64: "amd64", arm64: "arm64" }[arch()];
  if (!goos || !goarch) throw new Error(`no CortexDB release for ${platform()}/${arch()}`);
  return { goos, goarch, ext: goos === "windows" ? ".exe" : "" };
}

export function endpointAddress(endpoint) {
  return (endpoint || DEFAULT_ENDPOINT).replace(/^https?:\/\//, "");
}

function isLocalDefault(endpoint) {
  const addr = endpointAddress(endpoint);
  return addr === DEFAULT_ENDPOINT || addr === "localhost:47821";
}

async function download(url, output, fetchImpl) {
  const response = await fetchImpl(url, { redirect: "follow" });
  if (!response.ok || !response.body) throw new Error(`download ${url}: HTTP ${response.status}`);
  await pipeline(Readable.fromWeb(response.body), createWriteStream(output));
}

async function sha256(path) {
  return createHash("sha256").update(await readFile(path)).digest("hex");
}

export function extractTarEntry(tar, wanted) {
  let offset = 0;
  while (offset + 512 <= tar.length) {
    const header = tar.subarray(offset, offset + 512);
    const name = header.subarray(0, 100).toString("utf8").replace(/\0.*$/s, "");
    if (!name) break;
    const size = Number.parseInt(header.subarray(124, 136).toString("ascii").replace(/\0.*$/s, "").trim() || "0", 8);
    const start = offset + 512;
    if (basename(name) === wanted) return tar.subarray(start, start + size);
    offset = start + Math.ceil(size / 512) * 512;
  }
  throw new Error(`${wanted} not found in the CortexDB release archive`);
}

// releaseBinary returns the path of one release binary, downloading and
// verifying it on first use. `asset` is the release file; `unpack` turns its
// bytes into the executable's.
async function releaseBinary(root, exe, asset, unpack, fetchImpl) {
  const bin = join(root, "bin", `v${VERSION}`, exe);
  try {
    await chmod(bin, 0o755);
    return bin;
  } catch {}
  await mkdir(dirname(bin), { recursive: true });
  const archive = `${bin}.asset.download`;
  const checksum = `${archive}.sha256`;
  try {
    await download(`${RELEASES}/v${VERSION}/${asset}`, archive, fetchImpl);
    await download(`${RELEASES}/v${VERSION}/${asset}.sha256`, checksum, fetchImpl);
    const expected = (await readFile(checksum, "utf8")).trim().split(/\s+/)[0];
    const actual = await sha256(archive);
    if (expected !== actual) throw new Error(`${asset}: checksum mismatch, expected ${expected}, got ${actual}`);
    const tmp = `${bin}.download`;
    await writeFile(tmp, unpack(await readFile(archive)), { mode: 0o755 });
    await rename(tmp, bin);
    return bin;
  } finally {
    await rm(archive, { force: true });
    await rm(checksum, { force: true });
  }
}

export function grpcBinary(config, fetchImpl = fetch) {
  if (config.binaryPath) return Promise.resolve(config.binaryPath);
  const { goos, goarch, ext } = target();
  const exe = `cortexdb-grpc${ext}`;
  return releaseBinary(config.dataDir, exe, `cortexdb-grpc_${goos}_${goarch}.tar.gz`,
    (bytes) => extractTarEntry(gunzipSync(bytes), exe), fetchImpl);
}

export function mcpBinary(config, fetchImpl = fetch) {
  if (config.mcpBinaryPath) return Promise.resolve(config.mcpBinaryPath);
  const { goos, goarch, ext } = target();
  return releaseBinary(config.dataDir, `cortexdb-mcp${ext}`, `cortexdb-mcp-${goos}-${goarch}${ext}`, (bytes) => bytes, fetchImpl);
}

// createSidecar finds a sidecar at the configured endpoint, or starts one
// against the local database when the endpoint is the local default.
export function createSidecar(config, options = {}) {
  const state = { connected: false, managed: false, error: "", info: null, process: null };
  const sleep = options.sleep || ((ms) => new Promise((resolve) => setTimeout(resolve, ms)));
  const spawnProcess = options.spawn || spawn;
  const binary = options.binary || (() => grpcBinary(config));

  async function probe(client) {
    try {
      const health = await client.admin.Health({});
      if (!health.ok) throw new Error("health check returned not ok");
      state.info = await client.admin.Info({});
      state.connected = true;
      state.error = "";
      return true;
    } catch (error) {
      state.connected = false;
      state.error = error instanceof Error ? error.message : String(error);
      return false;
    }
  }

  async function start(client) {
    if (await probe(client)) return;
    if (!config.autoStart) throw new Error(`CortexDB is unreachable at ${config.endpoint}: ${state.error}`);
    if (!isLocalDefault(config.endpoint)) {
      throw new Error(`CortexDB is unreachable at ${config.endpoint} (${state.error}); autoStart only starts a sidecar on the local default ${DEFAULT_ENDPOINT}`);
    }
    const bin = await binary();
    await mkdir(dirname(config.dbPath), { recursive: true });
    state.process = spawnProcess(bin, ["-db", config.dbPath, "-addr", endpointAddress(config.endpoint)], {
      stdio: "ignore",
      windowsHide: true,
      env: { ...process.env, ...(config.token ? { CORTEXDB_GRPC_TOKEN: config.token } : {}) },
    });
    state.managed = true;
    state.process.once?.("exit", (code) => {
      state.connected = false;
      state.managed = false;
      if (code) state.error = `the CortexDB sidecar this plugin started exited with code ${code}`;
    });
    for (let attempt = 0; attempt < 40; attempt += 1) {
      if (await probe(client)) return;
      await sleep(250);
    }
    throw new Error(`the CortexDB sidecar did not become healthy at ${config.endpoint}: ${state.error}`);
  }

  async function stop() {
    if (!state.managed || !state.process) return;
    state.process.kill();
    state.process = null;
    state.managed = false;
    state.connected = false;
  }

  return { state, probe, start, stop };
}

function withTimeout(promise, ms, what) {
  let timer;
  const timeout = new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error(`${what} timed out after ${ms} ms`)), ms);
    timer.unref?.();
  });
  return Promise.race([promise, timeout]).finally(() => clearTimeout(timer));
}

// createBackend is what the plugin talks to: ready() once, then call() any
// CortexDB tool by name.
export function createBackend(config, options = {}) {
  const connect = options.connect || createClient;
  const client = connect(config.endpoint || DEFAULT_ENDPOINT, config.token ? { token: config.token } : {});
  const sidecar = (options.sidecar || createSidecar)(config);
  let readying = null;

  function ready() {
    readying ||= sidecar.start(client).catch((error) => {
      readying = null;
      throw new Error(`CortexDB unavailable: ${error instanceof Error ? error.message : String(error)}`);
    });
    return readying;
  }

  async function call(name, args, timeoutMs = config.callTimeoutMs) {
    await ready();
    const response = await withTimeout(client.tools.CallTool({ name, argsJson: JSON.stringify(args) }, timeoutMs), timeoutMs, name);
    return parseToolResult(name, response.resultJson, response.requestId);
  }

  async function close() {
    await sidecar.stop();
    client.close();
  }

  return { config, client, sidecar, ready, call, close };
}

export function defaultDataDir() {
  return join(homedir(), ".cortexdb", "dsh");
}
