import { describe, expect, it } from "vitest";
import { DRAFT_STATES, ONTOLOGY_STATES, type OntologyAnswer } from "@/api/ontology";
import { againstSummary, finding, headerOf, outsideTheModel, schemaChoices, textOf } from "./ontologyFinding";
import { absent, draft, noUsage, transit } from "./ontologyFixtures";

const say = (rep: OntologyAnswer) => textOf(finding(rep).runs);
const foot = (rep: OntologyAnswer) => finding(rep).foot.map(textOf).join("\n");

// The sentence each state is named by, worded as the page Go served worded it.
const SAVED: Record<string, string> = {
  [ONTOLOGY_STATES.unreadable]: "This view cannot be asked for an ontology.",
  [ONTOLOGY_STATES.absent]: "No ontology is saved in this store.",
  [ONTOLOGY_STATES.unused]: "An ontology is saved, and nothing in the store uses it.",
};
const DRAFTED: Record<string, string> = {
  [DRAFT_STATES.drafted]: "Nobody has saved this.",
  [DRAFT_STATES.nothingToDraft]: "There is nothing here to draft from.",
  [DRAFT_STATES.undraftable]: "This source cannot draft an ontology.",
};
const GAP = "The gap was not measured";
const EVERY = [...Object.values(SAVED), ...Object.values(DRAFTED), GAP];

function only(text: string, sentence: string) {
  expect(text).toContain(sentence);
  for (const other of EVERY) if (other !== sentence) expect(text).not.toContain(other);
}

// Four states, four sentences. A page that branched on a list length instead
// would render "nobody asked", "nothing is saved" and "you turned the count
// off" as the same empty diagram.
describe("the finding for a saved-schema answer", () => {
  it("has its own sentence for each state", () => {
    only(say(absent({ state: "unreadable", available: false, reason: "no hook", usage: noUsage("") })), SAVED.unreadable as string);
    only(say(absent()), SAVED.absent as string);
    only(say(transit({ state: "unused" })), SAVED.unused as string);
    only(say(transit({ usage: noUsage("declared vs actual is switched off") })), GAP);
  });

  it("states what a live schema is held against when the gap was measured", () => {
    const text = say(transit());
    expect(text).toBe(
      "7 object types and 6 link types declared, against 22 nodes and 15 edges in the store. 3 declarations are at zero — declared, never used. 5 types are in the data that the schema does not describe.",
    );
    expect(foot(transit())).toContain("counted over every row");
  });

  it("words what was counted in the page's language", () => {
    const graph = transit({ usage: { ...transit().usage, counted: "graph", returned: 2000, total: 8465 } });
    expect(finding(graph, "zh").foot.map(textOf).join("\n")).toContain("而且只返回了 8,465 个节点中连接最多的 2,000 个");
    expect(finding(graph, "en").foot.map(textOf).join("\n")).toContain("only the 2,000 most-connected of 8,465 nodes came back");
    const store = transit({ usage: { ...transit().usage, counted: "store" } });
    expect(finding(store, "zh").foot.map(textOf).join("\n")).toContain("存储中的每一行");
    expect(finding(store, "zh").foot.map(textOf).join("\n")).not.toContain("counted over");
  });

  it("lists the store's own record kinds apart from what the schema left out", () => {
    const rep = transit();
    rep.usage = { ...rep.usage, store_nodes: [{ name: "memory", count: 2967 }], store_edges: [{ name: "mentions", count: 8333 }] };
    const en = finding(rep).foot.map((line) => textOf(line)).join("\n");
    expect(en).toContain("the store's own records: memory 2,967 · mentions 8,333");
    const zh = finding(rep, "zh").foot.map((line) => textOf(line)).join("\n");
    expect(zh).toContain("存储自身的记录：memory 2,967 · mentions 8,333");
    // A server before v2.151 sends neither list, and the line is not drawn.
    expect(finding(transit()).foot.map((line) => textOf(line)).join("\n")).not.toContain("own records");
  });

  it("says when everything declared is in use and nothing is outside", () => {
    const clean = transit({ declared_unused_types: 0, declared_unused_links: 0, usage: { ...transit().usage, undeclared_node_types: 0, undeclared_edge_types: 0 } });
    expect(say(clean)).toContain("Everything declared is in use and nothing in the store is outside the model.");
  });

  it("branches on the state Go decided, never on how long a list is", () => {
    // Identical empty lists, two different findings.
    const nobodyAsked = absent({ state: "unreadable", usage: noUsage("") });
    const nothingSaved = absent({ usage: noUsage("") });
    expect(say(nobodyAsked)).not.toBe(say(nothingSaved));
    // An absent answer that somehow carries boxes is still absent.
    only(say(absent({ object_types: transit().object_types })), SAVED.absent as string);
    // A live schema with no boxes at all still gets the live sentence.
    expect(say(transit({ object_types: [], link_types: [] }))).toContain("0 object types and 0 link types declared, against 22 nodes");
  });

  it("gives the reason the gap was not measured, and never calls a type unused on a count nobody took", () => {
    const rep = transit({ usage: noUsage("node type counts: disk I/O error"), declared_unused_types: 0, declared_unused_links: 0 });
    expect(say(rep)).toContain(GAP);
    expect(say(rep)).not.toContain("never used");
    expect(foot(rep)).toBe("node type counts: disk I/O error");
  });

  it("tells a reader how big a store with no schema is", () => {
    expect(say(absent())).toContain("Its 22 nodes carry 9 distinct types and its 15 edges carry 6 — a vocabulary that exists and was never written down.");
    expect(say(absent({ usage: noUsage() }))).not.toContain("Its ");
  });

  it("says so when it meets a state it has never heard of, rather than guessing", () => {
    const rep = transit({ state: "half-read", reason: "new server" });
    expect(say(rep)).toBe("This report arrived in a state this page does not know how to read.");
    expect(foot(rep)).toContain("half-read · new server");
    expect(finding(rep).tone).toBe("bad");
  });
});

