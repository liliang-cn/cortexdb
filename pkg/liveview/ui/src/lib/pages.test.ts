import { describe, expect, it } from "vitest";
import { NAV_GROUPS, visibleNav } from "@/components/shell/nav";
import { pageOf, pageShown, parseHiddenPages } from "./pages";

describe("pages a host left out", () => {
  it("reads the meta tag's names, however they are spaced", () => {
    expect([...parseHiddenPages("  recalls\tontology  recalls ")].sort()).toEqual(["ontology", "recalls"]);
    expect(parseHiddenPages("").size).toBe(0);
    expect(parseHiddenPages(null).size).toBe(0);
  });

  it("names the page of an app path, query and all", () => {
    expect(pageOf("/recalls?trace=abc")).toBe("recalls");
    expect(pageOf("/ontology-history?schema=s")).toBe("ontology-history");
    expect(pageOf("/changes#top")).toBe("changes");
    expect(pageOf("/")).toBe("");
    expect(pageOf("ontology")).toBe("ontology");
  });

  it("never mistakes one page for another that shares its prefix", () => {
    const hidden = parseHiddenPages("ontology");
    expect(pageShown("/ontology?schema=s", hidden)).toBe(false);
    expect(pageShown("/ontology-history", hidden)).toBe(true);
    expect(pageShown("/", hidden)).toBe(true);
  });

  it("drops a left-out page from the navigation, and a group left empty with it", () => {
    const groups = visibleNav(NAV_GROUPS, parseHiddenPages("recalls library ontology ontology-history"));
    expect(groups.map((g) => g.map((i) => i.to))).toEqual([["/", "/changes", "/activity"]]);
    expect(visibleNav(NAV_GROUPS, parseHiddenPages(""))).toEqual(NAV_GROUPS);
  });
});
