import { describe, expect, it } from "vitest";
import { ONTOLOGY_STATES, type OntologyAnswer } from "@/api/ontology";
import { againstStats, finding, headerOf, outsideTheModel, schemaChoices, type Field } from "./ontologyFinding";
import { absent, draft, noUsage, transit } from "./ontologyFixtures";

const pairs = (fs: ReadonlyArray<Field>) => fs.map((f) => [f.label, f.value]);
const stats = (rep: OntologyAnswer, lang: "en" | "zh" = "en") => pairs(finding(rep, lang).stats);
const facts = (rep: OntologyAnswer, lang: "en" | "zh" = "en") => pairs(finding(rep, lang).facts);
const status = (rep: OntologyAnswer, lang: "en" | "zh" = "en") => finding(rep, lang).status;

// Every visible value is a label, a number, a status word or the server's own
// error. None of them is a sentence.
function noSentences(f: ReturnType<typeof finding>) {
  for (const v of [f.status ?? "", ...f.stats.flatMap((x) => [x.label, x.value]), ...f.facts.flatMap((x) => [x.label, x.value])]) {
    expect(v, v).not.toMatch(/[.。]$/);
  }
}

// One status word per state. A page that branched on a list length instead
// would render "nobody asked", "nothing is saved" and "you turned the count
// off" as the same empty diagram.
describe("the finding for a saved-schema answer", () => {
  it("has its own status for each state", () => {
    const words = [
      status(absent({ state: "unreadable", available: false, reason: "no hook", usage: noUsage("") })),
      status(absent()),
      status(transit({ state: "unused" })),
      status(transit({ usage: noUsage("node type counts: disk I/O error") })),
    ];
    expect(words).toEqual(["Unreadable", "No ontology", "Unused", "Not measured"]);
    expect(status(transit())).toBeNull();
  });

  it("puts a live schema's counts beside their labels", () => {
    expect(stats(transit())).toEqual([
      ["Object types", "7"],
      ["Link types", "6"],
      ["Nodes", "22"],
      ["Edges", "15"],
      ["Unused", "3"],
      ["Undeclared types", "5"],
    ]);
    expect(finding(transit()).stats.filter((f) => f.warn).map((f) => f.label)).toEqual(["Unused", "Undeclared types"]);
    noSentences(finding(transit()));
  });

  it("shows zero, not a sentence, when everything declared is in use", () => {
    const clean = transit({ declared_unused_types: 0, declared_unused_links: 0, usage: { ...transit().usage, undeclared_node_types: 0, undeclared_edge_types: 0 } });
    expect(stats(clean).slice(4)).toEqual([
      ["Unused", "0"],
      ["Undeclared types", "0"],
    ]);
    expect(finding(clean).stats.some((f) => f.warn)).toBe(false);
  });

  it("labels what was counted in the page's language", () => {
    const graph = transit({ usage: { ...transit().usage, counted: "graph", returned: 2000, total: 8465 } });
    expect(facts(graph)).toEqual([
      ["Counted over", "entity graph, chunks excluded"],
      ["Nodes returned", "2,000 / 8,465"],
    ]);
    expect(facts(graph, "zh")).toEqual([
      ["统计范围", "实体图，不含分块"],
      ["返回节点", "2,000 / 8,465"],
    ]);
    const store = transit({ usage: { ...transit().usage, counted: "store" } });
    expect(facts(store, "zh")).toEqual([["统计范围", "存储，含分块"]]);
    // A server that words the scope itself and sends no value gets no line.
    expect(facts(transit())).toEqual([]);
  });

  it("lists the store's own record kinds apart from what the schema left out", () => {
    const rep = transit();
    rep.usage = { ...rep.usage, store_nodes: [{ name: "memory", count: 2967 }], store_edges: [{ name: "mentions", count: 8333 }] };
    expect(facts(rep)).toContainEqual(["Store records", "memory 2,967 · mentions 8,333"]);
    expect(facts(rep, "zh")).toContainEqual(["存储自身记录", "memory 2,967 · mentions 8,333"]);
  });

  it("branches on the state Go decided, never on how long a list is", () => {
    const nobodyAsked = absent({ state: "unreadable", usage: noUsage("") });
    const nothingSaved = absent({ usage: noUsage("") });
    expect(status(nobodyAsked)).not.toBe(status(nothingSaved));
    // An absent answer that somehow carries boxes is still absent.
    expect(status(absent({ object_types: transit().object_types }))).toBe("No ontology");
    // A live schema with no boxes at all still gets the live counts.
    expect(stats(transit({ object_types: [], link_types: [] })).slice(0, 3)).toEqual([
      ["Object types", "0"],
      ["Link types", "0"],
      ["Nodes", "22"],
    ]);
  });

  it("gives the reason the gap was not measured, and never counts a declaration unused on a count nobody took", () => {
    const rep = transit({ usage: noUsage("node type counts: disk I/O error"), declared_unused_types: 0, declared_unused_links: 0 });
    expect(stats(rep)).toEqual([
      ["Object types", "7"],
      ["Link types", "6"],
    ]);
    expect(finding(rep).error).toBe("node type counts: disk I/O error");
  });

  it("says the gap is off when the reader switched it off, and shows no reason then", () => {
    const rep = transit({ usage: noUsage() });
    const f = finding(rep, "en", false);
    expect(pairs(f.facts)).toEqual([["Declared vs actual", "off"]]);
    expect(f.error).toBeNull();
    expect(pairs(finding(rep, "zh", false).facts)).toEqual([["声明与实际", "关闭"]]);
  });

  it("counts a store with no schema", () => {
    expect(stats(absent())).toEqual([
      ["Nodes", "22"],
      ["Node types", "9"],
      ["Edges", "15"],
      ["Edge types", "6"],
    ]);
    expect(stats(absent({ usage: noUsage() }))).toEqual([]);
  });

  it("names a state it has never heard of, rather than guessing", () => {
    const f = finding(transit({ state: "half-read", reason: "new server" }));
    expect(f.status).toBe("Unknown state");
    expect(f.error).toBe("half-read · new server");
    expect(f.tone).toBe("bad");
  });
});

