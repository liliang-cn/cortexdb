import { describe, expect, it } from "vitest";
import type { OntologyChange } from "@/api/ontologyHistory";
import { changeClass, changePart, changeSubject, diffCounts, groupDiff, splitTarget, summaryCounts } from "./ontologyHistoryDiff";

const ch = (kind: string, target: string, breaking = false): OntologyChange => ({ kind, target, detail: `${kind} on ${target}`, breaking });

describe("the word every change is said with", () => {
  it("knows each kind the server reports", () => {
    expect(changeClass("object_type_added")).toBe("added");
    expect(changeClass("required_property_added")).toBe("added");
    expect(changeClass("property_removed")).toBe("removed");
    expect(changeClass("link_type_removed")).toBe("removed");
    expect(changeClass("property_became_required")).toBe("changed");
    expect(changeClass("primary_key_changed")).toBe("changed");
    expect(changeClass("cardinality_relaxed")).toBe("changed");
    expect(changeClass("link_side_retargeted")).toBe("changed");
  });

  it("still says something about a kind from a newer server", () => {
    expect(changeClass("interface_added")).toBe("added");
    expect(changeClass("action_type_removed")).toBe("removed");
    expect(changeClass("something_renamed")).toBe("changed");
    expect(changePart("interface_added")).toBe("interface");
  });

  it("tells object-type changes from link-type changes", () => {
    expect(changeSubject("property_added")).toBe("object_type");
    expect(changeSubject("primary_key_changed")).toBe("object_type");
    expect(changeSubject("cardinality_tightened")).toBe("link_type");
    expect(changeSubject("link_side_retargeted")).toBe("link_type");
    expect(changeSubject("link_future_kind")).toBe("link_type");
  });

  it("splits a target into its type and member", () => {
    expect(splitTarget("Person.email")).toEqual({ name: "Person", member: "email" });
    expect(splitTarget("lives_in.a")).toEqual({ name: "lives_in", member: "a" });
    expect(splitTarget("City")).toEqual({ name: "City", member: "" });
  });
});

describe("grouping a diff by type", () => {
  // The server's order: by target, then kind.
  const changes = [
    ch("primary_key_changed", "City", true),
    ch("required_property_added", "City.slug", true),
    ch("object_type_removed", "Company", true),
    ch("property_added", "Person.email"),
    ch("property_removed", "Person.nickname", true),
    ch("link_type_added", "knows"),
    ch("cardinality_relaxed", "lives_in.a"),
    ch("link_side_retargeted", "lives_in.b", true),
  ];

  it("puts object types before link types, each in name order", () => {
    expect(groupDiff(changes).map((g) => `${g.subject}:${g.name}`)).toEqual([
      "object_type:City",
      "object_type:Company",
      "object_type:Person",
      "link_type:knows",
      "link_type:lives_in",
    ]);
  });

  it("marks a type that was added or removed as a whole", () => {
    const groups = groupDiff(changes);
    expect(groups.find((g) => g.name === "Company")?.whole).toBe("removed");
    expect(groups.find((g) => g.name === "knows")?.whole).toBe("added");
    expect(groups.find((g) => g.name === "City")?.whole).toBeNull();
  });

  it("reads a change to the type itself before its members, and counts what breaks", () => {
    const city = groupDiff(changes).find((g) => g.name === "City");
    expect(city?.entries.map((e) => e.change.kind)).toEqual(["primary_key_changed", "required_property_added"]);
    expect(city?.entries.map((e) => e.member)).toEqual(["", "slug"]);
    expect(city?.breaking).toBe(2);
    const lives = groupDiff(changes).find((g) => g.name === "lives_in");
    expect(lives?.entries.map((e) => `${e.part} ${e.member}`)).toEqual(["side a", "side b"]);
  });

  it("puts a whole-type removal first even when the server listed a member first", () => {
    const g = groupDiff([ch("property_removed", "Thing.x", true), ch("object_type_removed", "Thing", true)])[0];
    expect(g?.entries.map((e) => e.change.kind)).toEqual(["object_type_removed", "property_removed"]);
    expect(g?.whole).toBe("removed");
    expect(g?.entries.map((e) => e.whole)).toEqual([true, false]);
  });

  it("keeps an object type and a link type of the same name apart", () => {
    const groups = groupDiff([ch("object_type_added", "owns"), ch("link_type_added", "owns")]);
    expect(groups.map((g) => g.subject)).toEqual(["object_type", "link_type"]);
  });

  it("returns nothing for no changes", () => {
    expect(groupDiff([])).toEqual([]);
  });
});

describe("counting", () => {
  it("counts a diff by class, and breaking on its own", () => {
    expect(
      diffCounts([ch("property_added", "A.x"), ch("required_property_added", "A.y", true), ch("property_removed", "A.z", true), ch("cardinality_relaxed", "l.a")]),
    ).toEqual({ added: 2, removed: 1, changed: 1, breaking: 2 });
  });

  it("counts a saved summary the same way as a diff", () => {
    expect(summaryCounts({ property_added: 2, object_type_removed: 1, property_became_required: 3 }, 4)).toEqual({
      added: 2,
      removed: 1,
      changed: 3,
      breaking: 4,
    });
    expect(summaryCounts(undefined)).toEqual({ added: 0, removed: 0, changed: 0, breaking: 0 });
  });
});
