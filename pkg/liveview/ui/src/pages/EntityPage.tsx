import { useEffect, useState } from "react";
import { useSearchParams } from "react-router";
import { ApiError, api, isAbort, type EntityView } from "@/api";
import { GradeChip } from "@/components/semantic";
import { Empty, Page, PageHeader } from "@/components/Page";
import { useNode } from "@/live";

// Placeholder for the entity page. The id is `?id=`; the node comes from the
// live store, the relations from /api/entity.
export function EntityPage() {
  const [params] = useSearchParams();
  const id = params.get("id");
  const node = useNode(id);
  const [view, setView] = useState<EntityView | null>(null);
  const [problem, setProblem] = useState<string | null>(null);

  useEffect(() => {
    if (!id) return;
    const ctl = new AbortController();
    setView(null);
    setProblem(null);
    api
      .entity(id, { signal: ctl.signal })
      .then(setView)
      .catch((err: unknown) => {
        if (isAbort(err)) return;
        setProblem(err instanceof ApiError && err.status === 404 ? "This server cannot describe an entity yet." : String(err));
      });
    return () => ctl.abort();
  }, [id]);

  if (!id) {
    return (
      <Page>
        <Empty title="No entity chosen">Open an entity from the graph or from a change to see its relations and history.</Empty>
      </Page>
    );
  }
  const shown = view?.node ?? node;
  return (
    <Page>
      <PageHeader
        title={shown?.label ?? id}
        description={shown ? shown.type : "Not in the graph right now."}
        actions={shown ? <GradeChip value={shown.grade} /> : undefined}
      />
      {problem && <Empty title={problem} />}
      {view && (
        <p className="text-sm text-muted-foreground">
          {view.out.reduce((n, g) => n + g.edges.length, 0)} outgoing and {view.in.reduce((n, g) => n + g.edges.length, 0)} incoming relations.
        </p>
      )}
    </Page>
  );
}
