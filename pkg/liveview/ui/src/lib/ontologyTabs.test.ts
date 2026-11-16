import { describe, expect, it } from "vitest";
import { parseHiddenPages } from "./pages";
import { legacyVersionsSearch, ontologyTabs, readTab, scopeParams, switchTab, unscopeParams } from "./ontologyTabs";

const q = (s: string) => new URLSearchParams(s);

describe("the ontology page's tabs", () => {
  it("draws both, or the one a host left in", () => {
    expect(ontologyTabs(parseHiddenPages(""))).toEqual(["ontology", "versions"]);
    expect(ontologyTabs(parseHiddenPages("ontology-history"))).toEqual(["ontology"]);
    expect(ontologyTabs(parseHiddenPages("ontology"))).toEqual(["versions"]);
  });

  it("opens the ontology tab unless the URL names the versions tab", () => {
    const both = ontologyTabs(parseHiddenPages(""));
    expect(readTab(q(""), both)).toBe("ontology");
    expect(readTab(q("tab=versions"), both)).toBe("versions");
    expect(readTab(q("tab=nonsense"), both)).toBe("ontology");
  });

  it("opens the tab that is drawn when the URL names one that is not", () => {
    expect(readTab(q("tab=versions"), ["ontology"])).toBe("ontology");
    expect(readTab(q(""), ["versions"])).toBe("versions");
  });

  it("writes the versions tab into the URL and the ontology tab as the bare URL, keeping both tabs' parameters", () => {
    const versions = switchTab(q("schema=a&v_schema=b&v_from=1&lang=zh"), "versions");
    expect(versions.toString()).toBe("schema=a&v_schema=b&v_from=1&lang=zh&tab=versions");
    expect(switchTab(versions, "ontology").toString()).toBe("schema=a&v_schema=b&v_from=1&lang=zh");
  });

  it("opens the versions tab on the schema on screen, dropping the picks of another", () => {
    expect(switchTab(q("v_schema=b&v_from=1&v_to=2"), "versions", "a").toString()).toBe("v_schema=a&tab=versions");
    expect(switchTab(q("v_schema=a&v_from=1&v_to=2"), "versions", "a").toString()).toBe("v_schema=a&v_from=1&v_to=2&tab=versions");
  });
});

describe("the versions tab's own parameters", () => {
  it("are read without their prefix, and nothing else is", () => {
    expect(scopeParams(q("schema=a&v_schema=b&v_from=3&tab=versions"), "v_").toString()).toBe("schema=b&from=3");
  });

  it("are written back under it, leaving every other parameter alone", () => {
    const base = q("schema=a&v_schema=b&v_from=3&tab=versions");
    expect(unscopeParams(q("schema=c&to=4"), "v_", base).toString()).toBe("schema=a&tab=versions&v_schema=c&v_to=4");
    expect(unscopeParams(q(""), "v_", base).toString()).toBe("schema=a&tab=versions");
  });
});

describe("the old versions page's address", () => {
  it("lands on the versions tab with its schema and picks", () => {
    expect(legacyVersionsSearch("?schema=transit&from=2&to=5")).toBe("?tab=versions&v_schema=transit&v_from=2&v_to=5");
    expect(legacyVersionsSearch("")).toBe("?tab=versions");
  });

  it("keeps the parameters the whole app reads as they came", () => {
    expect(legacyVersionsSearch("?schema=a%20b&lang=zh&mode=light")).toBe("?tab=versions&v_schema=a+b&lang=zh&mode=light");
  });
});
