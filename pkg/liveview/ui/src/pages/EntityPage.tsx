import { useEffect, useMemo, useState, type ReactNode } from "react";
import { ArrowRight, Check, Copy } from "lucide-react";
import { Link, useSearchParams } from "react-router";
import type { RecordDetail } from "@/api";
import { ChangeRow } from "@/components/feed/ChangeRow";
import { EntityLink, entityPath } from "@/components/feed/EntityLink";
import { RelationGroupCard } from "@/components/feed/RelationGroupCard";
import { Chip, GradeChip } from "@/components/semantic";
import { Empty, Page } from "@/components/Page";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { countEdges, groupsWithHistory, historyByRelation, relationKey, splitCoOccurs } from "@/lib/entity";
import { changeOp, grade } from "@/lib/semantics";
import { useEntityData, useRecentChanges } from "@/lib/useEntityData";
import { formatClock } from "@/lib/time";
import { useNow } from "@/lib/useNow";
import { useNode, useTouch } from "@/live";

function CopyId({ id }: { id: string }) {
  const [done, setDone] = useState(false);
  useEffect(() => {
    if (!done) return;
    const t = setTimeout(() => setDone(false), 1500);
    return () => clearTimeout(t);
  }, [done]);
  return (
    <span className="inline-flex max-w-full items-center gap-1 text-xs text-muted-foreground">
      <span className="truncate font-mono" title={id}>{id}</span>
      <Button
        variant="ghost"
        size="icon-xs"
        aria-label="Copy id"
        onClick={() => {
          navigator.clipboard?.writeText(id).then(() => setDone(true), () => undefined);
        }}
      >
        {done ? <Check aria-hidden /> : <Copy aria-hidden />}
      </Button>
    </span>
  );
}

