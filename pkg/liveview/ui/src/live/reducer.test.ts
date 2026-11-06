import { describe, expect, it } from "vitest";
import type { ActivityEvent, ChangeItem, Delta, Edge, Node, SnapshotPayload } from "@/api";
import {
  ACTIVITY_LIMIT,
  CHANGES_LIMIT,
  TOUCH_TTL_MS,
  applyActivity,
  applyChange,
  applyDelta,
  applyReset,
  applySnapshot,
  edgeKey,
  initialData,
  type LiveData,
  mergeChangeItems,
  pinTo,
  sweepTouched,
} from "./reducer";

const leo: Node = { id: "entity:leo", label: "Leo", type: "person" };
const beijing: Node = { id: "entity:beijing", label: "Beijing", type: "place" };
const chengdu: Node = { id: "entity:chengdu", label: "Chengdu", type: "place" };
const livesBeijing: Edge = { id: "e1", source: leo.id, target: beijing.id, label: "lives_in" };
const livesChengdu: Edge = { id: "e2", source: leo.id, target: chengdu.id, label: "lives_in" };

function snapshot(over: Partial<SnapshotPayload> = {}): SnapshotPayload {
  return {
    version: 1,
    nodes: [leo, beijing],
    edges: [livesBeijing],
    events: [],
    source: "test",
    activity: true,
    interval_ms: 2000,
    cursor: 10,
    changes: true,
    ...over,
  };
}

function delta(over: Partial<Delta> = {}): Delta {
  return {
    version: 2,
    added_nodes: [],
    removed_nodes: [],
    added_edges: [],
    removed_edges: [],
    nodes: 0,
    edges: 0,
    ...over,
  };
}

function change(seq: number, over: Partial<ChangeItem> = {}): ChangeItem {
  return { seq, at: 1000 + seq, kind: "edge", op: "added", id: `e${seq}`, summary: `change ${seq}`, entities: [], ...over };
}

function event(seq: number, at = 1000 + seq): ActivityEvent {
  return { seq, at, kind: "query", tool: "search", text: `call ${seq}` };
}

const NOW = 100_000;

describe("a snapshot", () => {
  it("fills the graph, the capabilities and the cursor", () => {
    const s = applySnapshot(initialData(), snapshot({ grades: true }), NOW);
    expect(s.ready).toBe(true);
    expect([...s.nodes.keys()]).toEqual([leo.id, beijing.id]);
    expect(s.edges.size).toBe(1);
    expect(s.caps).toMatchObject({ grades: true, activity: true, changes: true, records: false });
    expect(s.cursor).toBe(10);
    expect(Object.keys(s.touched)).toEqual([]);
  });

  it("keeps the object of every row that did not change", () => {
    const first = applySnapshot(initialData(), snapshot(), NOW);
    const second = applySnapshot(first, snapshot({ version: 2, nodes: [{ ...leo }, { ...beijing, label: "Peking" }] }), NOW);
    expect(second.nodes.get(leo.id)).toBe(first.nodes.get(leo.id));
    expect(second.nodes.get(beijing.id)).not.toBe(first.nodes.get(beijing.id));
    expect(second.edges.get(edgeKey(livesBeijing))).toBe(first.edges.get(edgeKey(livesBeijing)));
  });

  it("lights what moved while the page was away, but not the first snapshot", () => {
    const first = applySnapshot(initialData(), snapshot(), NOW);
    const second = applySnapshot(first, snapshot({ nodes: [leo, chengdu], edges: [livesChengdu] }), NOW);
    expect(second.touched[chengdu.id]?.op).toBe("added");
    expect(second.touched[beijing.id]?.op).toBe("deleted");
    expect(second.touched["e2"]?.op).toBe("added");
    expect(second.touched["e1"]?.op).toBe("deleted");
  });

  it("does not light a whole graph swapped by a pin", () => {
    const first = applySnapshot(initialData(), snapshot(), NOW);
    const pinned = applySnapshot(first, snapshot({ pinned: true, as_of: 5000, nodes: [leo], edges: [], cursor: 0 }), NOW);
    expect(pinned.pinned).toBe(true);
    expect(pinned.pinnedAt).toBe(5000);
    expect(pinned.touched).toEqual(first.touched);
    expect(pinned.cursor).toBe(first.cursor);
  });

  it("clears the changes when the feed went backwards", () => {
    let s = applySnapshot(initialData(), snapshot({ cursor: 10 }), NOW);
    s = applyChange(s, change(11), NOW);
    expect(s.changes).toHaveLength(1);
    s = applySnapshot(s, snapshot({ cursor: 3 }), NOW);
    expect(s.changes).toHaveLength(0);
    expect(s.cursor).toBe(3);
  });

  it("does not move the cursor ahead of the changes it has seen", () => {
    let s = applySnapshot(initialData(), snapshot({ cursor: 10 }), NOW);
    s = applyChange(s, change(11), NOW);
    s = applySnapshot(s, snapshot({ cursor: 15 }), NOW);
    expect(s.cursor).toBe(11);
  });

  it("merges the activity backlog without repeating an event it already holds", () => {
    let s = applySnapshot(initialData(), snapshot({ events: [event(1), event(2)] }), NOW);
    s = applySnapshot(s, snapshot({ events: [event(2), event(3)] }), NOW);
    expect(s.activity.map((e) => e.seq)).toEqual([3, 2, 1]);
  });

  it("keeps a restarted server's first events, whose seq repeats the old ones", () => {
    let s = applySnapshot(initialData(), snapshot({ events: [event(1, 1000)] }), NOW);
    s = applySnapshot(s, snapshot({ events: [event(1, 9000)] }), NOW);
    expect(s.activity).toHaveLength(2);
    expect(s.activity[0]?.at).toBe(9000);
  });
});

