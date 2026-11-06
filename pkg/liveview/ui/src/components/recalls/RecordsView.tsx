import type { ReactNode } from "react";
import { Orbit } from "lucide-react";
import { Link } from "react-router";
import type { RecallStats } from "@/api/recalls";
import { Badge } from "@/components/ui/badge";
import type { Lang } from "@/lib/i18n";
import { clockTime, focusPath, relativeTime, windowDays } from "@/lib/recalls";
import type { RecallsText } from "@/lib/recalls-strings";
import { useLive } from "@/live";

function Tile({ value, label }: { value: ReactNode; label: string }) {
  return (
    <div className="rounded-xl border bg-card px-4 py-3">
      <div className="text-2xl font-semibold tracking-tight tabular-nums">{value}</div>
      <div className="mt-0.5 text-xs text-muted-foreground">{label}</div>
    </div>
  );
}

/** A link to a record's node, offered only when the graph has that node. */
function GraphLink({ nodeId, t }: { nodeId?: string; t: RecallsText }) {
  const known = useLive((s) => (nodeId ? s.nodes.has(nodeId) : false));
  if (!nodeId || !known) return null;
  return (
    <Link to={focusPath(nodeId)} className="inline-flex items-center gap-1 text-primary underline-offset-4 hover:underline">
      <Orbit aria-hidden className="size-3" />
      {t.onGraph}
    </Link>
  );
}

export interface RecordsViewProps {
  stats: RecallStats;
  /** The most recalled list is cut at this many records. */
  limit: number;
  now: number;
  lang: Lang;
  t: RecallsText;
  /** Open the timeline on the recalls that returned this record. */
  onRecord: (id: string) => void;
}

/** The log read per record: totals, what recalls keep reaching, and the memories they never do. */
export function RecordsView({ stats: s, limit, now, lang, t, onRecord }: RecordsViewProps) {
  const most = s.most_recalled ?? [];
  const never = s.never_recalled ?? [];
  const reached = most.length >= limit ? `${most.length}+` : String(most.length);
  return (
    <div className="space-y-6">
      <section aria-label={t.totals(windowDays(s.since, now))}>
        <h2 className="mb-2.5 text-sm font-semibold" title={clockTime(lang, Date.parse(s.since))}>
          {t.totals(windowDays(s.since, now))}
        </h2>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <Tile value={s.recalls.toLocaleString()} label={t.recallsN} />
          <Tile value={s.empty_recalls.toLocaleString()} label={t.emptyN} />
          <Tile value={s.degraded_recalls.toLocaleString()} label={t.degradedN} />
          <Tile value={reached} label={t.reached} />
        </div>
      </section>

      <div className="grid gap-6 lg:grid-cols-2">
        <section aria-label={t.most}>
          <h2 className="mb-2.5 text-sm font-semibold">{t.most}</h2>
          {most.length === 0 ? (
            <p className="rounded-xl border border-dashed px-4 py-6 text-center text-sm text-muted-foreground">{t.mostNone}</p>
          ) : (
            <ol className="divide-y overflow-hidden rounded-xl border bg-card">
              {most.map((m) => (
                <li key={`${m.kind}:${m.id}`} className="flex gap-3 px-4 py-3">
                  <span className="w-10 shrink-0 text-right text-lg font-semibold tabular-nums" title={t.count(m.count)}>
                    {m.count}
                  </span>
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <Badge variant="outline" className="font-normal text-muted-foreground">{t.kind(m.kind)}</Badge>
                      <button
                        type="button"
                        onClick={() => onRecord(m.id)}
                        title={t.showRecalls}
                        className="rounded-sm text-left text-sm font-medium underline decoration-border underline-offset-4 outline-none [overflow-wrap:anywhere] hover:text-primary hover:decoration-primary focus-visible:ring-[3px] focus-visible:ring-ring/50"
                      >
                        {m.title || m.id}
                      </button>
                    </div>
                    {(m.queries?.length ?? 0) > 0 && (
                      <ul className="mt-1 space-y-0.5 text-xs text-muted-foreground">
                        {m.queries?.map((q) => (
                          <li key={q} className="line-clamp-2 [overflow-wrap:anywhere]">“{q}”</li>
                        ))}
                      </ul>
                    )}
                    <div className="mt-1 flex flex-wrap items-center gap-x-2.5 gap-y-1 text-xs text-muted-foreground">
                      <time dateTime={m.last_at} title={clockTime(lang, Date.parse(m.last_at))}>
                        {t.lastRecalled(relativeTime(lang, Date.parse(m.last_at), now))}
                      </time>
                      <span className="font-mono [overflow-wrap:anywhere]">{m.id}</span>
                      <GraphLink nodeId={m.node_id} t={t} />
                    </div>
                  </div>
                </li>
              ))}
            </ol>
          )}
        </section>

        <section aria-label={t.never}>
          <h2 className="mb-2.5 text-sm font-semibold">
            {t.never}
            {never.length > 0 && <span className="ml-1.5 font-normal text-muted-foreground tabular-nums">{never.length}</span>}
          </h2>
          {never.length === 0 ? (
            // The server leaves the list out both when nothing is missing and
            // for a key with a scope, so the page says both.
            <div className="rounded-xl border border-dashed px-4 py-6 text-center">
              <p className="text-sm">{t.noneNever}</p>
              <p className="mx-auto mt-1 max-w-sm text-xs text-muted-foreground">{t.confinedNote}</p>
            </div>
          ) : (
            <ol className="divide-y overflow-hidden rounded-xl border bg-card">
              {never.map((n) => (
                <li key={n.id} className="px-4 py-3">
                  <p className="text-sm [overflow-wrap:anywhere]">{n.title}</p>
                  <div className="mt-1 flex flex-wrap items-center gap-x-2.5 gap-y-1 text-xs text-muted-foreground">
                    <time dateTime={n.created_at} title={clockTime(lang, Date.parse(n.created_at))}>
                      {t.saved(relativeTime(lang, Date.parse(n.created_at), now))}
                    </time>
                    <span className="font-mono [overflow-wrap:anywhere]">{n.id}</span>
                    <GraphLink nodeId={n.node_id} t={t} />
                  </div>
                </li>
              ))}
            </ol>
          )}
        </section>
      </div>
    </div>
  );
}
