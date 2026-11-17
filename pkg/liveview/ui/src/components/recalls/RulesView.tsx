import { useState, type MouseEvent, type ReactNode } from "react";
import { ChevronDown, ChevronUp } from "lucide-react";
import { Link, useNavigate } from "react-router";
import type { StandingRuleRow, StandingRulesView } from "@/api/recalls";
import { entityPath } from "@/components/feed/EntityLink";
import { Empty } from "@/components/Page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import type { Lang } from "@/lib/i18n";
import { numIn } from "@/lib/ontologyStrings";
import { clockTime } from "@/lib/recalls";
import type { RulesText } from "@/lib/rulesStrings";
import {
  expandable,
  formatImportance,
  RULES_PREFIX,
  rowBadges,
  rowDate,
  rulesHeader,
  rulesProject,
  scopeLabel,
  withRulesProject,
  type BadgeTone,
} from "@/lib/standingRules";
import { useScopedParams } from "@/lib/useScopedParams";
import { useStandingRules } from "@/lib/useRecalls";
import { cn } from "@/lib/utils";
import { useLive } from "@/live";

/** Radix's Select has no empty value; this stands for a session in no project. */
const NO_PROJECT = "-";

const TONE: Record<BadgeTone, string> = {
  injected: "border-transparent bg-op-added-soft text-op-added-ink",
  over: "border-transparent bg-grade-held-soft text-grade-held-ink",
  other: "text-muted-foreground",
  missing: "border-transparent bg-kind-failed-soft text-kind-failed-ink",
};

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 text-lg font-semibold tracking-tight tabular-nums">{children}</dd>
    </div>
  );
}

function Meta({ label, children }: { label: string; children: ReactNode }) {
  return (
    <span className="inline-flex items-baseline gap-1">
      <span>{label}</span>
      <span className="text-foreground tabular-nums">{children}</span>
    </span>
  );
}

function RuleItem({ row, lang, t }: { row: StandingRuleRow; lang: Lang; t: RulesText }) {
  const [open, setOpen] = useState(false);
  const navigate = useNavigate();
  const inGraph = useLive((s) => s.nodes.has(row.node_id));
  const long = expandable(row);
  const date = rowDate(row);
  const saved = row.created_at ? clockTime(lang, Date.parse(row.created_at)) : undefined;

  // The row opens the memory's page when the graph has it; its own controls
  // and links act on their own.
  const onRow = (e: MouseEvent<HTMLLIElement>) => {
    if (!inGraph || (e.target as HTMLElement).closest("a,button")) return;
    navigate(entityPath(row.node_id));
  };

  return (
    <li onClick={onRow} className={cn("px-4 py-3", inGraph && "cursor-pointer hover:bg-accent/40")}>
      <div className="flex flex-wrap items-center gap-1.5">
        {rowBadges(row, t).map((b) => (
          <Badge key={b.label} variant="outline" className={cn("font-normal", TONE[b.tone])}>
            {b.label}
          </Badge>
        ))}
        {row.type && (
          <Badge variant="secondary" className="font-mono text-[0.7rem] font-normal">
            {row.type}
          </Badge>
        )}
      </div>
      <p className={cn("mt-1.5 text-sm whitespace-pre-wrap [overflow-wrap:anywhere]", !open && "line-clamp-2")}>
        {inGraph ? (
          <Link to={entityPath(row.node_id)} className="underline decoration-border underline-offset-4 hover:text-primary hover:decoration-primary">
            {row.content}
          </Link>
        ) : (
          row.content
        )}
      </p>
      <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
        <span className="font-mono [overflow-wrap:anywhere]">{row.id}</span>
        <Meta label={t.importance}>{formatImportance(row.importance)}</Meta>
        <Meta label={t.scope}>{scopeLabel(row, t)}</Meta>
        {date && (
          <span className="inline-flex items-baseline gap-1" title={saved}>
            <span>{t.date}</span>
            <span className="text-foreground tabular-nums">{date}</span>
          </span>
        )}
        <Meta label={t.recalls}>{numIn(lang, row.recall_count)}</Meta>
        {long && (
          <Button variant="ghost" size="xs" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
            {open ? <ChevronUp aria-hidden /> : <ChevronDown aria-hidden />}
            {open ? t.collapse : t.expand}
          </Button>
        )}
      </div>
    </li>
  );
}

function Group({ title, count, children }: { title: string; count: ReactNode; children: ReactNode }) {
  return (
    <section aria-label={title}>
      <h2 className="mb-2.5 flex items-baseline gap-1.5 text-sm font-semibold">
        {title}
        <span className="font-normal text-muted-foreground tabular-nums">{count}</span>
      </h2>
      {children}
    </section>
  );
}