// Three draft states, and none of them may wear a saved one's status. A page
// that fell through to the saved branch would describe a derivation nobody
// signed in the words of a schema somebody did.
describe("the finding for a draft", () => {
  it("has its own status for each draft state", () => {
    expect(status(draft())).toBe("Not saved");
    expect(status(draft({ state: "nothing-to-draft" }))).toBe("Nothing to draft");
    const failed = finding(draft({ state: "undraftable", reason: "unknown tool" }));
    expect(failed.status).toBe("Cannot draft");
    expect(failed.error).toBe("unknown tool");
  });

  it("is read off the draft flag, so a draft never takes a saved schema's status", () => {
    for (const state of Object.values(ONTOLOGY_STATES)) expect(status(draft({ state }))).toBe("Unknown state");
    expect(status(transit({ state: "drafted" }))).not.toBe("Not saved");
  });

  it("counts what it proposes, what it read and the questions waiting", () => {
    expect(stats(draft())).toEqual([
      ["Object types", "7"],
      ["Link types", "6"],
      ["Nodes", "22"],
      ["Edges", "15"],
      ["Node types", "9"],
      ["Edge types", "6"],
      ["To decide", "2"],
    ]);
    expect(finding(draft({ decisions_total: 0 })).stats.find((f) => f.label === "To decide")?.warn).toBeUndefined();
    noSentences(finding(draft()));
    noSentences(finding(draft(), "zh"));
  });

  it("states the threshold it ran with and what that kept out of the drawing", () => {
    expect(facts(draft())).toEqual([
      ["Threshold", "min_nodes=3 · min_edges=3"],
      ["Not drawn", "4 node types (6) · 3 edge types (4)"],
    ]);
    expect(facts(draft(), "zh")[1]).toEqual(["未绘制", "4 个节点类型（6）· 3 个边类型（4）"]);
  });

  it("shows zero when the threshold kept nothing out, because silence would mean either", () => {
    expect(facts(draft({ min_nodes: 0, min_edges: 0, pruned_node_types: 0, pruned_edge_types: 0, pruned_nodes: 0, pruned_edges: 0 }))).toEqual([
      ["Threshold", "min_nodes=0 · min_edges=0"],
      ["Not drawn", "0"],
    ]);
  });
});

