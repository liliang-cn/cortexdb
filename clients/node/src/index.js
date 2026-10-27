'use strict';

const crypto = require('crypto');
const path = require('path');
const grpc = require('@grpc/grpc-js');
const protoLoader = require('@grpc/proto-loader');

const PROTO_DIR = path.join(__dirname, '..', 'proto');
const PROTO_FILES = [
  'admin', 'knowledge', 'memory', 'graph', 'graphrag', 'tools',
].map((n) => path.join(PROTO_DIR, 'cortexdb', 'v1', `${n}.proto`));

function loadPackage() {
  const def = protoLoader.loadSync(PROTO_FILES, {
    keepCase: false,
    longs: Number,
    enums: String,
    defaults: true,
    oneofs: true,
    includeDirs: [PROTO_DIR],
  });
  return grpc.loadPackageDefinition(def).cortexdb.v1;
}

const REQUEST_ID_HEADER = 'x-request-id';

// Promisify every unary method on a generated grpc client so callers can await
// instead of dealing with callbacks. Returns a plain object of method -> fn.
function promisify(stub, metadata) {
  const out = {};
  const proto = Object.getPrototypeOf(stub);
  for (const name of Object.getOwnPropertyNames(proto)) {
    if (name === 'constructor') continue;
    const method = stub[name];
    if (typeof method !== 'function' || !method.requestSerialize) continue;
    // Expose both the original PascalCase RPC name and a camelCase alias.
    const camel = name.charAt(0).toLowerCase() + name.slice(1);
    // Every call carries a request id the server logs and returns. The id
    // is on a failed call's error (err.requestId, and in its message) and,
    // not enumerable, on the response, so a reply that went wrong can be
    // matched to the server's record of it.
    const fn = (request = {}) =>
      new Promise((resolve, reject) => {
        const requestId = crypto.randomBytes(16).toString('hex');
        const md = metadata.clone();
        md.set(REQUEST_ID_HEADER, requestId);
        stub[name](request, md, (err, res) => {
          if (err) {
            err.requestId = requestId;
            err.message = `${err.message} (request_id ${requestId})`;
            reject(err);
            return;
          }
          if (res && typeof res === 'object') Object.defineProperty(res, 'requestId', { value: requestId });
          resolve(res);
        });
      });
    out[name] = fn;
    out[camel] = fn;
  }
  return out;
}

/**
 * One connection to a CortexDB sidecar, exposing all service clients.
 * Sub-clients mirror the Rust crate and the Python package.
 */
class CortexClient {
  /**
   * @param {string} endpoint host:port, optionally http(s):// prefixed
   * @param {object} [opts]
   * @param {string} [opts.token] bearer token applied to every request
   * @param {grpc.ChannelCredentials} [opts.credentials] TLS credentials (default: insecure)
   */
  static connect(endpoint, opts = {}) {
    return new CortexClient(endpoint, opts);
  }

  constructor(endpoint, opts = {}) {
    let target = endpoint;
    for (const prefix of ['http://', 'https://']) {
      if (target.startsWith(prefix)) { target = target.slice(prefix.length); break; }
    }
    const creds = opts.credentials || grpc.credentials.createInsecure();

    this._metadata = new grpc.Metadata();
    if (opts.token) this._metadata.set('authorization', `Bearer ${opts.token}`);

    const pkg = loadPackage();
    this._stubs = {
      admin: new pkg.AdminService(target, creds),
      knowledge: new pkg.KnowledgeService(target, creds),
      memory: new pkg.MemoryService(target, creds),
      graph: new pkg.KnowledgeGraphService(target, creds),
      graphrag: new pkg.GraphRagService(target, creds),
      tools: new pkg.ToolsService(target, creds),
    };

    this.admin = promisify(this._stubs.admin, this._metadata);
    this.knowledge = promisify(this._stubs.knowledge, this._metadata);
    this.memory = promisify(this._stubs.memory, this._metadata);
    this.graph = promisify(this._stubs.graph, this._metadata);
    this.graphrag = promisify(this._stubs.graphrag, this._metadata);
    this.tools = promisify(this._stubs.tools, this._metadata);
  }

  close() {
    for (const s of Object.values(this._stubs)) {
      if (typeof s.close === 'function') s.close();
    }
  }
}

module.exports = { CortexClient, grpc };