function Rules({ v, lang, t, onProject }: { v: StandingRulesView; lang: Lang; t: RulesText; onProject: (p: string) => void }) {
  const h = rulesHeader(v);
  const over = h.chars > h.maxChars;
  const shownNear = v.near_misses.length;
  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-4 rounded-xl border bg-card px-4 py-3">
        <dl className="grid grid-cols-2 gap-x-8 gap-y-3 sm:grid-cols-4">
          <Field label={t.rules}>{numIn(lang, h.rules)}</Field>
          <Field label={t.injected}>{numIn(lang, h.injected)}</Field>
          <Field label={t.characters}>
            <span className={cn(over && "text-destructive")}>{numIn(lang, h.chars)}</span>
            <span className="text-sm font-normal text-muted-foreground"> / {numIn(lang, h.maxChars)}</span>
          </Field>
          <Field label={t.notQualifying}>{numIn(lang, h.notQualifying)}</Field>
        </dl>
        <div className="flex flex-wrap items-center gap-2">
          {v.partial && (
            <Badge variant="outline" className={TONE.over}>
              {t.partial}
            </Badge>
          )}
          <label className="flex items-center gap-2 text-sm">
            <span className="text-muted-foreground">{t.project}</span>
            <Select value={v.project || NO_PROJECT} onValueChange={(p) => onProject(p === NO_PROJECT ? "" : p)}>
              <SelectTrigger size="sm" className="min-w-36" aria-label={t.project}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NO_PROJECT}>{t.noProject}</SelectItem>
                {v.projects.map((p) => (
                  <SelectItem key={p} value={p}>
                    {p}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </label>
        </div>
      </div>

      <Group title={t.rules} count={numIn(lang, v.rules.length)}>
        {v.rules.length === 0 ? (
          <Empty title={t.none} />
        ) : (
          <ol className="divide-y overflow-hidden rounded-xl border bg-card">
            {v.rules.map((r) => (
              <RuleItem key={r.id} row={r} lang={lang} t={t} />
            ))}
          </ol>
        )}
      </Group>

      {!v.partial && (
        <Group title={t.notQualifying} count={numIn(lang, h.notQualifying)}>
          {shownNear === 0 ? (
            <p className="rounded-xl border border-dashed px-4 py-6 text-center text-sm text-muted-foreground">{t.noneNear}</p>
          ) : (
            <>
              <ol className="divide-y overflow-hidden rounded-xl border bg-card">
                {v.near_misses.map((r) => (
                  <RuleItem key={r.id} row={r} lang={lang} t={t} />
                ))}
              </ol>
              {h.notQualifying > shownNear && (
                <p className="mt-2 text-center text-xs text-muted-foreground tabular-nums">{t.more(numIn(lang, h.notQualifying - shownNear))}</p>
              )}
            </>
          )}
        </Group>
      )}
    </div>
  );
}

function RulesSkeleton({ label }: { label: string }) {
  return (
    <div className="space-y-4" aria-busy aria-label={label}>
      <Skeleton className="h-16 w-full rounded-xl" />
      <div className="divide-y rounded-xl border bg-card">
        {Array.from({ length: 5 }, (_, i) => (
          <div key={i} className="space-y-2 px-4 py-3">
            <Skeleton className="h-4 w-24" />
            <Skeleton className="h-4 w-4/5" />
            <Skeleton className="h-3 w-1/2" />
          </div>
        ))}
      </div>
    </div>
  );
}

/** The Rules tab: every standing rule, which fit the session start's budget and which do not, and the near misses. */
export function RulesView({ lang, t, noSourceTitle }: { lang: Lang; t: RulesText; noSourceTitle: string }) {
  const [scoped, setScoped] = useScopedParams(RULES_PREFIX);
  const project = rulesProject(scoped);
  const state = useStandingRules(project, true);
  const onProject = (p: string) => setScoped((cur) => withRulesProject(cur, p), { replace: true });

  if (state.status === "loading" && !state.view) return <RulesSkeleton label={t.loading} />;
  if (state.status === "error" || !state.view) {
    const err = state.error ?? { message: "", status: 0 };
    if (err.status === 501) return <Empty title={noSourceTitle} />;
    return (
      <Empty title={t.readError}>
        <span className="[overflow-wrap:anywhere]">{err.message}</span>{" "}
        <button type="button" className="text-primary underline underline-offset-4" onClick={state.reload}>
          {t.retry}
        </button>
      </Empty>
    );
  }
  return <Rules v={state.view} lang={lang} t={t} onProject={onProject} />;
}