/** "touched just now": the colour of what happened, pulsing while it is fresh. */
function TouchPulse({ id }: { id: string }) {
  const t = useTouch(id);
  if (!t) return null;
  const s = changeOp(t.op);
  return (
    <span role="status" className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium ${s.chip}`}>
      <span className="relative flex size-2">
        <span className={`absolute inline-flex size-full animate-ping rounded-full opacity-60 motion-reduce:animate-none ${s.dot}`} />
        <span className={`relative inline-flex size-2 rounded-full ${s.dot}`} />
      </span>
      {s.label.toLowerCase()} just now
    </span>
  );
}

function Section({ title, aside, children }: { title: string; aside?: ReactNode; children: ReactNode }) {
  return (
    <section className="mt-8">
      <div className="mb-3 flex items-baseline justify-between gap-3">
        <h2 className="text-sm font-semibold">{title}</h2>
        {aside && <span className="text-xs text-muted-foreground">{aside}</span>}
      </div>
      {children}
    </section>
  );
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex gap-3 py-1.5 text-sm">
      <dt className="w-28 shrink-0 text-muted-foreground">{label}</dt>
      <dd className="min-w-0 flex-1 [overflow-wrap:anywhere]">{children}</dd>
    </div>
  );
}

function Provenance({ rec }: { rec: RecordDetail }) {
  const g = grade(rec.grade);
  const contradicts = rec.contradicts ?? [];
  const decisions = rec.decisions ?? [];
  return (
    <div className="rounded-xl border bg-card px-4 py-2">
      <dl className="divide-y divide-border/60">
        {rec.grade !== undefined || rec.state ? (
          <Fact label="Grade">
            <span className="flex flex-wrap items-center gap-2">
              <Chip s={g} />
              {rec.state && <span className="text-muted-foreground">{rec.state}</span>}
            </span>
            {rec.why && <p className="mt-1 text-muted-foreground">{rec.why}</p>}
          </Fact>
        ) : null}
        {rec.source && <Fact label="Source">{rec.source}</Fact>}
        {rec.document_id && <Fact label="Document"><span className="font-mono text-xs">{rec.document_id}</span></Fact>}
        {(rec.chunk_ids?.length ?? 0) > 0 && (
          <Fact label="Chunks">
            <span className="font-mono text-xs">{rec.chunk_ids?.join(", ")}</span>
          </Fact>
        )}
        {rec.chunk && !rec.chunk_ids?.includes(rec.chunk) && <Fact label="Chunk"><span className="font-mono text-xs">{rec.chunk}</span></Fact>}
        {rec.producer && <Fact label="Producer"><span className="font-mono text-xs">{rec.producer}</span></Fact>}
        {(rec.at || rec.by) && (
          <Fact label="Recorded">
            {rec.at && <span>{rec.at}</span>} {rec.by && <span className="text-muted-foreground">by {rec.by}</span>}
          </Fact>
        )}
        {rec.confidence && <Fact label="Confidence">{rec.confidence}</Fact>}
        {rec.inferred && <Fact label="Inferred">{rec.rule ? `By rule ${rec.rule}.` : "Yes, not stated directly."}</Fact>}
        {contradicts.length > 0 && (
          <Fact label="Contradicts">
            <span className="flex flex-wrap gap-x-3 gap-y-1">
              {contradicts.map((c) => (
                <EntityLink key={c} id={c} label={c} className="font-mono text-xs font-normal" />
              ))}
            </span>
          </Fact>
        )}
        {decisions.length > 0 && (
          <Fact label="Decisions">
            <ul className="space-y-2">
              {decisions.map((d) => (
                <li key={d.id} className="text-sm">
                  <span className="font-medium">{d.verdict ?? d.kind ?? "decision"}</span>
                  {d.actor && <span className="text-muted-foreground"> by {d.actor}</span>}
                  {d.at && <span className="text-muted-foreground"> on {d.at}</span>}
                  {d.grade && <GradeChip value={d.grade} className="ml-2" />}
                  {d.note && <p className="text-muted-foreground">{d.note}</p>}
                  <p className="font-mono text-xs text-muted-foreground">
                    {d.id}
                    {(d.supersedes ?? []).length > 0 && (
                      <>
                        {" "}replaces{" "}
                        {d.supersedes?.map((s) => (
                          <EntityLink key={s} id={s} label={s} className="font-mono text-xs font-normal" />
                        ))}
                      </>
                    )}
                  </p>
                </li>
              ))}
            </ul>
          </Fact>
        )}
      </dl>
      {(rec.notes ?? []).map((n) => (
        <p key={n} className="border-t border-border/60 py-2 text-xs text-muted-foreground">
          {n}
        </p>
      ))}
    </div>
  );
}

function hasProvenance(r: RecordDetail | null): r is RecordDetail {
  if (!r || !r.available || !r.found) return false;
  return !!(r.grade || r.source || r.document_id || r.producer || r.at || r.why || r.inferred || r.contradicts?.length || r.decisions?.length || r.chunk_ids?.length);
}

function Skeletons() {
  return (
    <div className="mt-6 grid gap-4 lg:grid-cols-[1fr_14rem_1fr]" aria-busy aria-label="Loading entity">
      {[0, 1, 2].map((i) => (
        <div key={i} className="space-y-3">
          <Skeleton className="h-24 w-full rounded-xl" />
          <Skeleton className="h-20 w-full rounded-xl" />
        </div>
      ))}
    </div>
  );
}

/** One entity: what it is linked to, what those links replaced, where it came from, what changed lately. */
export function EntityPage() {
  const [params] = useSearchParams();
  const id = params.get("id");
  const now = useNow(5000);
  const liveNode = useNode(id);
  const data = useEntityData(id);
  const recent = useRecentChanges(id, data.changes);

  const node = data.view?.node ?? liveNode ?? null;
  const out = useMemo(() => splitCoOccurs(data.view?.out ?? []), [data.view]);
  const inn = useMemo(() => splitCoOccurs(data.view?.in ?? []), [data.view]);
  const history = useMemo(() => historyByRelation(data.history), [data.history]);
  const outGroups = useMemo(() => groupsWithHistory("out", out.groups, history), [out.groups, history]);
  const inGroups = useMemo(() => groupsWithHistory("in", inn.groups, history), [inn.groups, history]);
  const together = useMemo(() => {
    const m = new Map(out.together.map((r) => [r.other.id, r]));
    for (const r of inn.together) if (!m.has(r.other.id)) m.set(r.other.id, r);
    return Array.from(m.values());
  }, [out.together, inn.together]);

  if (!id) {
    return (
      <Page>
        <Empty title="No entity chosen">Press Ctrl or Cmd K to search for one, or open one from a change in the Changes page.</Empty>
      </Page>
    );
  }

  const inGraph = !!node;
  const title = node?.label ?? id;
  const anyHistory = (data.history?.lines.length ?? 0) > 0 || (data.history?.node_versions?.length ?? 0) > 0;
  const nothingKnown = !data.loading && !inGraph && !anyHistory && recent.length === 0;

  return (
    <Page className="max-w-6xl">
      <header className="flex flex-wrap items-start justify-between gap-x-6 gap-y-2">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <h1 className="text-2xl font-semibold tracking-tight [overflow-wrap:anywhere]">{title}</h1>
            {node?.type && <span className="rounded-full border px-2.5 py-0.5 text-xs text-muted-foreground">{node.type}</span>}
            {inGraph && <GradeChip value={node?.grade} />}
            <TouchPulse id={id} />
          </div>
          <div className="mt-1.5"><CopyId id={id} /></div>
        </div>
        <Button variant="outline" size="sm" asChild>
          <Link to={`/changes?entity=${encodeURIComponent(id)}`}>All changes</Link>
        </Button>
      </header>

      {data.error && <p className="mt-4 rounded-lg border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm">{data.error}</p>}

      {!data.loading && !inGraph && !data.error && (
        <p className="mt-5 rounded-lg border border-dashed px-4 py-3 text-sm text-muted-foreground">
          {nothingKnown
            ? "Nothing is known about this id: it is not in the graph and no change mentions it."
            : "This entity is not in the graph right now. It may have been merged, retracted or deleted; what is known about it is below."}
          {data.unsupported && " This server cannot describe live relations, so only history is shown."}
        </p>
      )}

      {data.loading ? (
        <Skeletons />
      ) : (
        (inGraph || outGroups.length > 0 || inGroups.length > 0) && (
          <div className="mt-6 grid gap-4 lg:grid-cols-[minmax(0,1fr)_13rem_minmax(0,1fr)] lg:gap-6">
            <div className="space-y-3" aria-label="Incoming relations">
              <p className="text-xs font-medium text-muted-foreground">
                Incoming <span className="tabular-nums">({countEdges(inn.groups)})</span>
              </p>
              {inGroups.length === 0 && <p className="rounded-xl border border-dashed px-3 py-4 text-sm text-muted-foreground">Nothing points here.</p>}
              {inGroups.map((g) => (
                <RelationGroupCard key={g.predicate} group={g} direction="in" lines={history.get(relationKey("in", g.predicate)) ?? []} now={now} />
              ))}
            </div>

            <div className="order-first lg:order-none">
              <div className="rounded-xl border-2 border-primary/40 bg-accent/60 px-3 py-4 text-center lg:sticky lg:top-6">
                <p className="text-base font-semibold [overflow-wrap:anywhere]">{title}</p>
                {node?.type && <p className="text-xs text-muted-foreground">{node.type}</p>}
                <p className="mt-2 flex items-center justify-center gap-2 text-xs text-muted-foreground tabular-nums">
                  <span>{countEdges(inn.groups)} in</span>
                  <ArrowRight aria-hidden className="size-3" />
                  <span>{countEdges(out.groups)} out</span>
                </p>
              </div>
            </div>

            <div className="space-y-3" aria-label="Outgoing relations">
              <p className="text-xs font-medium text-muted-foreground">
                Outgoing <span className="tabular-nums">({countEdges(out.groups)})</span>
              </p>
              {outGroups.length === 0 && <p className="rounded-xl border border-dashed px-3 py-4 text-sm text-muted-foreground">Nothing leaves from here.</p>}
              {outGroups.map((g) => (
                <RelationGroupCard key={g.predicate} group={g} direction="out" lines={history.get(relationKey("out", g.predicate)) ?? []} now={now} />
              ))}
            </div>
          </div>
        )
      )}

      {together.length > 0 && (
        <Section title={`Mentioned together with ${together.length}`} aside="Seen in the same passage; not a stated relation.">
          <div className="flex flex-wrap gap-2">
            {together.map((r) => (
              <Link
                key={r.other.id}
                to={entityPath(r.other.id)}
                title={r.other.type}
                className="rounded-full border bg-card px-3 py-1 text-sm outline-none hover:border-primary hover:text-primary focus-visible:ring-[3px] focus-visible:ring-ring/50"
              >
                {r.other.label || r.other.id}
              </Link>
            ))}
          </div>
        </Section>
      )}

      {(data.history?.merged_from?.length ?? 0) > 0 && (
        <Section title="Merged from">
          <ul className="space-y-1 text-sm">
            {data.history?.merged_from?.map((m) => (
              <li key={m.id}>
                <EntityLink id={m.id} label={m.name || m.id} />
                {m.at && <span className="text-muted-foreground"> on {formatClock(Date.parse(m.at))}</span>}
              </li>
            ))}
          </ul>
        </Section>
      )}

      {hasProvenance(data.record) && (
        <Section title="Where it came from" aside={data.record.type}>
          <Provenance rec={data.record} />
        </Section>
      )}

      <Section title="Recent changes" aside={recent.length > 0 ? `${recent.length} shown` : undefined}>
        {recent.length === 0 ? (
          <Empty title="No changes recorded for this entity">{data.loading ? "Reading the change feed." : "Changes that touch it will show up here as they happen."}</Empty>
        ) : (
          <ol className="divide-y overflow-hidden rounded-xl border bg-card">
            {recent.map((c) => (
              <li key={c.seq}>
                <ChangeRow item={c} now={now} compact current={id} />
              </li>
            ))}
          </ol>
        )}
      </Section>
    </Page>
  );
}
