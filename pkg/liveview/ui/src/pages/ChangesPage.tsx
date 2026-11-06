import { useEffect, useMemo, useState } from "react";
import { ApiError, api, isAbort, type ChangeItem } from "@/api";
import { OpChip } from "@/components/semantic";
import { Empty, Page, PageHeader } from "@/components/Page";
import { useCapabilities, useChanges, mergeChangeItems } from "@/live";
import { changeKindLabel } from "@/lib/semantics";
import { formatRelative } from "@/lib/time";
import { useNow } from "@/lib/useNow";

// Placeholder for the change timeline: the live ring, backfilled with the first
// page of /api/changes. A server without that route answers 404 and the page
// shows the live ring alone.
export function ChangesPage() {
  const live = useChanges();
  const caps = useCapabilities();
  const [page, setPage] = useState<ChangeItem[]>([]);
  const [problem, setProblem] = useState<string | null>(null);
  const now = useNow();

  useEffect(() => {
    const ctl = new AbortController();
    api
      .changes({ limit: 50 }, { signal: ctl.signal })
      .then((p) => setPage(p.items))
      .catch((err: unknown) => {
        if (isAbort(err)) return;
        setProblem(err instanceof ApiError && err.status === 404 ? "This server has no change feed." : String(err));
      });
    return () => ctl.abort();
  }, []);

  const items = useMemo(() => mergeChangeItems(live, page, 200), [live, page]);

  return (
    <Page>
      <PageHeader title="Changes" description="Every fact added, replaced or retracted, newest first." />
      {items.length === 0 ? (
        <Empty title={problem ?? (caps.changes ? "No changes yet" : "No change feed from this source")}>
          {caps.changes ? "New writes appear here as they happen." : "Graph structure is still live; writes will not be listed."}
        </Empty>
      ) : (
        <ol className="divide-y rounded-xl border bg-card">
          {items.map((c) => (
            <li key={c.seq} className="flex items-center gap-3 px-4 py-2.5 text-sm">
              <OpChip op={c.op} className="w-28 shrink-0 justify-start" />
              <span className="min-w-0 flex-1 truncate">{c.summary}</span>
              <span className="hidden shrink-0 text-xs text-muted-foreground sm:inline">{changeKindLabel(c.kind)}</span>
              <time className="shrink-0 text-xs text-muted-foreground" dateTime={new Date(c.at).toISOString()}>
                {formatRelative(c.at, now)}
              </time>
            </li>
          ))}
        </ol>
      )}
    </Page>
  );
}
