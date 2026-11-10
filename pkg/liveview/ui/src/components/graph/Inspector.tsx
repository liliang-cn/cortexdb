import { useEffect, useMemo, useState } from "react";
import { ArrowDownLeft, ArrowUpRight, Expand, History, Link2, Maximize } from "lucide-react";
import { Link } from "react-router";
import { api, isAbort, type ChangeItem, type Edge } from "@/api";
import { OpChip, GradeChip } from "@/components/semantic";
import { Button } from "@/components/ui/button";
import { CO_OCCURS, predicateColor, typeColor } from "@/lib/graph/colors";
import { edgeKey, predicateShown, type Adjacency, type NodeMap } from "@/lib/graph/model";
import { useCapabilities } from "@/live";
import { GRAPH_TEXT } from "@/lib/graphStrings";
import { useLang, useText } from "@/lib/i18n";
import { useRecentChanges } from "@/lib/useEntityData";
import { formatRelative } from "@/lib/time";
import { useNow } from "@/lib/useNow";
import { cn } from "@/lib/utils";
import { expandNode, selectNode, setHighlight, shareLink, toast } from "./actions";
import { getScene } from "./controller";
import { useDark, useMerged } from "./hooks";
import { Hint, SectionTitle } from "./primitives";
import { IdLink, RecordView, useRecord } from "./RecordView";
import { useGraphUI } from "./state";

interface Group {
  predicate: string;
  rows: Array<{ edge: Edge; key: string; other: string }>;
}

/** Relations of one direction grouped by predicate: biggest first, co_occurs last, because it says least. */
function groupRelations(list: Edge[] | undefined, dir: "out" | "in"): Group[] {
  const m = new Map<string, Group>();
  for (const e of list ?? []) {
    const p = e.label || "related";
    let g = m.get(p);
    if (!g) m.set(p, (g = { predicate: p, rows: [] }));
    g.rows.push({ edge: e, key: edgeKey(e), other: dir === "out" ? e.target : e.source });
  }
  return [...m.values()].sort((a, b) => Number(a.predicate === CO_OCCURS) - Number(b.predicate === CO_OCCURS) || b.rows.length - a.rows.length || (a.predicate < b.predicate ? -1 : 1));
}

function RelationRow({ row, dir, nodes }: { row: Group["rows"][number]; dir: "out" | "in"; nodes: NodeMap }) {
  const set = useGraphUI((s) => s.set);
  const dark = useDark();
  const other = nodes.get(row.other);
  const Arrow = dir === "out" ? ArrowUpRight : ArrowDownLeft;
  return (
    <li>
      <button
        type="button"
        onClick={() => selectNode(row.other)}
        onMouseEnter={() => set({ preview: { nodes: new Set([row.other, dir === "out" ? row.edge.source : row.edge.target]), edges: new Set([row.key]) } })}
        onMouseLeave={() => set({ preview: null })}
        className="flex w-full items-center gap-1.5 rounded-md px-1.5 py-1 text-left text-[13px] outline-none hover:bg-muted focus-visible:ring-[3px] focus-visible:ring-ring/50"
      >
        <Arrow aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="size-2 shrink-0 rounded-full" style={{ background: typeColor(other?.type ?? "", dark) }} />
        <span className="min-w-0 flex-1 truncate">{other?.label ?? row.other}</span>
        {row.edge.grade && <GradeChip value={row.edge.grade} iconOnly />}
      </button>
    </li>
  );
}

