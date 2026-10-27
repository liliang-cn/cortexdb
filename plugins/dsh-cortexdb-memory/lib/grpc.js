// A gRPC client for the three calls this plugin makes, on node:http2 alone.
//
// dsh installs plugins with pnpm, which refuses a dependency whose build
// script nobody approved — and every gRPC library for Node pulls in protobufjs,
// which has one. Three unary calls with string and bool fields do not need a
// protobuf runtime: a unary gRPC call is one HTTP/2 POST with a five-byte frame
// header, and its status comes back in the trailers.

import { randomBytes } from "node:crypto";
import http2 from "node:http2";

// --- protobuf, the subset these messages use: strings, bools, int64s ------

function varint(n) {
  const out = [];
  let v = BigInt(n);
  while (v > 127n) {
    out.push(Number((v & 127n) | 128n));
    v >>= 7n;
  }
  out.push(Number(v));
  return Buffer.from(out);
}

// encodeStrings encodes a message whose fields are all strings, keyed by field
// number. Empty strings are left out, as proto3 does.
export function encodeStrings(fields) {
  const parts = [];
  for (const [number, value] of Object.entries(fields)) {
    if (value === undefined || value === null || value === "") continue;
    const bytes = Buffer.from(String(value), "utf8");
    parts.push(varint((Number(number) << 3) | 2), varint(bytes.length), bytes);
  }
  return Buffer.concat(parts);
}

function readVarint(buf, pos) {
  let result = 0n;
  let shift = 0n;
  for (;;) {
    if (pos >= buf.length) throw new Error("truncated protobuf varint");
    const byte = buf[pos++];
    result |= BigInt(byte & 127) << shift;
    if (byte < 128) return [result, pos];
    shift += 7n;
  }
}

// decodeFields reads a message into { fieldNumber: value }: varints as
// BigInt, length-delimited fields as Buffers. Fixed-width fields are skipped.
export function decodeFields(buf) {
  const out = {};
  let pos = 0;
  while (pos < buf.length) {
    let key;
    [key, pos] = readVarint(buf, pos);
    const number = Number(key >> 3n);
    const wire = Number(key & 7n);
    if (wire === 0) {
      [out[number], pos] = readVarint(buf, pos);
    } else if (wire === 2) {
      let len;
      [len, pos] = readVarint(buf, pos);
      if (len > BigInt(buf.length - pos)) throw new Error("truncated protobuf length-delimited field");
      out[number] = buf.subarray(pos, pos + Number(len));
      pos += Number(len);
    } else if (wire === 1) {
      if (pos + 8 > buf.length) throw new Error("truncated protobuf fixed64 field");
      pos += 8;
    } else if (wire === 5) {
      if (pos + 4 > buf.length) throw new Error("truncated protobuf fixed32 field");
      pos += 4;
    } else {
      throw new Error(`unsupported protobuf wire type ${wire}`);
    }
  }
  return out;
}

const str = (v) => (v ? Buffer.from(v).toString("utf8") : "");
const bool = (v) => v === 1n;

// --- gRPC over HTTP/2 -------------------------------------------------------

function frame(message) {
  const header = Buffer.alloc(5);
  header.writeUInt32BE(message.length, 1);
  return Buffer.concat([header, message]);
}

function unframe(body) {
  if (body.length === 0) return Buffer.alloc(0);
  if (body.length < 5) throw new Error("truncated gRPC frame");
  if (body[0] !== 0) throw new Error("compressed gRPC responses are not supported");
  const size = body.readUInt32BE(1);
  if (body.length < 5 + size) throw new Error("truncated gRPC frame payload");
  return body.subarray(5, 5 + size);
}

export class GrpcError extends Error {
  constructor(code, message, requestId = "") {
    super(`gRPC ${code}: ${message}${requestId ? ` (request_id ${requestId})` : ""}`);
    this.code = code;
    this.requestId = requestId;
  }
}

// parseToolResult reads a tool's JSON answer. A reply that is not JSON is
// reported by what it was — its size, its first bytes and the call's request
// id — not by the parser's message: "Unexpected identifier" says nothing about
// a host that swapped an oversized result for a line of text, and the request
// id finds the server's record of what it actually sent.
export function parseToolResult(tool, resultJson, requestId = "") {
  const text = resultJson || "{}";
  try {
    return JSON.parse(text);
  } catch {
    const head = text.slice(0, 200).replace(/\s+/g, " ");
    const id = requestId ? `, request_id ${requestId}` : "";
    const error = new Error(`${tool}: reply is not JSON (${Buffer.byteLength(text)} bytes${id}): ${head}`);
    error.requestId = requestId;
    error.received = text.slice(0, 2000);
    throw error;
  }
}

