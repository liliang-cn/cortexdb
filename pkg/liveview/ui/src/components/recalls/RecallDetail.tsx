import { useState, type ReactNode } from "react";
import { Check, Orbit, ShieldCheck, TriangleAlert, Quote, X } from "lucide-react";
import { Link } from "react-router";
import type { RecallEntry, RecallExplainSide, RecallHit, RecallVerdict } from "@/api/recalls";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import type { Lang } from "@/lib/i18n";
import { clockTime, droppedBeyond, finalMatcher, focusPath, replayPath, retrieverNames, score, verdictFromToggle, verdictOf } from "@/lib/recalls";
import type { RecallsText } from "@/lib/recalls-strings";
import { cn } from "@/lib/utils";
import { useLive } from "@/live";

/** A narrowing the detail can ask the timeline for: a trace, a key or a project. */
export type DetailFilter = { trace: string } | { key: string } | { project: string };

export interface RecallDetailProps {
  entry: RecallEntry;
  lang: Lang;
  t: RecallsText;
  /** Mark one hit; resolves when the server has it. */
  onMark: (hit: RecallHit, verdict: RecallVerdict | "clear") => Promise<void>;
  onFilter: (f: DetailFilter) => void;
}

function Section({ title, children, aside }: { title: string; aside?: ReactNode; children: ReactNode }) {
  return (
    <section className="border-t px-4 py-4 first:border-t-0 sm:px-5">
      <h3 className="mb-2.5 flex items-baseline gap-2 text-xs font-semibold tracking-wide text-muted-foreground uppercase">
        {title}
        {aside}
      </h3>
      {children}
    </section>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5 py-1 sm:flex-row sm:gap-3">
      <dt className="shrink-0 text-muted-foreground sm:w-28">{label}</dt>
      <dd className="min-w-0 flex-1 [overflow-wrap:anywhere]">{children}</dd>
    </div>
  );
}

function Chips({ values }: { values: string[] }) {
  return (
    <span className="flex flex-wrap gap-1">
      {values.map((v) => (
        <Badge key={v} variant="secondary" className="font-normal">
          {v}
        </Badge>
      ))}
    </span>
  );
}

/** A value that narrows the timeline to itself when pressed. */
function FilterLink({ label, title, onClick, mono }: { label: string; title: string; onClick: () => void; mono?: boolean }) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={title}
      className={cn(
        "rounded-sm text-left text-primary underline decoration-primary/30 underline-offset-4 outline-none hover:decoration-primary focus-visible:ring-[3px] focus-visible:ring-ring/50",
        mono && "font-mono text-xs",
      )}
    >
      {label}
    </button>
  );
}

