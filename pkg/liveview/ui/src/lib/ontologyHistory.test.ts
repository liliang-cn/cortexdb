import { describe, expect, it } from "vitest";
import type { OntologyHistoryResponse, OntologyVersionInfo } from "@/api/ontologyHistory";
import {
  activationView,
  appendOlder,
  applyRefresh,
  arrivals,
  defaultSchema,
  endWords,
  HISTORY_MAX_LIMIT,
  isNoHistory,
  listFromPage,
  newestSchemaChange,
  olderOffset,
  pairOf,
  pickRole,
  picksFromParams,
  picksToParams,
  picksWithin,
  previousPair,
  refreshLimit,
  renamedTo,
  picksKey,
  samePickSet,
  settleEcho,
  sameMinute,
  schemaOptions,
  schemaToParams,
  summaryView,
  togglePick,
  type VersionList,
} from "./ontologyHistory";

describe("picking two versions", () => {
  it("picks, unpicks, and keeps the two most recent picks", () => {
    let p = togglePick([], 5);
    expect(p).toEqual([5]);
    p = togglePick(p, 2);
    expect(p).toEqual([5, 2]);
    p = togglePick(p, 9);
    expect(p).toEqual([2, 9]);
    p = togglePick(p, 2);
    expect(p).toEqual([9]);
  });

  it("compares older to newer whatever the order of picking", () => {
    expect(pairOf([9, 2])).toEqual({ from: 2, to: 9 });
    expect(pairOf([2, 9])).toEqual({ from: 2, to: 9 });
    expect(pairOf([4])).toBeNull();
    expect(pairOf([])).toBeNull();
  });

  it("names each picked version's side", () => {
    expect(pickRole([9, 2], 2)).toBe("from");
    expect(pickRole([9, 2], 9)).toBe("to");
    expect(pickRole([4], 4)).toBe("picked");
    expect(pickRole([4], 5)).toBeNull();
  });

  it("compares a version with the one before it, except the first", () => {
    expect(previousPair(7)).toEqual({ from: 6, to: 7 });
    expect(previousPair(1)).toBeNull();
  });

  it("treats the same versions in another order as the same picks", () => {
    expect(samePickSet([12, 3], [3, 12])).toBe(true);
    expect(samePickSet([3], [3, 12])).toBe(false);
    expect(samePickSet([3, 9], [3, 12])).toBe(false);
    expect(samePickSet([], [])).toBe(true);
  });

  it("keys picks whatever their order", () => {
    expect(picksKey([12, 3])).toBe("3,12");
    expect(picksKey([3, 12])).toBe("3,12");
    expect(picksKey([])).toBe("");
  });

  it("tells the URL catching up with the page's own writes from a change made elsewhere", () => {
    // Two quick picks: the page wrote "31" then "31,33".
    let w = ["31", "31,33"];
    // The router may skip straight to the last write...
    expect(settleEcho(w, "31,33")).toEqual({ echo: true, written: [] });
    // ...or pass through the first, which is still an echo, and the second is still to come.
    const mid = settleEcho(w, "31");
    expect(mid).toEqual({ echo: true, written: ["31,33"] });
    w = mid.written;
    expect(settleEcho(w, "31,33")).toEqual({ echo: true, written: [] });
    // Back, or a link, names something the page did not write: it wins.
    expect(settleEcho(["31,33"], "3,12")).toEqual({ echo: false, written: [] });
    expect(settleEcho([], "")).toEqual({ echo: false, written: [] });
  });

  it("drops picks the schema never had", () => {
    expect(picksWithin([3, 40], 33)).toEqual([3]);
    expect(picksWithin([0, 2], 33)).toEqual([2]);
  });
});

