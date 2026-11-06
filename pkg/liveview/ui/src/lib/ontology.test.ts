import { describe, expect, it } from "vitest";
import { DEFAULT_OPTIONS, ONTOLOGY_MS, failedAnswer, latestSchemaChange, ontologyQuery, pageParts, queryKey, readOntologyOptions, versionsPath, writeOntologyOptions } from "./ontology";
import { absent, draft, transit } from "./ontologyFixtures";

const read = (search: string) => readOntologyOptions(new URLSearchParams(search));

describe("the ontology page's cadence", () => {
  it("is liveview.OntologyInterval, half a minute", () => {
    // The Go test reads the source line; this keeps the value honest from
    // the other side.
    expect(ONTOLOGY_MS).toBe(30_000);
  });
});

// Every switch is also a URL option, because an embedder cannot press a
// button.
describe("options from the URL", () => {
  it("default to everything on, the saved schema, and no draft", () => {
    expect(read("")).toEqual(DEFAULT_OPTIONS);
  });

  it("read every switch the page offers", () => {
    expect(read("?gap=0").gap).toBe(false);
    expect(read("?strays=0").strays).toBe(false);
    expect(read("?panels=0").panels).toBe(false);
    expect(read("?schema=transit-lite").schema).toBe("transit-lite");
    expect(read("?draft=1").draft).toBe(true);
    expect(read("?draft=1&min=5&min_nodes=2&min_edges=7")).toMatchObject({ min: "5", minNodes: "2", minEdges: "7" });
    expect(read("?bg=FAFAF9").bg).toBe("#fafaf9");
  });

  it("only turn a switch off on its exact word", () => {
    expect(read("?gap=1").gap).toBe(true);
    expect(read("?gap=false").gap).toBe(true);
    expect(read("?draft=true").draft).toBe(false);
    expect(read("?bg=red").bg).toBeNull();
  });

  it("keep a threshold of zero apart from no threshold", () => {
    // Zero is how a reader asks for the whole vocabulary.
    expect(read("?draft=1&min=0").min).toBe("0");
    expect(read("?draft=1").min).toBe("");
  });

  it("write back only what differs from the default, keeping the rest of the URL", () => {
    const base = new URLSearchParams("mode=dark&gap=0");
    expect(writeOntologyOptions(DEFAULT_OPTIONS, base).toString()).toBe("mode=dark");
    const all = writeOntologyOptions({ ...DEFAULT_OPTIONS, gap: false, strays: false, panels: false, schema: "s", draft: true, min: "0", minNodes: "1", minEdges: "2", bg: "#000000" });
    expect(Object.fromEntries(all)).toEqual({ gap: "0", strays: "0", panels: "0", schema: "s", draft: "1", min: "0", min_nodes: "1", min_edges: "2", bg: "000000" });
    expect(readOntologyOptions(all)).toMatchObject({ gap: false, strays: false, panels: false, schema: "s", draft: true, min: "0", minNodes: "1", minEdges: "2", bg: "#000000" });
  });
});

// ?panels=0 hides chrome an embedder can do without, and the diagram, the
// finding above it and the band below it are not chrome: they are the answer.
describe("the diagram alone", () => {
  it("keeps the answer when the chrome is turned off", () => {
    expect(pageParts(read("?panels=0"))).toEqual({
      header: false,
      controls: false,
      key: false,
      detail: false,
      finding: true,
      diagram: true,
      outside: true,
      review: true,
    });
  });

  it("draws everything by default, and drops only the band with ?strays=0", () => {
    expect(Object.values(pageParts(read(""))).every(Boolean)).toBe(true);
    expect(pageParts(read("?strays=0"))).toMatchObject({ outside: false, diagram: true, finding: true });
  });
});

describe("what the server is asked", () => {
  it("is nothing at all by default", () => {
    expect(ontologyQuery(read(""))).toEqual({});
  });

  it("sends gap=0, because it changes the server's work and not just the paint", () => {
    expect(ontologyQuery(read("?gap=0&schema=transit"))).toEqual({ gap: "0", schema: "transit" });
  });

  it("keeps paint-only switches to itself", () => {
    expect(ontologyQuery(read("?strays=0&panels=0&bg=ffffff"))).toEqual({});
  });

  it("asks for a draft with its thresholds, and never with gap=0", () => {
    // A draft reads the counts as part of deriving it, so there is no
    // overlay to switch off.
    expect(ontologyQuery(read("?draft=1&gap=0&min=0&min_edges=5"))).toEqual({ draft: "1", min: "0", min_edges: "5" });
  });

  it("sends thresholds only with a draft", () => {
    expect(ontologyQuery(read("?min=2"))).toEqual({});
  });

  it("keys a question the same way whatever order it was built in", () => {
    expect(queryKey({ schema: "s", draft: "1" })).toBe(queryKey({ draft: "1", schema: "s" }));
    expect(queryKey({ draft: "1" })).not.toBe(queryKey({}));
  });
});

describe("a read that never arrived", () => {
  it("is a saved report nobody could ask, not an empty schema", () => {
    const rep = failedAnswer(false, "network error");
    expect(rep.state).toBe("unreadable");
    expect(rep.draft).toBeUndefined();
    expect(rep.reason).toBe("network error");
  });

  it("stays a draft when a draft was asked for", () => {
    const rep = failedAnswer(true, "timeout");
    expect(rep.draft).toBe(true);
    expect(rep.state).toBe("undraftable");
  });
});

describe("the live stream", () => {
  it("names the newest change to a saved schema, and nothing else", () => {
    expect(latestSchemaChange([])).toBe(0);
    expect(
      latestSchemaChange([
        { seq: 9, kind: "node" },
        { seq: 7, kind: "ontology_schema" },
        { seq: 4, kind: "ontology_schema" },
        { seq: 12, kind: "edge" },
      ]),
    ).toBe(7);
  });
});

describe("the link to the versions page", () => {
  it("names the saved schema on screen, as a router path", () => {
    expect(versionsPath(transit())).toBe("/ontology-history?schema=transit");
    expect(versionsPath(transit({ schema_id: "a b" }))).toBe("/ontology-history?schema=a%20b");
  });

  it("names the schema a draft was held against, since a draft has no versions", () => {
    expect(versionsPath(draft({ against: "transit" }))).toBe("/ontology-history?schema=transit");
    expect(versionsPath(draft())).toBe("/ontology-history");
  });

  it("names nothing when nothing is saved", () => {
    expect(versionsPath(absent())).toBe("/ontology-history");
    expect(versionsPath(null)).toBe("/ontology-history");
  });
});
