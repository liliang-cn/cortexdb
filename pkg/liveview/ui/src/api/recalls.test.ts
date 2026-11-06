import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiUrl } from "./client";
import { GOLDEN_PATH, recallsApi } from "./recalls";

// The view may be mounted under a prefix of another mux, so every request is
// resolved against the page's <base>. Here the page sits under /live/.
const BASE = "http://127.0.0.1:46432/live/";

describe("the recalls API", () => {
  const calls: Array<{ url: string; init: RequestInit }> = [];

  beforeEach(() => {
    calls.length = 0;
    vi.stubGlobal("document", { baseURI: BASE });
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: URL | string, init: RequestInit = {}) => {
        calls.push({ url: String(url), init });
        return new Response(JSON.stringify({ recall_id: "r1", record_id: "m1", verdict: "useful" }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }),
    );
  });
  afterEach(() => vi.unstubAllGlobals());

  it("posts a mark from the page itself, as a JSON body under the page's own prefix", async () => {
    await recallsApi.feedback({ recall_id: "r1", record_id: "m1", verdict: "useful" });
    expect(calls).toHaveLength(1);
    const { url, init } = calls[0]!;
    expect(url).toBe(`${BASE}api/recalls/feedback`);
    expect(init.method).toBe("POST");
    expect(new Headers(init.headers).get("Content-Type")).toBe("application/json");
    expect(JSON.parse(String(init.body))).toEqual({ recall_id: "r1", record_id: "m1", verdict: "useful" });
  });

  it("asks for a page of the timeline with its filters, the trace among them", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: URL | string) => {
        calls.push({ url: String(url), init: {} });
        return new Response(JSON.stringify({ recalls: [] }), { status: 200 });
      }),
    );
    await recallsApi.list({ trace: "4bf92f3577b34da6a3ce929d0e0e4736", empty: true, before: 17, limit: 50 });
    const u = new URL(calls[0]!.url);
    expect(u.pathname).toBe("/live/api/recalls");
    expect(Object.fromEntries(u.searchParams)).toEqual({ trace: "4bf92f3577b34da6a3ce929d0e0e4736", empty: "1", before: "17", limit: "50" });
  });

  it("downloads the golden set from a relative path", () => {
    expect(GOLDEN_PATH.startsWith("/")).toBe(false);
    expect(apiUrl(GOLDEN_PATH).href).toBe(`${BASE}api/recalls/golden`);
  });

  it("turns a refused export into the server's own sentence", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(JSON.stringify({ error: "the recall log is off on this brain" }), { status: 502 })),
    );
    await expect(recallsApi.golden()).rejects.toMatchObject({ message: "the recall log is off on this brain", status: 502 });
  });
});