describe("the picks in the URL", () => {
  it("reads a pair, a lone pick, and nothing", () => {
    expect(picksFromParams(new URLSearchParams("schema=people&from=3&to=12"))).toEqual([3, 12]);
    expect(picksFromParams(new URLSearchParams("from=7"))).toEqual([7]);
    expect(picksFromParams(new URLSearchParams("to=7"))).toEqual([7]);
    expect(picksFromParams(new URLSearchParams("schema=people"))).toEqual([]);
  });

  it("ignores what is not a version and reads the same version twice as one pick", () => {
    expect(picksFromParams(new URLSearchParams("from=0&to=x"))).toEqual([]);
    expect(picksFromParams(new URLSearchParams("from=-2&to=1.5"))).toEqual([]);
    expect(picksFromParams(new URLSearchParams("from=4&to=4"))).toEqual([4]);
  });

  it("writes the older version as from and keeps unrelated parameters", () => {
    const next = picksToParams([12, 3], new URLSearchParams("schema=people&mode=dark"));
    expect(next.get("from")).toBe("3");
    expect(next.get("to")).toBe("12");
    expect(next.get("schema")).toBe("people");
    expect(next.get("mode")).toBe("dark");
  });

  it("round-trips a lone pick and clears an empty one", () => {
    const one = picksToParams([7], new URLSearchParams("from=3&to=12"));
    expect(one.get("from")).toBe("7");
    expect(one.has("to")).toBe(false);
    expect(picksFromParams(one)).toEqual([7]);
    const none = picksToParams([], new URLSearchParams("schema=people&from=3&to=12"));
    expect(none.toString()).toBe("schema=people");
  });

  it("drops the picks when the schema changes", () => {
    const next = schemaToParams("places", new URLSearchParams("schema=people&from=3&to=12&mode=dark"));
    expect(next.toString()).toBe("schema=places&mode=dark");
  });
});

describe("choosing the schema", () => {
  const schemas = [
    { schema_id: "people", version: 3, active: false },
    { schema_id: "places", version: 1, active: true },
  ];
  it("opens the active schema, else the first", () => {
    expect(defaultSchema(schemas)).toBe("places");
    expect(defaultSchema([{ schema_id: "a", version: 1, active: false }])).toBe("a");
    expect(defaultSchema([])).toBe("");
  });
  it("offers a schema the URL names even when it was deleted", () => {
    expect(schemaOptions(schemas, "legacy").map((s) => s.schema_id)).toEqual(["people", "places", "legacy"]);
    expect(schemaOptions(schemas, "people")).toHaveLength(2);
  });
});

// ---- pages of versions ----------------------------------------------------

function version(n: number, over: Partial<OntologyVersionInfo> = {}): OntologyVersionInfo {
  return { version: n, name: "People", saved_at: new Date(1_700_000_000_000 + n * 60_000).toISOString(), active: false, activations: [], ...over };
}

/**
 * The page the server would answer for a schema of `total` versions, newest
 * first, at an offset: the same arithmetic as cortexdb.OntologyHistory.
 */
function serverPage(total: number, offset: number, limit: number, over: (n: number) => Partial<OntologyVersionInfo> = () => ({})): OntologyHistoryResponse {
  const versions: OntologyVersionInfo[] = [];
  for (let n = total - offset; n >= 1 && versions.length < limit; n--) versions.push(version(n, over(n)));
  return { schema_id: "people", exists: true, current_version: total, total, has_more: offset + versions.length < total, versions };
}

const numbers = (l: VersionList) => l.versions.map((v) => v.version);

describe("joining pages", () => {
  it("reads a first page and says there is more", () => {
    const l = listFromPage(serverPage(33, 0, 25));
    expect(l.versions).toHaveLength(25);
    expect(l.versions[0]?.version).toBe(33);
    expect(l.hasMore).toBe(true);
    expect(olderOffset(l)).toBe(25);
  });

  it("joins an older page under it with no gap and no repeat", () => {
    const l = appendOlder(listFromPage(serverPage(33, 0, 25)), serverPage(33, 25, 25));
    expect(numbers(l)).toEqual(Array.from({ length: 33 }, (_, i) => 33 - i));
    expect(l.hasMore).toBe(false);
  });

  it("does not show a version twice when one was saved between the pages", () => {
    const first = listFromPage(serverPage(33, 0, 25));
    // v34 arrived: the server's offset 25 now starts at v9, which the list already has.
    const l = appendOlder(first, serverPage(34, 25, 25));
    expect(numbers(l)).toEqual(Array.from({ length: 33 }, (_, i) => 33 - i));
    expect(l.total).toBe(33);
    // The re-read the live change triggers then brings v34 in and loses nothing.
    const after = applyRefresh(l, serverPage(34, 0, refreshLimit(l)));
    expect(numbers(after)).toEqual(Array.from({ length: 34 }, (_, i) => 34 - i));
    expect(after.hasMore).toBe(false);
  });
});