function Plan({ entry: r, lang, t, onFilter }: Omit<RecallDetailProps, "onMark">) {
  const plan = r.explain?.plan ?? { query: r.query };
  const who: Array<[string, string, DetailFilter | null]> = [];
  if (r.key) who.push([t.byKey, r.key, { key: r.key }]);
  if (r.host) who.push([t.host, r.host, null]);
  if (r.project) who.push([t.byProject, r.project, { project: r.project }]);
  if (r.agent) who.push([t.agent, r.agent, null]);
  const planner = plan.planner || r.planner;
  return (
    <dl className="text-sm">
      {plan.query && plan.query !== r.query && <Row label={t.query}>{plan.query}</Row>}
      {(plan.keywords?.length ?? 0) > 0 && <Row label={t.keywords}><Chips values={plan.keywords ?? []} /></Row>}
      {(plan.alternate_queries?.length ?? 0) > 0 && (
        <Row label={t.alternates}>
          <ul className="space-y-0.5">
            {plan.alternate_queries?.map((q) => <li key={q}>{q}</li>)}
          </ul>
        </Row>
      )}
      {(plan.entity_names?.length ?? 0) > 0 && <Row label={t.entities}><Chips values={plan.entity_names ?? []} /></Row>}
      {plan.retrieval_mode && <Row label={t.mode}><span className="font-mono text-xs">{plan.retrieval_mode}</span></Row>}
      {planner && <Row label={t.planner}>{planner}</Row>}
      {who.length > 0 && (
        <Row label={t.asked}>
          <span className="flex flex-wrap items-center gap-x-1.5 gap-y-1">
            {who.map(([label, value, f], i) => (
              <span key={label} className="inline-flex items-center gap-1.5">
                {i > 0 && <span aria-hidden className="text-muted-foreground">·</span>}
                {f ? <FilterLink label={value} title={t.filterBy(`${label} ${value}`)} onClick={() => onFilter(f)} /> : <span>{value}</span>}
              </span>
            ))}
          </span>
        </Row>
      )}
      <Row label={t.when}>
        <span className="tabular-nums">{clockTime(lang, Date.parse(r.at))}</span>
        <span className="text-muted-foreground"> · {t.ms(r.latency_ms)}</span>
      </Row>
      {r.request_id && <Row label={t.requestId}><span className="font-mono text-xs">{r.request_id}</span></Row>}
      {r.trace_id && (
        <Row label={t.traceId}>
          <FilterLink label={r.trace_id} title={t.filterBy(`${t.trace} ${r.trace_id}`)} onClick={() => onFilter({ trace: r.trace_id ?? "" })} mono />
          {r.span_id && <span className="ml-2 font-mono text-xs text-muted-foreground">{t.span} {r.span_id}</span>}
        </Row>
      )}
      {(r.explain?.graph_facts ?? 0) > 0 && <Row label={t.graphFacts}><span className="tabular-nums">{r.explain?.graph_facts}</span></Row>}
    </dl>
  );
}

/** How the record says it is known: a guess is marked, a verified hit shows its check on hover, an untagged one says nothing. */
function GradeMark({ hit, t }: { hit: RecallHit; t: RecallsText }) {
  if (hit.grade === "asserted" || hit.grade === "held") {
    return (
      <Badge variant="outline" className="border-transparent bg-grade-held-soft font-normal text-grade-held-ink" title={hit.grade}>
        <Quote aria-hidden />
        {t.unverified}
      </Badge>
    );
  }
  if (hit.grade === "verified") {
    return (
      <Badge variant="outline" className="border-transparent bg-grade-verified-soft font-normal text-grade-verified-ink" title={hit.grade_why || hit.grade}>
        <ShieldCheck aria-hidden />
        {t.verified}
      </Badge>
    );
  }
  return null;
}