describe("the header", () => {
  it("marks a draft as one, and gives it none of a saved schema's pills", () => {
    const h = headerOf(draft({ against: "transit" }));
    expect(h.title).toBe("draft");
    expect(h.detail).toBeNull();
    expect(h.pills.map((p) => p.label)).toEqual(["draft", "vocabulary", "min 3 / 3", "vs transit"]);
    expect(h.pills.some((p) => p.label === "active" || p.label === "inactive")).toBe(false);
  });

  it("says what a draft that did not happen is", () => {
    expect(headerOf(draft({ state: "undraftable" }))).toEqual({ title: null, detail: "no draft", pills: [] });
    expect(headerOf(draft({ state: "nothing-to-draft" }))).toEqual({ title: null, detail: "nothing to draft", pills: [] });
  });

  it("names a saved schema with its version, whether it is in effect, and how it enforces", () => {
    const h = headerOf(transit());
    expect(h.title).toBe("Transit");
    expect(h.detail).toBe("v1");
    expect(h.pills.map((p) => [p.label, p.tone])).toEqual([
      ["active", "on"],
      ["vocabulary", "warn"],
    ]);
    const strict = headerOf(transit({ active: false, enforcement: "strict", strict_actions: true, action_types: 2, object_sets: 1 }));
    expect(strict.pills.map((p) => p.label)).toEqual(["inactive", "strict", "strict actions", "2 actions", "1 object set"]);
  });

  it("claims no enforcement a source did not declare", () => {
    // A host's own Source.Ontology that leaves Enforcement empty.
    const h = headerOf(transit({ enforcement: undefined }));
    expect(h.pills.map((p) => p.label)).toEqual(["active"]);
    expect(headerOf(transit({ enforcement: "" })).pills.map((p) => p.label)).toEqual(["active"]);
  });

  it("tells nobody-asked from nothing-saved", () => {
    expect(headerOf(absent()).detail).toBe("no schema");
    expect(headerOf(absent({ state: "unreadable" })).detail).toBe("no answer");
  });
});

describe("the schema picker", () => {
  it("appears only when there is a choice to make", () => {
    expect(schemaChoices(transit({ schemas: [{ schema_id: "transit", version: 1, active: true }] }))).toEqual([]);
    expect(schemaChoices(draft())).toEqual([]);
    const choices = schemaChoices(transit({ schema_id: "TRANSIT" }));
    expect(choices.map((c) => [c.schema_id, c.current])).toEqual([
      ["transit", true],
      ["transit-lite", false],
    ]);
  });
});

describe("the band outside the model", () => {
  it("lists what nothing declares, with the record that named no type said as such", () => {
    const band = outsideTheModel(transit(), true);
    expect(band?.stats.map((f) => [f.label, f.value])).toEqual([
      ["Undeclared nodes", "9 / 22"],
      ["Undeclared edges", "5 / 15"],
    ]);
    expect(band?.groups.map((g) => g.label)).toEqual(["node types · 3", "edge types · 2"]);
    expect(band?.groups[0]?.chips).toEqual([
      { name: "concept", count: "5" },
      { name: "memory", count: "3" },
      { name: null, count: "1" },
    ]);
  });

  it("is hidden when switched off, or when the count was never taken", () => {
    expect(outsideTheModel(transit(), false)).toBeNull();
    expect(outsideTheModel(transit({ usage: noUsage() }), true)).toBeNull();
  });

  it("says how many it could not list, rather than a list that quietly stops", () => {
    const u = transit().usage;
    const band = outsideTheModel(transit({ usage: { ...u, undeclared_node_types: 45, node_list_truncated: true } }), true);
    expect(band?.groups[0]?.more).toBe("+42");
    expect(band?.groups[1]?.more).toBeNull();
  });
});

describe("a draft held against a saved schema", () => {
  it("shows nothing when there is no saved schema to hold it against", () => {
    expect(againstStats(draft())).toBeNull();
  });

  it("counts the changes and the ones that would break what is stored", () => {
    const rep = draft({
      against: "transit",
      against_version: 1,
      changes: [
        { kind: "object_type_removed", target: "Gate", detail: "", breaking: true },
        { kind: "object_type_added", target: "concept", detail: "", breaking: false },
      ],
      changes_total: 26,
      breaking: true,
    });
    expect(againstStats(rep)?.map((f) => [f.label, f.value, !!f.warn])).toEqual([
      ["Changes", "26", false],
      ["Breaking", "1", true],
    ]);
    expect(againstStats({ ...rep, breaking: false })?.map((f) => [f.label, f.value])).toEqual([
      ["Changes", "26"],
      ["Breaking", "0"],
    ]);
    expect(againstStats(rep, "zh")?.map((f) => f.label)).toEqual(["变更", "破坏性"]);
  });
});

describe("the finding in Chinese", () => {
  it("labels each count in Chinese, with the same numbers", () => {
    expect(status(absent(), "zh")).toBe("无本体");
    expect(stats(transit(), "zh")).toEqual([
      ["对象类型", "7"],
      ["链接类型", "6"],
      ["节点", "22"],
      ["边", "15"],
      ["未使用", "3"],
      ["未声明类型", "5"],
    ]);
    noSentences(finding(transit(), "zh"));
  });

  it("keeps the names the schema declares as they are", () => {
    const h = headerOf(transit(), "zh");
    expect(h.pills[0]?.label).toBe("生效中");
    expect(h.detail).toMatch(/^v\d/);
  });
});
