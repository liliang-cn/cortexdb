import { describe, expect, it } from "vitest";
import { parseHiddenPages, routeShown } from "./pages";
import { graphViews, legacyLibrarySearch, readView, switchView } from "./graphViews";

const q = (s: string) => new URLSearchParams(s);

describe("the graph page's views", () => {
  it("draws both, or the graph alone when a host leaves the library out", () => {
    expect(graphViews(parseHiddenPages(""))).toEqual(["graph", "library"]);
    expect(graphViews(parseHiddenPages("library"))).toEqual(["graph"]);
    expect(graphViews(parseHiddenPages("recalls ontology"))).toEqual(["graph", "library"]);
  });

  it("opens the graph unless the URL names the library", () => {
    const both = graphViews(parseHiddenPages(""));
    expect(readView(q(""), both)).toBe("graph");
    expect(readView(q("view=library"), both)).toBe("library");
    expect(readView(q("view=graph"), both)).toBe("graph");
    expect(readView(q("view=nonsense"), both)).toBe("graph");
  });

  it("opens the graph when the URL names a library the host left out", () => {
    expect(readView(q("view=library&book=b1"), ["graph"])).toBe("graph");
  });

  it("writes the library into the URL and the graph as the bare URL, keeping both views' parameters", () => {
    const library = switchView(q("focus=n1&book=b1&lang=zh"), "library");
    expect(library.toString()).toBe("focus=n1&book=b1&lang=zh&view=library");
    expect(switchView(library, "graph").toString()).toBe("focus=n1&book=b1&lang=zh");
    expect(switchView(library, "library").toString()).toBe(library.toString());
  });
});

describe("the old library page's address", () => {
  it("lands on the library view with its book or card", () => {
    expect(legacyLibrarySearch("?book=m%201")).toBe("?book=m+1&view=library");
    expect(legacyLibrarySearch("?card=e1&lang=zh&mode=light")).toBe("?card=e1&lang=zh&mode=light&view=library");
    expect(legacyLibrarySearch("")).toBe("?view=library");
  });

  it("is a route only while the library is part of the view", () => {
    expect(routeShown("/library", parseHiddenPages(""))).toBe(true);
    expect(routeShown("/library", parseHiddenPages("library"))).toBe(false);
    expect(routeShown("/", parseHiddenPages("library"))).toBe(true);
  });
});
