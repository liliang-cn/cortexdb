import { createHash } from "node:crypto";
import { createWriteStream } from "node:fs";
import { chmod, mkdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { homedir, platform, arch } from "node:os";
import { basename, dirname, join } from "node:path";
import { spawn } from "node:child_process";
import { pipeline } from "node:stream/promises";
import { Readable } from "node:stream";
import { gunzipSync } from "node:zlib";

const SIDECAR_VERSION = "2.57.0";

function target() {
  const os = platform();
  const cpu = arch();
  const goos = os === "darwin" ? "darwin" : os === "linux" ? "linux" : os === "win32" ? "windows" : "";
  const goarch = cpu === "x64" ? "amd64" : cpu === "arm64" ? "arm64" : "";
  if (!goos || !goarch) throw new Error(`unsupported CortexDB sidecar platform: ${os}/${cpu}`);
  return { goos, goarch, exe: goos === "windows" ? "cortexdb-grpc.exe" : "cortexdb-grpc" };
}

function endpointAddress(endpoint) {
  if (!endpoint) return "127.0.0.1:47821";
  return endpoint.replace(/^https?:\/\//, "");
}

async function download(url, output) {
  const response = await fetch(url, { redirect: "follow" });
  if (!response.ok || !response.body) throw new Error(`download ${url}: HTTP ${response.status}`);
  await pipeline(Readable.fromWeb(response.body), createWriteStream(output));
}

async function sha256(path) {
  return createHash("sha256").update(await readFile(path)).digest("hex");
}

function extractTarEntry(tar, wanted) {
  let offset = 0;
  while (offset + 512 <= tar.length) {
    const header = tar.subarray(offset, offset + 512);
    const name = header.subarray(0, 100).toString("utf8").replace(/\0.*$/, "");
    if (!name) break;
    const sizeText = header.subarray(124, 136).toString("ascii").replace(/\0.*$/, "").trim();
    const size = Number.parseInt(sizeText || "0", 8);
    const start = offset + 512;
    if (basename(name) === wanted) return tar.subarray(start, start + size);
    offset = start + Math.ceil(size / 512) * 512;
  }
  throw new Error(`${wanted} not found in CortexDB release archive`);
}

export function createSidecar(config, options = {}) {
  const state = { connected: false, managed: false, error: "", info: null, process: null };
  const sleep = options.sleep || ((ms) => new Promise((resolve) => setTimeout(resolve, ms)));
  const spawnProcess = options.spawn || spawn;
  const root = config.dataDir || join(homedir(), ".cortexdb", "openclaw");

  async function probe(client) {
    try {
      const health = await client.admin.Health({});
      if (!health.ok) throw new Error("CortexDB health check returned not ok");
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

  async function ensureBinary() {
    if (config.binaryPath) return config.binaryPath;
    const { goos, goarch, exe } = target();
    const bin = join(root, "bin", `v${SIDECAR_VERSION}`, exe);
    try {
      await chmod(bin, 0o755);
      return bin;
    } catch {}

    await mkdir(dirname(bin), { recursive: true });
    const asset = `cortexdb-grpc_${goos}_${goarch}.tar.gz`;
    const base = `https://github.com/liliang-cn/cortexdb/releases/download/v${SIDECAR_VERSION}`;
    const archive = `${bin}.tar.gz.download`;
    const checksumFile = `${archive}.sha256`;
    await rm(archive, { force: true });
    await rm(checksumFile, { force: true });
    await download(`${base}/${asset}`, archive);
    await download(`${base}/${asset}.sha256`, checksumFile);
    const expected = (await readFile(checksumFile, "utf8")).trim().split(/\s+/)[0];
    const actual = await sha256(archive);
    if (expected !== actual) throw new Error(`CortexDB sidecar checksum mismatch: expected ${expected}, got ${actual}`);
    const content = extractTarEntry(gunzipSync(await readFile(archive)), exe);
    const tmp = `${bin}.download`;
    await writeFile(tmp, content, { mode: 0o755 });
    await rename(tmp, bin);
    await rm(archive, { force: true });
    await rm(checksumFile, { force: true });
    return bin;
  }

  async function start(client) {
    if (await probe(client)) return;
    if (!config.autoStart) throw new Error(`CortexDB is unreachable at ${config.endpoint}: ${state.error}`);
    if (endpointAddress(config.endpoint) !== "127.0.0.1:47821" && endpointAddress(config.endpoint) !== "localhost:47821") {
      throw new Error(`autoStart only supports the local default endpoint; configure an external sidecar for ${config.endpoint}`);
    }
    const binary = await ensureBinary();
    const dbPath = config.dbPath || join(homedir(), ".cortexdb", "cortexdb.db");
    await mkdir(dirname(dbPath), { recursive: true });
    state.process = spawnProcess(binary, ["-db", dbPath, "-addr", endpointAddress(config.endpoint)], {
      detached: false,
      stdio: "ignore",
      windowsHide: true,
      env: { ...process.env, ...(config.token ? { CORTEXDB_GRPC_TOKEN: config.token } : {}) },
    });
    state.managed = true;
    state.process.once?.("exit", (code) => {
      state.connected = false;
      state.managed = false;
      if (code && code !== 0) state.error = `managed CortexDB sidecar exited with code ${code}`;
    });
    for (let attempt = 0; attempt < 40; attempt += 1) {
      if (await probe(client)) return;
      await sleep(250);
    }
    throw new Error(`CortexDB sidecar did not become healthy at ${config.endpoint}: ${state.error}`);
  }

  async function stop() {
    if (!state.managed || !state.process) return;
    state.process.kill();
    state.process = null;
    state.managed = false;
    state.connected = false;
  }

  return { state, probe, start, stop, ensureBinary };
}