describe("a delta", () => {
  const base = () => applySnapshot(initialData(), snapshot(), NOW);

  it("upserts and removes rows", () => {
    const s = applyDelta(
      base(),
      delta({ added_nodes: [chengdu], added_edges: [livesChengdu], removed_edges: [livesBeijing], removed_nodes: [beijing.id] }),
      NOW,
    );
    expect([...s.nodes.keys()].sort()).toEqual([chengdu.id, leo.id]);
    expect([...s.edges.values()].map((e) => e.id)).toEqual(["e2"]);
    expect(s.version).toBe(2);
  });

  it("keeps the identity of rows it re-sends unchanged", () => {
    const before = base();
    const after = applyDelta(before, delta({ added_nodes: [{ ...leo }], added_edges: [{ ...livesBeijing }] }), NOW);
    expect(after.nodes).toBe(before.nodes);
    expect(after.edges).toBe(before.edges);
    expect(after.nodes.get(leo.id)).toBe(before.nodes.get(leo.id));
  });

  it("changes only the row it touched when a label changes", () => {
    const before = base();
    const after = applyDelta(before, delta({ added_nodes: [{ ...beijing, label: "Peking" }] }), NOW);
    expect(after.nodes.get(leo.id)).toBe(before.nodes.get(leo.id));
    expect(after.nodes.get(beijing.id)?.label).toBe("Peking");
    expect(after.edges).toBe(before.edges);
    expect(after.touched[beijing.id]?.op).toBe("updated");
  });

  it("marks a new row added and a removed one deleted", () => {
    const s = applyDelta(base(), delta({ added_nodes: [chengdu], removed_nodes: [beijing.id] }), NOW);
    expect(s.touched[chengdu.id]).toEqual({ op: "added", at: NOW });
    expect(s.touched[beijing.id]).toEqual({ op: "deleted", at: NOW });
  });

  it("ignores a delta at or behind the version it already has", () => {
    const before = base();
    expect(applyDelta(before, delta({ version: 1, added_nodes: [chengdu] }), NOW)).toBe(before);
  });

  it("ignores deltas before the first snapshot and while pinned", () => {
    const empty = initialData();
    expect(applyDelta(empty, delta({ added_nodes: [chengdu] }), NOW)).toBe(empty);
    const pinned = applySnapshot(initialData(), snapshot({ pinned: true, as_of: 1 }), NOW);
    expect(applyDelta(pinned, delta({ added_nodes: [chengdu] }), NOW)).toBe(pinned);
  });
});