// Three more states, three more sentences, and none of them may be one of the
// saved four. A page that fell through to the saved branch would describe a
// derivation nobody signed in the words of a schema somebody did.
describe("the finding for a draft", () => {
  it("has its own sentence for each draft state", () => {
    only(say(draft()), DRAFTED.drafted as string);
    only(say(draft({ state: "nothing-to-draft" })), DRAFTED["nothing-to-draft"] as string);
    only(say(draft({ state: "undraftable", reason: "unknown tool" })), DRAFTED.undraftable as string);
    expect(foot(draft({ state: "undraftable", reason: "unknown tool" }))).toBe("unknown tool");
  });

  it("is read off the draft flag, so a draft never takes a saved schema's sentence", () => {
    // A state word from the saved four arriving on a draft payload.
    for (const state of Object.values(ONTOLOGY_STATES)) {
      const text = say(draft({ state }));
      for (const saved of [...Object.values(SAVED), GAP]) expect(text).not.toContain(saved);
      expect(text).toContain("This draft arrived in a state this page does not know how to read.");
    }
    // And a draft's word on a saved payload is not a draft.
    expect(say(transit({ state: "drafted" }))).not.toContain(DRAFTED.drafted);
  });

  it("names where saving happens, so it never looks like this page's job", () => {
    expect(say(draft())).toContain("through ontology_save.");
  });

  it("counts what it proposes against what it read, and the questions waiting", () => {
    expect(say(draft())).toContain("7 object types and 6 link types proposed over the 22 nodes and 15 edges that were read.");
    expect(say(draft())).toContain("2 questions are waiting for a decision below.");
    expect(say(draft({ decisions_total: 1 }))).toContain("1 question is waiting");
    expect(say(draft({ decisions_total: 0 }))).not.toContain("waiting for a decision");
  });

  it("states the threshold it ran with and what that kept out of the drawing", () => {
    const text = foot(draft());
    expect(text).toContain("Drawn at min_nodes=3, min_edges=3.");
    expect(text).toContain("That kept 4 node types (6 nodes) and 3 edge types (4 edges) out of the drawing — not out of the counts.");
    expect(text).toContain("The store carries 9 node types and 6 edge types in all.");
  });

  it("says when the threshold kept nothing out, because silence would mean either", () => {
    const text = foot(draft({ min_nodes: 0, min_edges: 0, pruned_node_types: 0, pruned_edge_types: 0, pruned_nodes: 0, pruned_edges: 0 }));
    expect(text).toContain("Drawn at min_nodes=0, min_edges=0.");
    expect(text).toContain("Nothing was kept out of the drawing by the threshold");
  });

  it("carries the deriver's notes in its own words", () => {
    expect(finding(draft()).notes).toEqual(["Nothing here was saved. Read it, correct it, then save it with ontology_save."]);
  });
});