function Hit({ hit: h, t, onMark }: { hit: RecallHit; t: RecallsText; onMark: RecallDetailProps["onMark"] }) {
  const [busy, setBusy] = useState(false);
  const inGraph = useLive((s) => (h.node_id ? s.nodes.has(h.node_id) : false));
  const scores = [`${t.score.toLowerCase()} ${score(h.score)}`];
  if (h.similarity) scores.push(`cos ${score(h.similarity)}`);
  if (h.coverage) scores.push(`cov ${score(h.coverage)}`);
  const origin = [h.date, h.writer ? `key ${h.writer}` : "", h.source, h.project].filter(Boolean).join(" · ");
  const current = verdictOf(h);
  const value = current === "clear" ? "" : current;

  const press = (next: string) => {
    setBusy(true);
    void onMark(h, verdictFromToggle(next)).finally(() => setBusy(false));
  };

  return (
    <li className="grid grid-cols-[minmax(0,1fr)] gap-x-3 gap-y-2 py-3 first:pt-0 last:pb-0 sm:grid-cols-[minmax(0,1fr)_auto]">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-1.5 text-sm leading-6">
          {/* Memories and knowledge are ranked apart, so the rank is said with its kind. */}
          <Badge variant="outline" className="font-normal text-muted-foreground" title={t.rankIn(h.rank, t.kind(h.kind))}>
            {t.kind(h.kind)} <span className="tabular-nums">#{h.rank}</span>
          </Badge>
          <GradeMark hit={h} t={t} />
          <span className="[overflow-wrap:anywhere]">{h.title || h.id}</span>
        </div>
        <div className="mt-1 flex flex-wrap items-center gap-x-2.5 gap-y-1 text-xs text-muted-foreground">
          {(h.via ?? []).map((v) => (
            <Badge key={v} variant="secondary" className="font-mono text-[0.7rem] font-normal">{v}</Badge>
          ))}
          <span className="font-mono tabular-nums">{scores.join(" · ")}</span>
          {origin && <span className="[overflow-wrap:anywhere]">{origin}</span>}
          <span className="font-mono [overflow-wrap:anywhere]">{h.id}</span>
          {h.node_id && inGraph && (
            <Link to={focusPath(h.node_id)} className="inline-flex items-center gap-1 text-primary underline-offset-4 hover:underline">
              <Orbit aria-hidden className="size-3" />
              {t.onGraph}
            </Link>
          )}
        </div>
      </div>
      <ToggleGroup
        type="single"
        variant="outline"
        size="sm"
        value={value}
        onValueChange={press}
        disabled={busy}
        aria-label={t.markHit}
        className="sm:col-start-2 sm:row-start-1 sm:self-start"
      >
        <ToggleGroupItem
          value="useful"
          aria-label={t.useful}
          title={t.useful}
          className="gap-1 data-[state=on]:border-op-added/40 data-[state=on]:bg-op-added-soft data-[state=on]:text-op-added-ink"
        >
          <Check aria-hidden />
          <span className="text-xs sm:sr-only">{t.useful}</span>
        </ToggleGroupItem>
        <ToggleGroupItem
          value="noise"
          aria-label={t.noise}
          title={t.noise}
          className="gap-1 data-[state=on]:border-kind-failed/40 data-[state=on]:bg-kind-failed-soft data-[state=on]:text-kind-failed-ink"
        >
          <X aria-hidden />
          <span className="text-xs sm:sr-only">{t.noise}</span>
        </ToggleGroupItem>
      </ToggleGroup>
    </li>
  );
}