describe("change rows", () => {
  it("are ordered newest first and counted once across a replay overlap", () => {
    let s = applySnapshot(initialData(), snapshot({ cursor: 0 }), NOW);
    for (const seq of [5, 6, 7]) s = applyChange(s, change(seq), NOW);
    // the reconnect replays 6 and 7 again, then continues with 8
    for (const seq of [6, 7, 8]) s = applyChange(s, change(seq), NOW);
    expect(s.changes.map((c) => c.seq)).toEqual([8, 7, 6, 5]);
    expect(s.cursor).toBe(8);
  });

  it("slot into place when they arrive out of order", () => {
    let s = initialData();
    for (const seq of [3, 9, 5]) s = applyChange(s, change(seq), NOW);
    expect(s.changes.map((c) => c.seq)).toEqual([9, 5, 3]);
  });

  it("return the same state for a row already held", () => {
    const s = applyChange(initialData(), change(4), NOW);
    expect(applyChange(s, change(4), NOW)).toBe(s);
  });

  it("keep at most CHANGES_LIMIT, dropping the oldest", () => {
    let s = initialData();
    for (let seq = 1; seq <= CHANGES_LIMIT + 25; seq++) s = applyChange(s, change(seq), NOW);
    expect(s.changes).toHaveLength(CHANGES_LIMIT);
    expect(s.changes[0]?.seq).toBe(CHANGES_LIMIT + 25);
    expect(s.changes.at(-1)?.seq).toBe(26);
  });

  it("light the changed row and every entity they touched, with the reason's op", () => {
    const s = applyChange(initialData(), change(1, { op: "superseded", id: "e1", entities: [leo.id, beijing.id] }), NOW);
    expect(s.touched["e1"]?.op).toBe("superseded");
    expect(s.touched[leo.id]?.op).toBe("superseded");
    expect(s.touched[beijing.id]?.op).toBe("superseded");
  });

  it("keep a specific op when the delta for the same row arrives right after", () => {
    let s = applySnapshot(initialData(), snapshot(), NOW);
    s = applyChange(s, change(11, { op: "superseded", id: "e1", entities: [] }), NOW);
    s = applyDelta(s, delta({ removed_edges: [livesBeijing] }), NOW + 50);
    expect(s.touched["e1"]?.op).toBe("superseded");
  });

  it("mergeChangeItems splices a fetched page into the live ring", () => {
    const live = [change(9), change(8)];
    const fetched = [change(8), change(7), change(6)];
    expect(mergeChangeItems(live, fetched).map((c) => c.seq)).toEqual([9, 8, 7, 6]);
    expect(mergeChangeItems(live, [])).toBe(live);
    expect(mergeChangeItems(live, [change(9)])).toBe(live);
  });
});

describe("a reset", () => {
  it("drops the changes and moves the cursor to the head", () => {
    let s = applySnapshot(initialData(), snapshot({ cursor: 10 }), NOW);
    s = applyChange(s, change(11), NOW);
    s = applyReset(s, { reason: "pruned", pruned_through: 500, head: 900 });
    expect(s.changes).toEqual([]);
    expect(s.cursor).toBe(900);
    expect(s.resets).toBe(1);
    // the fresh snapshot that follows replaces the graph
    s = applySnapshot(s, snapshot({ cursor: 900, nodes: [chengdu], edges: [] }), NOW);
    expect([...s.nodes.keys()]).toEqual([chengdu.id]);
    expect(s.cursor).toBe(900);
  });
});

describe("recently touched", () => {
  it("expires after its lifetime and not before", () => {
    const s = applyChange(initialData(), change(1, { entities: [leo.id] }), NOW);
    expect(sweepTouched(s, NOW + TOUCH_TTL_MS - 1)).toBe(s);
    const later = sweepTouched(s, NOW + TOUCH_TTL_MS);
    expect(later.touched).toEqual({});
  });

  it("drops only the entries that expired", () => {
    let s = applyChange(initialData(), change(1, { id: "old", entities: [] }), NOW);
    s = applyChange(s, change(2, { id: "new", entities: [] }), NOW + 4000);
    const swept = sweepTouched(s, NOW + TOUCH_TTL_MS + 1);
    expect(Object.keys(swept.touched)).toEqual(["new"]);
  });
});

describe("activity", () => {
  it("is newest first and capped at ACTIVITY_LIMIT", () => {
    let s = initialData();
    for (let seq = 1; seq <= ACTIVITY_LIMIT + 10; seq++) s = applyActivity(s, event(seq));
    expect(s.activity).toHaveLength(ACTIVITY_LIMIT);
    expect(s.activity[0]?.seq).toBe(ACTIVITY_LIMIT + 10);
    expect(s.activity.at(-1)?.seq).toBe(11);
  });

  it("does not repeat an event it already holds", () => {
    const s = applyActivity(initialData(), event(1));
    expect(applyActivity(s, event(1))).toBe(s);
  });
});

describe("pinning", () => {
  it("records the instant and clears an old refusal", () => {
    let s: LiveData = { ...initialData(), pinReason: "too old" };
    s = pinTo(s, 5000);
    expect(s.asOf).toBe(5000);
    expect(s.pinReason).toBeNull();
    expect(pinTo(s, 5000)).toBe(s);
    expect(pinTo(s, null).asOf).toBeNull();
  });
});