describe("the header", () => {
  it("marks a draft as one, and gives it none of a saved schema's pills", () => {
    const h = headerOf(draft({ against: "transit" }));
    expect(h.title).toBe("draft");
    expect(h.detail).toBe("derived, not saved");
    expect(h.pills.find((p) => p.tone === "draft")?.label).toContain("draft");
    expect(h.pills.map((p) => p.label)).toEqual(["draft · nobody signed this", "vocabulary", "min 3 / 3", "vs transit"]);
    expect(h.pills.some((p) => p.label === "active" || p.label === "inactive")).toBe(false);
  });

  it("says what a draft that did not happen is", () => {
    expect(headerOf(draft({ state: "undraftable" }))).toEqual({ title: null, detail: "no draft from this source", pills: [] });
    expect(headerOf(draft({ state: "nothing-to-draft" }))).toEqual({ title: null, detail: "nothing to draft from", pills: [] });
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
    expect(headerOf(absent()).detail).toBe("no schema saved");
    expect(headerOf(absent({ state: "unreadable" })).detail).toBe("no answer from this source");
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
    expect(band && textOf(band.runs)).toBe(
      "These are the types the store's own records carry, which this schema never declares. 9 of 22 nodes and 5 of 15 edges are under one of them.",
    );
    expect(band?.groups.map((g) => g.label)).toEqual(["node types · 3", "edge types · 2"]);
    expect(band?.groups[0]?.chips).toEqual([
      { name: "concept", count: "5" },
      { name: "memory", count: "3" },
      { name: null, count: "1" },
    ]);
  });

  it("is the whole vocabulary on a store with no schema", () => {
    expect(textOf(outsideTheModel(absent(), true)?.runs ?? [])).toContain("There is no schema, so every type the store uses is here.");
  });

  it("is the deriver's own exclusions on a draft", () => {
    expect(textOf(outsideTheModel(draft(), true)?.runs ?? [])).toContain("The draft is silent about these.");
  });

  it("is hidden when switched off, or when the count was never taken", () => {
    expect(outsideTheModel(transit(), false)).toBeNull();
    expect(outsideTheModel(transit({ usage: noUsage() }), true)).toBeNull();
  });

  it("says how many it could not list, rather than a list that quietly stops", () => {
    const u = transit().usage;
    const band = outsideTheModel(transit({ usage: { ...u, undeclared_node_types: 45, node_list_truncated: true } }), true);
    expect(band?.groups[0]?.more).toBe("and 42 more");
    expect(band?.groups[1]?.more).toBeNull();
  });
});

describe("a draft held against a saved schema", () => {
  it("says nothing when there is no saved schema to hold it against", () => {
    expect(againstSummary(draft())).toBeNull();
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
    expect(textOf(againstSummary(rep) ?? [])).toContain("26 changes, of which 1 listed here would stop something already stored from validating.");
    expect(textOf(againstSummary({ ...rep, breaking: false }) ?? [])).toContain("none of which would invalidate anything already stored.");
  });
});

describe("the finding in Chinese", () => {
  it("says each state in Chinese, with the same numbers set off", () => {
    expect(textOf(finding(absent(), "zh").runs)).toContain("这个存储没有保存本体。");
    const live = finding(transit(), "zh");
    expect(textOf(live.runs)).toBe("声明了 7 个对象类型和 6 个链接类型，对照存储中的 22 个节点和 15 条边。3 项声明为零——声明了，从未使用。数据中有 5 种类型是模式没有描述的。");
    expect(live.runs.filter((r) => typeof r !== "string" && "b" in r)).toEqual(finding(transit()).runs.filter((r) => typeof r !== "string" && "b" in r));
  });

  it("keeps the names the schema declares as they are", () => {
    const h = headerOf(transit(), "zh");
    expect(h.pills[0]?.label).toBe("生效中");
    expect(h.detail).toMatch(/^v\d/);
    expect(textOf(finding(draft(), "zh").runs)).toContain("ontology_save");
  });
});