describe("re-reading after a live change", () => {
  it("asks for what is shown plus room for the new ones, within the server's cap", () => {
    expect(refreshLimit(null)).toBe(25);
    expect(refreshLimit(listFromPage(serverPage(60, 0, 50)), 25)).toBe(75);
    expect(refreshLimit({ ...listFromPage(serverPage(600, 0, 500)) })).toBe(HISTORY_MAX_LIMIT);
  });

  it("adds the new version at the top and keeps every version the reader had loaded", () => {
    const shown = appendOlder(listFromPage(serverPage(60, 0, 25)), serverPage(60, 25, 25)); // v60..v11
    const after = applyRefresh(shown, serverPage(61, 0, refreshLimit(shown)));
    expect(after.versions[0]?.version).toBe(61);
    expect(after.versions.at(-1)?.version).toBe(11);
    expect(after.versions).toHaveLength(51);
    expect(after.total).toBe(61);
    expect(after.hasMore).toBe(true);
    // And the next older page carries on from v10.
    const older = appendOlder(after, serverPage(61, olderOffset(after), 25));
    expect(older.versions.at(-1)?.version).toBe(1);
    expect(new Set(numbers(older)).size).toBe(61);
  });

  it("refreshes what it covers: the old active version's interval has closed", () => {
    const shown = listFromPage(serverPage(5, 0, 25, (n) => (n === 5 ? { active: true } : {})));
    const after = applyRefresh(shown, serverPage(6, 0, 30, (n) => (n === 6 ? { active: true } : {})));
    expect(after.versions.find((v) => v.version === 5)?.active).toBe(false);
    expect(after.versions.find((v) => v.version === 6)?.active).toBe(true);
  });

  it("keeps the list as it was when nothing arrived", () => {
    const shown = listFromPage(serverPage(10, 0, 25));
    expect(numbers(applyRefresh(shown, serverPage(10, 0, 50)))).toEqual(numbers(shown));
  });

  it("starts over rather than leave a hole when more arrived than the re-read holds", () => {
    const shown = listFromPage(serverPage(30, 0, 5)); // v30..v26
    const after = applyRefresh(shown, serverPage(60, 0, 10)); // v60..v51: v50..v31 unread
    expect(numbers(after)).toEqual([60, 59, 58, 57, 56, 55, 54, 53, 52, 51]);
    expect(after.hasMore).toBe(true);
  });

  it("names the versions a re-read brought in", () => {
    const shown = listFromPage(serverPage(10, 0, 25));
    expect(arrivals(shown, applyRefresh(shown, serverPage(12, 0, 50)))).toEqual([12, 11]);
    expect(arrivals(shown, shown)).toEqual([]);
    expect(arrivals(null, shown)).toEqual([]);
  });

  it("says when the schema was deleted while its history stays", () => {
    const shown = listFromPage(serverPage(3, 0, 25));
    const page = { ...serverPage(3, 0, 25), exists: false, current_version: undefined };
    const after = applyRefresh(shown, page);
    expect(after.exists).toBe(false);
    expect(after.currentVersion).toBe(0);
    expect(after.versions).toHaveLength(3);
  });
});

