import assert from "node:assert/strict";
import test from "node:test";
import { createSidecar } from "../lib/sidecar.js";

test("uses an already healthy external sidecar", async () => {
  const client = {
    admin: {
      async Health() { return { ok: true }; },
      async Info() { return { version: "2.57.0", dbPath: "/tmp/cortexdb.db" }; },
    },
  };
  const sidecar = createSidecar({ endpoint: "127.0.0.1:47821", autoStart: true });
  await sidecar.start(client);
  assert.equal(sidecar.state.connected, true);
  assert.equal(sidecar.state.managed, false);
  assert.equal(sidecar.state.info.version, "2.57.0");
});

test("fails clearly when autoStart is disabled", async () => {
  const client = { admin: { async Health() { throw new Error("connection refused"); } } };
  const sidecar = createSidecar({ endpoint: "127.0.0.1:47821", autoStart: false });
  await assert.rejects(sidecar.start(client), /CortexDB is unreachable.*connection refused/);
});

test("starts a configured local binary and waits for health", async () => {
  let attempts = 0;
  const client = {
    admin: {
      async Health() {
        attempts += 1;
        if (attempts < 3) throw new Error("connection refused");
        return { ok: true };
      },
      async Info() { return { version: "2.57.0" }; },
    },
  };
  const spawned = [];
  const process = { once() {}, kill() {} };
  const sidecar = createSidecar({
    endpoint: "127.0.0.1:47821",
    autoStart: true,
    binaryPath: "/tmp/cortexdb-grpc",
    dbPath: "/tmp/cortexdb.db",
  }, {
    sleep: async () => {},
    spawn(binary, args) { spawned.push({ binary, args }); return process; },
  });
  await sidecar.start(client);
  assert.equal(spawned[0].binary, "/tmp/cortexdb-grpc");
  assert.deepEqual(spawned[0].args, ["-db", "/tmp/cortexdb.db", "-addr", "127.0.0.1:47821"]);
  assert.equal(sidecar.state.managed, true);
  assert.equal(sidecar.state.connected, true);
});