function decodeGrpcMessage(value) {
  try {
    return decodeURIComponent(String(value ?? ""));
  } catch {
    return String(value ?? "");
  }
}

// createClient talks to `endpoint` (host:port, or an http:// / https:// URL;
// a bare host:port is plaintext, as the CortexDB sidecar serves by default).
export function createClient(endpoint, { token = "" } = {}) {
  const url = /^https?:\/\//.test(endpoint) ? endpoint : `http://${endpoint}`;
  let session = null;
  let active = 0;

  function connection() {
    if (!session || session.closed || session.destroyed) {
      session = http2.connect(url);
      session.on("error", () => {});
      session.unref();
    }
    return session;
  }

  // Every call carries a request id the server logs and returns, so a reply
  // that went wrong can be matched to the server's record of it.
  function unary(path, message, timeoutMs) {
    const requestId = randomBytes(16).toString("hex");
    return new Promise((resolve, reject) => {
      const conn = connection();
      active += 1;
      conn.ref();
      const headers = {
        ":method": "POST",
        ":path": path,
        "content-type": "application/grpc",
        te: "trailers",
        ...(timeoutMs ? { "grpc-timeout": `${Math.max(1, Math.ceil(timeoutMs))}m` } : {}),
        ...(token ? { authorization: `Bearer ${token}` } : {}),
        "x-request-id": requestId,
      };
      const stream = conn.request(headers);
      const chunks = [];
      let status = null;
      let detail = "";
      let settled = false;
      const timer = timeoutMs ? setTimeout(() => {
        finish(new GrpcError("DEADLINE_EXCEEDED", `${path} timed out after ${timeoutMs} ms`, requestId));
        stream.close(http2.constants.NGHTTP2_CANCEL);
      }, timeoutMs) : null;
      timer?.unref?.();

      function finish(error, value) {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        active -= 1;
        if (active === 0 && !conn.destroyed) conn.unref();
        if (error) {
          error.requestId ||= requestId;
          reject(error);
        } else resolve({ fields: value, requestId });
      }
      function readStatus(h) {
        if (h["grpc-status"] !== undefined) {
          status = Number(h["grpc-status"]);
          detail = decodeGrpcMessage(h["grpc-message"]);
        }
      }

      stream.on("response", (h) => {
        if (h[":status"] !== 200) {
          finish(new GrpcError("UNAVAILABLE", `${path}: HTTP ${h[":status"]}`, requestId));
          return;
        }
        readStatus(h);
      });
      stream.on("trailers", readStatus);
      stream.on("data", (chunk) => chunks.push(chunk));
      stream.on("error", (error) => finish(new GrpcError("UNAVAILABLE", error.message, requestId)));
      stream.on("end", () => {
        if (status === null) return finish(new GrpcError("UNKNOWN", `${path}: no grpc-status`, requestId));
        if (status !== 0) return finish(new GrpcError(status, detail || `${path} failed`, requestId));
        try {
          finish(null, decodeFields(unframe(Buffer.concat(chunks))));
        } catch (error) {
          finish(error);
        }
      });
      stream.end(frame(message));
    });
  }

  return {
    admin: {
      async Health(_req, timeoutMs = 5000) {
        const { fields: f } = await unary("/cortexdb.v1.AdminService/Health", Buffer.alloc(0), timeoutMs);
        return { ok: bool(f[1]) };
      },
      async Info(_req, timeoutMs = 5000) {
        const { fields: f } = await unary("/cortexdb.v1.AdminService/Info", Buffer.alloc(0), timeoutMs);
        return { version: str(f[1]), dbPath: str(f[2]), hasEmbedder: bool(f[3]) };
      },
    },
    tools: {
      async CallTool({ name, argsJson }, timeoutMs) {
        const { fields: f, requestId } = await unary("/cortexdb.v1.ToolsService/CallTool", encodeStrings({ 1: name, 2: argsJson }), timeoutMs);
        return { resultJson: str(f[1]), requestId };
      },
    },
    close() {
      session?.close();
      session = null;
    },
  };
}
