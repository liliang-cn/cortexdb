import { describe, expect, it } from "vitest";

// The ontology page's own source, read as text. These guard decisions that are
// written down at the top of lib/ontologyLayout.ts and would be quietly
// reversed by an import, not by a change to any function a test calls.
const sources = import.meta.glob(["../pages/OntologyPage.tsx", "../components/ontology/*.{ts,tsx}", "./ontology*.ts", "./useOntology.ts", "../api/ontology.ts", "!./*.test.ts"], {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

describe("the ontology page's source", () => {
  it("is all here to be read", () => {
    const names = Object.keys(sources);
    expect(names).toContain("../pages/OntologyPage.tsx");
    expect(names).toContain("../components/ontology/Diagram.tsx");
    expect(names).toContain("./ontologyLayout.ts");
  });

  it("is not the graph page's scene again", () => {
    // Tens of named types are not a force layout's problem.
    for (const [name, src] of Object.entries(sources)) {
      for (const wrong of [/from ["']three["']/, /from ["']three\//, /3d-force-graph/, /ForceGraph3D/, /d3-force/, /forceSimulation/]) {
        expect(wrong.test(src), `${name} matches ${wrong}`).toBe(false);
      }
    }
  });

  it("loads nothing off the network and asks for its data relatively", () => {
    for (const [name, src] of Object.entries(sources)) {
      expect(src.includes("https://") || src.includes("http://"), `${name} names a URL`).toBe(false);
      expect(/["'`]\/api\//.test(src), `${name} asks an absolute /api/ path`).toBe(false);
    }
    expect(sources["../api/ontology.ts"]).toContain('getJSON<OntologyAnswer>("api/ontology"');
  });

  it("reads the live store rather than opening a stream of its own", () => {
    for (const [name, src] of Object.entries(sources)) expect(src.includes("EventSource"), name).toBe(false);
  });

  it("links to the versions page through the router", () => {
    expect(sources["../pages/OntologyPage.tsx"]).toMatch(/<Link to=\{versionsPath\(rep\)\}/);
  });
});