describe("the words a version says", () => {
  it("names a rename on the version that made it, and nowhere else", () => {
    const vs = [version(4, { name: "Persons" }), version(3, { name: "People" }), version(2, { name: "People" })];
    expect(renamedTo(vs, 0)).toBe("Persons");
    expect(renamedTo(vs, 1)).toBe("");
    // The version before the oldest shown is not loaded: nothing to compare with.
    expect(renamedTo(vs, 2)).toBe("");
    expect(renamedTo([version(9, { name: "A" }), version(7, { name: "B" })], 0)).toBe("");
  });

  it("tells the five kinds of summary apart", () => {
    expect(summaryView(undefined).kind).toBe("unrecorded");
    expect(summaryView({ initial: true, definition_changed: true, changes: 0, breaking: 0, has_breaking_changes: false }).kind).toBe("initial");
    expect(summaryView({ definition_changed: false, changes: 0, breaking: 0, has_breaking_changes: false }).kind).toBe("unchanged");
    expect(summaryView({ definition_changed: true, changes: 0, breaking: 0, has_breaking_changes: false }).kind).toBe("uncounted");
    expect(
      summaryView({ definition_changed: true, changes: 3, breaking: 1, has_breaking_changes: true, by_kind: { property_added: 2, property_removed: 1 } }),
    ).toEqual({ kind: "changes", total: 3, counts: { added: 2, removed: 1, changed: 0, breaking: 1 } });
  });

  it("still counts changes when the summary has no breakdown", () => {
    const v = summaryView({ definition_changed: true, changes: 2, breaking: 0, has_breaking_changes: false });
    expect(v).toEqual({ kind: "changes", total: 2, counts: { added: 0, removed: 0, changed: 2, breaking: 0 } });
  });

  it("says why an activation ended", () => {
    expect(endWords("replaced")).toBe("replaced by a newer version");
    expect(endWords("switched")).toBe("another schema was activated");
    expect(endWords("some_new_reason")).toBe("some new reason");
    expect(endWords(undefined)).toBe("");
  });

  it("reads an open and a closed activation", () => {
    const open = activationView({ schema_id: "people", version: 3, active_from: "2026-11-06T09:07:02.443743Z", activated_by: "leo" });
    expect(open.to).toBeNull();
    expect(open.activatedBy).toBe("leo");
    expect(open.from).toBe(Date.parse("2026-11-06T09:07:02.443Z"));
    const closed = activationView({
      schema_id: "people",
      version: 2,
      active_from: "2026-11-06T09:06:00Z",
      active_to: "2026-11-06T09:07:00Z",
      ended_by: "mia",
      end_reason: "switched",
    });
    expect(closed.to).toBe(Date.parse("2026-11-06T09:07:00Z"));
    expect(closed.ended).toBe("another schema was activated");
    expect(closed.endedBy).toBe("mia");
  });

  it("knows when both ends of an interval print as one minute", () => {
    expect(sameMinute(Date.parse("2026-11-06T09:07:01Z"), Date.parse("2026-11-06T09:07:59Z"))).toBe(true);
    expect(sameMinute(Date.parse("2026-11-06T09:07:59Z"), Date.parse("2026-11-06T09:08:00Z"))).toBe(false);
  });

  it("reads the route's 404 as an empty history and anything else as a failure", () => {
    expect(isNoHistory(404)).toBe(true);
    expect(isNoHistory(502)).toBe(false);
    expect(isNoHistory(0)).toBe(false);
    expect(isNoHistory(null)).toBe(false);
  });
});

describe("live changes to a schema", () => {
  const c = (seq: number, kind: string, id: string) => ({ seq, kind, id });
  const ring = [c(9, "edge", "people"), c(8, "ontology_schema", "places"), c(6, "ontology_schema", "people"), c(3, "node", "x")];
  it("finds the newest change to the schema's own row", () => {
    expect(newestSchemaChange(ring, "people")).toBe(6);
    expect(newestSchemaChange(ring, "places")).toBe(8);
    expect(newestSchemaChange(ring, "legacy")).toBe(0);
  });
  it("matches every schema when none is named, and nothing but schema rows", () => {
    expect(newestSchemaChange(ring, null)).toBe(8);
    expect(newestSchemaChange([c(4, "edge", "people")], null)).toBe(0);
  });
});
