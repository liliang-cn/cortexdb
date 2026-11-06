// How a diff between two schema versions is read: every change sorted into
// added, removed or changed, and the changes gathered under the type they
// belong to, the way a code diff is gathered under its files. Pure, so it is
// tested directly.

import type { OntologyChange } from "@/api/ontologyHistory";

/** The three words every change is said with. */
export type DiffClass = "added" | "removed" | "changed";

export const DIFF_CLASS_ORDER: DiffClass[] = ["added", "removed", "changed"];

/** Whether a change is about an object type or a link type. */
export type DiffSubject = "object_type" | "link_type";

/**
 * The kinds cortexdb.DiffOntologySchemas reports, each with its class and the
 * part of the type it is about. Listed rather than parsed from the kind's
 * spelling: "property_became_required" ends in neither _added nor _removed,
 * yet a reader should see it as a change, and "required_property_added" is an
 * addition that breaks data, which the class must not hide.
 */
const KINDS: Record<string, { cls: DiffClass; subject: DiffSubject; part: string }> = {
  object_type_added: { cls: "added", subject: "object_type", part: "object type" },
  object_type_removed: { cls: "removed", subject: "object_type", part: "object type" },
  primary_key_changed: { cls: "changed", subject: "object_type", part: "primary key" },
  property_added: { cls: "added", subject: "object_type", part: "property" },
  required_property_added: { cls: "added", subject: "object_type", part: "required property" },
  property_removed: { cls: "removed", subject: "object_type", part: "property" },
  property_type_changed: { cls: "changed", subject: "object_type", part: "property type" },
  property_became_required: { cls: "changed", subject: "object_type", part: "property" },
  property_became_optional: { cls: "changed", subject: "object_type", part: "property" },
  link_type_added: { cls: "added", subject: "link_type", part: "link type" },
  link_type_removed: { cls: "removed", subject: "link_type", part: "link type" },
  link_side_retargeted: { cls: "changed", subject: "link_type", part: "side" },
  cardinality_tightened: { cls: "changed", subject: "link_type", part: "side" },
  cardinality_relaxed: { cls: "changed", subject: "link_type", part: "side" },
};

/**
 * The class of a kind. A kind this page does not know yet, from a newer
 * server, still gets a word: its own suffix when it says one, "changed"
 * otherwise, so nothing is ever drawn with colour alone or not at all.
 */
export function changeClass(kind: string): DiffClass {
  const known = KINDS[kind];
  if (known) return known.cls;
  if (kind.endsWith("_added")) return "added";
  if (kind.endsWith("_removed")) return "removed";
  return "changed";
}

export function changeSubject(kind: string): DiffSubject {
  return KINDS[kind]?.subject ?? (kind.startsWith("link_") || kind.startsWith("cardinality_") ? "link_type" : "object_type");
}

/** The words for what part of the type a change touched: "property", "side", "object type". */
export function changePart(kind: string): string {
  return KINDS[kind]?.part ?? kind.replace(/_(added|removed|changed)$/, "").replaceAll("_", " ");
}

export interface DiffEntry {
  change: OntologyChange;
  cls: DiffClass;
  /** The part words, "property" or "side". */
  part: string;
  /** What inside the type: "email" of "Person.email", "a" of "lives_in.a"; empty for the type itself. */
  member: string;
  /** The type as a whole was added or removed: the group's heading already says so. */
  whole: boolean;
}

export interface DiffGroup {
  /** The object or link type's API name. */
  name: string;
  subject: DiffSubject;
  /** Set when the type as a whole was added or removed. */
  whole: DiffClass | null;
  entries: DiffEntry[];
  breaking: number;
}

/** "Person.email" is the type "Person" and the member "email". API names carry no dots. */
export function splitTarget(target: string): { name: string; member: string } {
  const dot = target.indexOf(".");
  return dot < 0 ? { name: target, member: "" } : { name: target.slice(0, dot), member: target.slice(dot + 1) };
}

/**
 * The changes gathered by type: object types before link types, each set in
 * name order, and inside a type the change to the type itself before the
 * changes to its members, which keep the server's order (by target, then
 * kind). A type and a link type may share a name, so the subject is part of
 * the key.
 */
export function groupDiff(changes: ReadonlyArray<OntologyChange>): DiffGroup[] {
  // Three buckets per type: its own addition or removal, other changes to the
  // type itself (its primary key), then its members.
  const groups = new Map<string, { g: DiffGroup; whole: DiffEntry[]; self: DiffEntry[]; members: DiffEntry[] }>();
  for (const change of changes) {
    const subject = changeSubject(change.kind);
    const { name, member } = splitTarget(change.target);
    const key = `${subject}\u0000${name}`;
    let b = groups.get(key);
    if (!b) {
      b = { g: { name, subject, whole: null, entries: [], breaking: 0 }, whole: [], self: [], members: [] };
      groups.set(key, b);
    }
    const cls = changeClass(change.kind);
    const whole = member === "" && (change.kind === `${subject}_added` || change.kind === `${subject}_removed`);
    const entry: DiffEntry = { change, cls, part: changePart(change.kind), member, whole };
    if (whole) {
      b.g.whole = cls;
      b.whole.push(entry);
    } else if (member === "") {
      b.self.push(entry);
    } else {
      b.members.push(entry);
    }
    if (change.breaking) b.g.breaking++;
  }
  const subjectRank = (s: DiffSubject) => (s === "object_type" ? 0 : 1);
  return Array.from(groups.values())
    .map((b) => ({ ...b.g, entries: [...b.whole, ...b.self, ...b.members] }))
    .sort(
      (a, b) =>
        subjectRank(a.subject) - subjectRank(b.subject) ||
        a.name.localeCompare(b.name, undefined, { sensitivity: "base" }) ||
        (a.name < b.name ? -1 : a.name > b.name ? 1 : 0),
    );
}

export interface DiffCounts {
  added: number;
  removed: number;
  changed: number;
  breaking: number;
}

export function diffCounts(changes: ReadonlyArray<OntologyChange>): DiffCounts {
  const c: DiffCounts = { added: 0, removed: 0, changed: 0, breaking: 0 };
  for (const change of changes) {
    c[changeClass(change.kind)]++;
    if (change.breaking) c.breaking++;
  }
  return c;
}

/**
 * A version's saved summary, counted the same way as a diff, so the list and
 * the comparison say "2 added" about the same two changes. The summary holds
 * counts per kind, not the changes, and its breaking count is its own.
 */
export function summaryCounts(byKind: Readonly<Record<string, number>> | undefined, breaking = 0): DiffCounts {
  const c: DiffCounts = { added: 0, removed: 0, changed: 0, breaking };
  for (const [kind, n] of Object.entries(byKind ?? {})) c[changeClass(kind)] += n;
  return c;
}
