// What the Go tests used to check on the page's HTML, checked on the code the
// app is built from now: the library is a page of the view like the others,
// mounted wherever the view is, and drawn in walnut, oak, parchment and brass.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "@/api/endpoints";
import { libraryApi } from "@/api/library";
import { isViolet } from "./libraryPalette";

// Every source file the library is made of, tests aside, as text.
const sources: Record<string, string> = {
  ...import.meta.glob(["../components/library/**/*.{ts,tsx}", "./library*.ts", "!./*.test.ts"], { query: "?raw", import: "default", eager: true }),
  ...import.meta.glob(["../api/library.ts", "../pages/LibraryPage.tsx"], { query: "?raw", import: "default", eager: true }),
};

describe("the library's source", () => {
  it("is all there to be read", () => {
    const names = Object.keys(sources);
    expect(names).toContain("../api/library.ts");
    expect(names).toContain("../pages/LibraryPage.tsx");
    expect(names).toContain("./library.ts");
    expect(names.some((n) => n.startsWith("../components/library/"))).toBe(true);
  });

  it("asks for nothing by an absolute path, which would leave an embedder's mount point", () => {
    for (const [name, text] of Object.entries(sources)) {
      for (const absolute of [`"/api`, "'/api", "`/api", `fetch("/`, `href="/`]) {
        expect(text.includes(absolute), `${name} requests ${absolute}…`).toBe(false);
      }
    }
  });

  it("opens no stream of its own: the app keeps one, and the library reads its store", () => {
    for (const [name, text] of Object.entries(sources)) expect(/new\s+EventSource\b/.test(text), name).toBe(false);
  });

  it("loads nothing off the network", () => {
    for (const [name, text] of Object.entries(sources)) {
      expect(/https?:\/\//.test(text), `${name} names a URL`).toBe(false);
      expect(/\bimport\s*\(\s*["'`]https?:/.test(text), name).toBe(false);
    }
  });

  it("writes no violet anywhere, the scene's labels included", () => {
    let seen = 0;
    for (const [name, text] of Object.entries(sources)) {
      for (const m of text.matchAll(/#[0-9a-f]{6}\b/gi)) {
        seen++;
        expect(isViolet(m[0]), `${name} uses ${m[0]}`).toBe(false);
      }
    }
    expect(seen).toBeGreaterThan(60);
  });
});

describe("the library's requests, under a prefix", () => {
  const seen: string[] = [];
  beforeEach(() => {
    seen.length = 0;
    vi.stubGlobal("document", { baseURI: "http://view.invalid/brain/live/" });
    vi.stubGlobal("fetch", async (url: URL | string) => {
      seen.push(String(url));
      return new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } });
    });
  });
  afterEach(() => vi.unstubAllGlobals());

  it("stay under the mount point", async () => {
    await libraryApi.library();
    await libraryApi.card("entity:CortexDB");
    await libraryApi.book("memory:a b");
    await api.find("open claw");
    expect(seen).toEqual([
      "http://view.invalid/brain/live/api/library",
      "http://view.invalid/brain/live/api/library/card?id=entity%3ACortexDB",
      "http://view.invalid/brain/live/api/library/book?id=memory%3Aa+b",
      "http://view.invalid/brain/live/api/find?q=open+claw",
    ]);
  });
});