function Groups({ groups, dir, nodes }: { groups: Group[]; dir: "out" | "in"; nodes: NodeMap }) {
  const [open, setOpen] = useState<Set<string>>(new Set());
  const filters = useGraphUI((s) => s.filters);
  const t = useText(GRAPH_TEXT);
  if (!groups.length) return null;
  return (
    <div>
      {groups.map((g) => {
        const all = open.has(g.predicate);
        // A relation the key has switched off stays listed, folded to one line.
        const off = !predicateShown(filters, g.predicate);
        const rows = off && !all ? [] : all ? g.rows : g.rows.slice(0, 5);
        const quiet = g.predicate === CO_OCCURS;
        return (
          <div key={g.predicate} className="mb-2">
            <div className="flex items-center gap-2 px-1.5">
              <span className="h-0.5 w-3.5 rounded-full" style={{ background: predicateColor(g.predicate) }} />
              <span className={cn("text-[13px] font-medium", quiet && "text-muted-foreground")}>{g.predicate}</span>
              <span className="font-mono text-[11px] tabular-nums text-muted-foreground">{g.rows.length}</span>
              {quiet && !off && <span className="text-[11px] text-muted-foreground">{t.appearedTogether}</span>}
              {off && (
                <button
                  type="button"
                  className="ml-auto text-xs text-muted-foreground hover:text-foreground"
                  onClick={() => setOpen((prev) => { const n = new Set(prev); if (!n.delete(g.predicate)) n.add(g.predicate); return n; })}
                >
                  {all ? t.fold : t.hiddenList}
                </button>
              )}
            </div>
            <ul>
              {rows.map((r) => <RelationRow key={r.key} row={r} dir={dir} nodes={nodes} />)}
            </ul>
            {!off && g.rows.length > 5 && (
              <button
                type="button"
                className="ml-1.5 px-1.5 text-xs text-muted-foreground hover:text-foreground"
                onClick={() => setOpen((prev) => { const n = new Set(prev); if (!n.delete(g.predicate)) n.add(g.predicate); return n; })}
              >
                {all ? t.showFewer : t.more(g.rows.length - 5)}
              </button>
            )}
          </div>
        );
      })}
    </div>
  );
}

function Relations({ id, adj, nodes }: { id: string; adj: Adjacency; nodes: NodeMap }) {
  const out = useMemo(() => groupRelations(adj.out.get(id), "out"), [adj, id]);
  const inn = useMemo(() => groupRelations(adj.in.get(id), "in"), [adj, id]);
  const t = useText(GRAPH_TEXT);
  if (!out.length && !inn.length) return <Hint>{t.noRelations}</Hint>;
  return (
    <>
      {out.length > 0 && (
        <>
          <p className="mb-1 flex items-center gap-1 px-1.5 text-xs text-muted-foreground"><ArrowUpRight className="size-3" /> {t.outgoing}</p>
          <Groups groups={out} dir="out" nodes={nodes} />
        </>
      )}
      {inn.length > 0 && (
        <>
          <p className={cn("mb-1 flex items-center gap-1 px-1.5 text-xs text-muted-foreground", out.length > 0 && "mt-2")}><ArrowDownLeft className="size-3" /> {t.incoming}</p>
          <Groups groups={inn} dir="in" nodes={nodes} />
        </>
      )}
    </>
  );
}

function Changes({ id }: { id: string }) {
  const caps = useCapabilities();
  // The live ring only holds what arrived since the page opened; a node picked
  // after a reload would otherwise look as if nothing ever happened to it.
  const [fetched, setFetched] = useState<{ id: string; items: ChangeItem[] }>({ id: "", items: [] });
  useEffect(() => {
    if (!caps.changes) return;
    const ctl = new AbortController();
    api
      .changes({ entity: [id], limit: 6 }, { signal: ctl.signal })
      .then((p) => setFetched({ id, items: p.items }))
      .catch((err) => {
        if (!isAbort(err)) setFetched({ id, items: [] });
      });
    return () => ctl.abort();
  }, [id, caps.changes]);
  const items = useRecentChanges(id, fetched.id === id ? fetched.items : [], 6);
  const now = useNow(5000);
  const lang = useLang();
  const t = GRAPH_TEXT[lang];
  if (!items.length) return <Hint>{caps.changes ? t.noEntityChanges : t.noChangeLog}</Hint>;
  return (
    <ul className="space-y-1">
      {items.slice(0, 6).map((c) => (
        <li key={c.seq} className="flex items-center gap-2 text-[13px]">
          <OpChip op={c.op} iconOnly />
          <span className="min-w-0 flex-1 truncate" title={c.summary}>{c.summary}</span>
          <time className="shrink-0 font-mono text-[11px] tabular-nums text-muted-foreground">{formatRelative(lang, c.at, now)}</time>
        </li>
      ))}
    </ul>
  );
}