function Side({ name, side, isFinal, t }: { name: string; side: RecallExplainSide; isFinal: (id: string) => boolean; t: RecallsText }) {
  const dropped = side.dropped ?? [];
  const more = droppedBeyond(dropped.length, side.dropped_total);
  const requested = side.requested_mode && side.requested_mode !== side.effective_mode ? side.requested_mode : "";
  return (
    <div className="mt-4 first:mt-0">
      <h4 className="flex flex-wrap items-center gap-2 text-sm font-semibold">
        {name}
        {side.effective_mode && <Badge variant="secondary" className="font-mono text-[0.7rem] font-normal">{side.effective_mode}</Badge>}
        {requested && <span className="text-xs font-normal text-muted-foreground">{t.askedFor(requested)}</span>}
      </h4>
      {side.reason && <p className="mt-1 text-sm text-muted-foreground">{side.reason}</p>}
      {retrieverNames(side.candidates).map((ret) => (
        <table key={ret} className="mt-3 w-full table-fixed border-collapse text-xs">
          <thead>
            <tr className="border-b text-left text-muted-foreground">
              <th className="py-1 pr-2 font-medium">{t.retriever}: <span className="font-mono">{ret}</span></th>
              <th className="w-12 py-1 pr-2 text-right font-medium">{t.rank}</th>
              <th className="w-16 py-1 text-right font-medium">{t.score}</th>
            </tr>
          </thead>
          <tbody>
            {(side.candidates?.[ret] ?? []).map((c) => (
              <tr key={`${c.id}:${c.rank}`} className={cn("border-b border-border/60", isFinal(c.id) && "bg-op-added-soft")}>
                <td className="py-1 pr-2 font-mono [overflow-wrap:anywhere]">{c.id}</td>
                <td className="py-1 pr-2 text-right font-mono tabular-nums">{c.rank}</td>
                <td className="py-1 text-right font-mono tabular-nums">{score(c.score)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ))}
      {dropped.length > 0 && (
        <>
          <p className="mt-4 mb-1 text-xs font-semibold text-muted-foreground">{t.dropped}</p>
          <table className="w-full table-fixed border-collapse text-xs">
            <thead>
              <tr className="border-b text-left text-muted-foreground">
                <th className="w-[38%] py-1 pr-2 font-medium">{t.by}</th>
                <th className="py-1 pr-2 font-medium">{t.id}</th>
                <th className="w-14 py-1 pr-2 text-right font-medium">{t.score}</th>
                <th className="w-16 py-1 text-right font-medium">{t.threshold}</th>
              </tr>
            </thead>
            <tbody>
              {dropped.map((d, i) => (
                <tr key={`${d.by}:${d.id}:${i}`} className="border-b border-border/60">
                  <td className="py-1 pr-2 font-mono [overflow-wrap:anywhere]">{d.by}</td>
                  <td className="py-1 pr-2 font-mono [overflow-wrap:anywhere]">{d.id}</td>
                  <td className="py-1 pr-2 text-right font-mono tabular-nums">{score(d.score)}</td>
                  <td className="py-1 text-right font-mono tabular-nums">{score(d.threshold)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {more > 0 && <p className="mt-1 text-xs text-muted-foreground">{t.droppedMore(more)}</p>}
        </>
      )}
    </div>
  );
}

/** Why one recall returned what it did: the plan, the hits with their marks, and what each retriever put forward. */
export function RecallDetail({ entry: r, lang, t, onMark, onFilter }: RecallDetailProps) {
  const ex = r.explain;
  const isFinal = finalMatcher(r.hits);
  const degraded = r.degraded ?? ex?.degraded ?? [];
  return (
    <article aria-label={r.query}>
      <header className="px-4 pt-4 pb-3 sm:px-5">
        <h2 className="text-base leading-snug font-semibold [overflow-wrap:anywhere]">{r.query}</h2>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <Button asChild variant="outline" size="sm">
            <Link to={replayPath(r.id)}>
              <Orbit aria-hidden />
              {t.replay}
            </Link>
          </Button>
        </div>
      </header>

      {degraded.length > 0 && (
        <div role="note" className="mx-4 mb-3 space-y-1 rounded-lg bg-kind-failed-soft px-3 py-2 text-sm text-kind-failed-ink sm:mx-5">
          {degraded.map((d) => (
            <p key={d} className="flex items-start gap-2">
              <TriangleAlert aria-hidden className="mt-0.5 size-4 shrink-0" />
              <span>
                <span className="font-medium">{t.degraded}: <span className="font-mono text-xs">{d}</span></span>
                {t.degradedWhy[d] && <span className="block text-kind-failed-ink/90">{t.degradedWhy[d]}</span>}
              </span>
            </p>
          ))}
        </div>
      )}

      <Section title={t.plan}>
        <Plan entry={r} lang={lang} t={t} onFilter={onFilter} />
      </Section>

      <Section title={t.hitsHeading} aside={<span className="font-normal tabular-nums normal-case">{r.hits.length}</span>}>
        {r.hits.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t.noHits}</p>
        ) : (
          <ol className="divide-y divide-border/70">
            {r.hits.map((h) => (
              <Hit key={`${h.kind}:${h.id}`} hit={h} t={t} onMark={onMark} />
            ))}
          </ol>
        )}
      </Section>

      <Section title={t.candidates}>
        {ex ? (
          <>
            {ex.memory && <Side name={t.sideMemory} side={ex.memory} isFinal={isFinal} t={t} />}
            {ex.knowledge && <Side name={t.sideKnowledge} side={ex.knowledge} isFinal={isFinal} t={t} />}
          </>
        ) : (
          <p className="text-sm text-muted-foreground">{t.noExplain}</p>
        )}
      </Section>
    </article>
  );
}