function Actions({ id }: { id: string }) {
  const caps = useCapabilities();
  const [busy, setBusy] = useState(false);
  const g = useMerged();
  const t = useText(GRAPH_TEXT);
  return (
    <div className="flex flex-wrap gap-1.5">
      <Button asChild variant="secondary" size="sm" className="h-8 gap-1.5">
        <Link to={{ pathname: "/entity", search: `?id=${encodeURIComponent(id)}` }} title={t.historyTitle}>
          <History className="size-3.5" />{t.history}
        </Link>
      </Button>
      {caps.explore && (
        <Button
          variant="secondary"
          size="sm"
          className="h-8 gap-1.5"
          disabled={busy}
          onClick={() => {
            setBusy(true);
            expandNode(id, false)
              .then(() => requestAnimationFrame(() => setHighlight([id, ...(g.adj.out.get(id) ?? []).map((e) => e.target), ...(g.adj.in.get(id) ?? []).map((e) => e.source)], false)))
              .catch((err) => toast(err instanceof Error ? err.message : String(err)))
              .finally(() => setBusy(false));
          }}
          title={t.expandTitle}
        >
          <Expand className="size-3.5" />{busy ? t.expanding : t.expand}
        </Button>
      )}
      <Button variant="ghost" size="sm" className="h-8 gap-1.5" onClick={() => getScene()?.flyTo(id, 700)} title={t.flyTo}>
        <Maximize className="size-3.5" />{t.frame}
      </Button>
      <Button variant="ghost" size="sm" className="h-8 gap-1.5" onClick={() => shareLink([["focus", id]])} title={t.shareTitle}>
        <Link2 className="size-3.5" />{t.share}
      </Button>
    </div>
  );
}

export function NodeTitle({ id }: { id: string }) {
  const g = useMerged();
  const dark = useDark();
  const n = g.nodes.get(id);
  return (
    <div className="flex min-w-0 items-center gap-2">
      <span className="size-2.5 shrink-0 rounded-full" style={{ background: typeColor(n?.type ?? "", dark) }} />
      <span className="min-w-0 truncate" title={n?.label ?? id}>{n?.label ?? id}</span>
    </div>
  );
}

export function NodeInspector({ id }: { id: string }) {
  const g = useMerged();
  const caps = useCapabilities();
  const n = g.nodes.get(id);
  const rec = useRecord(id, caps.records);
  const degree = g.adj.degree.get(id) ?? 0;
  const t = useText(GRAPH_TEXT);
  return (
    <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-3 py-3">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
        <span>{n?.type || t.untyped}</span>
        <span aria-hidden>·</span>
        <span>{t.relations(degree)}</span>
        {n?.grade && <GradeChip value={n.grade} className="ml-1" />}
      </div>
      <p className="mt-1 break-all font-mono text-[11px] text-muted-foreground">{id}</p>
      {!n && <p className="mt-2"><Hint>{t.notDrawn}</Hint></p>}
      <div className="mt-3"><Actions id={id} /></div>

      <SectionTitle>{t.relationsTitle}</SectionTitle>
      <Relations id={id} adj={g.adj} nodes={g.nodes} />

      <SectionTitle>{t.latestChanges}</SectionTitle>
      <Changes id={id} />

      <SectionTitle>{t.record}</SectionTitle>
      <RecordView rep={rec.rep} error={rec.error} loading={rec.loading} available={caps.records} />
    </div>
  );
}

export function EdgeInspector({ edgeKey: key }: { edgeKey: string }) {
  const g = useMerged();
  const caps = useCapabilities();
  const e = g.edges.get(key);
  const rec = useRecord(e?.id ?? null, caps.records && !!e?.id);
  const t = useText(GRAPH_TEXT);
  if (!e) return <div className="px-3 py-3"><Hint>{t.relationGone}</Hint></div>;
  const name = (id: string) => g.nodes.get(id)?.label ?? id;
  return (
    <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-3 py-3">
      <div className="flex flex-wrap items-center gap-1.5 text-[13px]">
        <button type="button" className="font-medium hover:underline" onClick={() => selectNode(e.source)}>{name(e.source)}</button>
        <span className="inline-flex items-center gap-1 rounded-full border px-1.5 text-xs" style={{ borderColor: predicateColor(e.label), color: `color-mix(in oklab, ${predicateColor(e.label)} 55%, var(--foreground))` }}>
          {e.label || "related"} <span aria-hidden>→</span>
        </span>
        <button type="button" className="font-medium hover:underline" onClick={() => selectNode(e.target)}>{name(e.target)}</button>
      </div>
      {e.grade && <div className="mt-2"><GradeChip value={e.grade} /></div>}
      {e.id && <p className="mt-2 break-all font-mono text-[11px] text-muted-foreground">{e.id}</p>}
      <SectionTitle>{t.record}</SectionTitle>
      {e.id ? (
        <RecordView rep={rec.rep} error={rec.error} loading={rec.loading} available={caps.records} />
      ) : (
        <Hint>{t.noRecordId}</Hint>
      )}
      <div className="mt-3 flex gap-1.5">
        <IdLink id={e.source} label={t.openSource} />
        <span className="text-muted-foreground">·</span>
        <IdLink id={e.target} label={t.openTarget} />
      </div>
    </div>
  );
}

