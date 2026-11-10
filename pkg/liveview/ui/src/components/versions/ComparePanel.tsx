import { ArrowRight, GitCompareArrows, X } from "lucide-react";
import type { OntologyDiff, OntologyVersionInfo } from "@/api/ontologyHistory";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import type { Pair } from "@/lib/ontologyHistory";
import { diffCounts, groupDiff, type DiffGroup } from "@/lib/ontologyHistoryDiff";
import type { DiffState } from "@/lib/ontologyHistoryHooks";
import { useLang } from "@/lib/i18n";
import { partIn, VERSIONS_TEXT } from "@/lib/versionsStrings";
import { cn } from "@/lib/utils";
import { BreakingChip, ClassChip, CountChips, DIFF_TONES } from "./DiffChips";
import { fullClock } from "./VersionRow";

function Side({ label, version, info }: { label: string; version: number; info: OntologyVersionInfo | undefined }) {
  const lang = useLang();
  return (
    <div className="min-w-0 rounded-lg border bg-background/60 px-3 py-2">
      <p className="text-[0.7rem] font-medium tracking-wide text-muted-foreground uppercase">{label}</p>
      <p className="font-semibold tabular-nums">v{version}</p>
      {info && (
        <p className="text-xs text-muted-foreground [overflow-wrap:anywhere]">
          {fullClock(lang, info.saved_at)}
          {info.saved_by && (
            <>
              <br />
              {VERSIONS_TEXT[lang].by(info.saved_by)}
            </>
          )}
        </p>
      )}
    </div>
  );
}

function Group({ g }: { g: DiffGroup }) {
  const lang = useLang();
  const t = VERSIONS_TEXT[lang];
  const subject = g.subject === "object_type" ? t.objectType : t.linkType;
  return (
    <section className="overflow-hidden rounded-lg border bg-card" aria-label={`${g.name}, ${subject}`}>
      <header className="flex flex-wrap items-center gap-x-2 gap-y-1 border-b bg-muted/40 px-3 py-2">
        <span className="font-mono text-sm font-medium [overflow-wrap:anywhere]">{g.name}</span>
        <span className="text-xs text-muted-foreground">{subject}</span>
        {g.whole && <ClassChip cls={g.whole} className="ml-auto" />}
      </header>
      <ul className="divide-y">
        {g.entries.map((e) => (
          <li key={`${e.change.kind}:${e.change.target}`} title={e.change.kind} className="relative py-2 pr-3 pl-4">
            {/* A rail of its own rather than a left border: a border colour
                would also colour the dividers between the entries. */}
            <span aria-hidden className={cn("absolute inset-y-0 left-0 w-[3px]", DIFF_TONES[e.cls].dot)} />
            {e.whole ? (
              // The heading already says the type was added or removed; the entry adds why it matters.
              <p className="text-sm text-muted-foreground [overflow-wrap:anywhere]">
                {e.change.detail}
                {e.change.breaking && <BreakingChip className="ml-1.5 align-middle" />}
              </p>
            ) : (
              <>
                <div className="flex flex-wrap items-center gap-1.5">
                  <ClassChip cls={e.cls} />
                  <span className="text-sm">
                    {partIn(lang, e.part)}
                    {e.member && <> <code className="font-mono text-[0.8rem] font-medium [overflow-wrap:anywhere]">{e.member}</code></>}
                  </span>
                  {e.change.breaking && <BreakingChip />}
                </div>
                <p className="mt-1 text-sm text-muted-foreground [overflow-wrap:anywhere]">{e.change.detail}</p>
              </>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

function DiffBody({ diff, pair }: { diff: OntologyDiff; pair: Pair }) {
  const groups = groupDiff(diff.changes);
  const counts = diffCounts(diff.changes);
  const n = diff.changes.length;
  const t = VERSIONS_TEXT[useLang()];
  return (
    <div className="space-y-3">
      <div className="space-y-2">
        <p className="text-sm">
          {n === 0 ? (
            <>{t.noDifference}</>
          ) : (
            <>
              <span className="font-medium">{t.changes(n)}</span>
              {t.toTypes(groups.length)}
            </>
          )}
        </p>
        {n > 0 && <CountChips counts={counts} />}
        {n > 0 && (
          <p className={cn("text-sm", diff.has_breaking_changes ? "text-destructive" : "text-muted-foreground")}>
            {diff.has_breaking_changes ? t.breakingSentence(pair.from, pair.to) : t.safeSentence(pair.from, pair.to)}
          </p>
        )}
      </div>
      {groups.map((g) => (
        <Group key={`${g.subject}:${g.name}`} g={g} />
      ))}
      <p className="text-xs text-muted-foreground">
        {t.notCompared}
      </p>
    </div>
  );
}

function DiffSkeleton() {
  const t = VERSIONS_TEXT[useLang()];
  return (
    <div className="space-y-3" aria-busy aria-label={t.comparing}>
      <Skeleton className="h-4 w-2/5" />
      <Skeleton className="h-5 w-3/5 rounded-full" />
      {[0, 1].map((i) => (
        <div key={i} className="space-y-2 rounded-lg border p-3">
          <Skeleton className="h-4 w-1/3" />
          <Skeleton className="h-3 w-4/5" />
          <Skeleton className="h-3 w-3/5" />
        </div>
      ))}
    </div>
  );
}

export interface ComparePanelProps {
  picks: readonly number[];
  pair: Pair | null;
  diff: DiffState;
  /** The newest version, to offer as the other side of a lone pick. */
  latest: number;
  infoOf: (version: number) => OntologyVersionInfo | undefined;
  onPair: (pair: Pair) => void;
  onClear: () => void;
  /** The sheet has its own close button and title; the side panel shows its own. */
  inSheet?: boolean;
}

/** Two picked versions and what changed between them, or how to pick them. */
export function ComparePanel({ picks, pair, diff, latest, infoOf, onPair, onClear, inSheet }: ComparePanelProps) {
  const t = VERSIONS_TEXT[useLang()];
  if (!pair) {
    const one = picks[0];
    return (
      <div className="space-y-3 p-4">
        {!inSheet && <h2 className="text-sm font-semibold">{t.compare}</h2>}
        {one === undefined ? (
          <div className="rounded-lg border border-dashed px-4 py-6 text-center">
            <GitCompareArrows aria-hidden className="mx-auto mb-2 size-5 text-muted-foreground" />
            <p className="text-sm font-medium">{t.pickTwo}</p>
            <p className="mx-auto mt-1 max-w-xs text-sm text-muted-foreground">
              {t.pickTwoBody}
            </p>
          </div>
        ) : (
          <div className="space-y-3">
            <p className="text-sm">
              <span className="font-semibold tabular-nums">v{one}</span>
              {t.pickedPickMore}
            </p>
            <div className="flex flex-wrap gap-2">
              {one > 1 && (
                <Button variant="outline" size="sm" onClick={() => onPair({ from: one - 1, to: one })}>
                  {t.withBefore(one - 1)}
                </Button>
              )}
              {latest > one && (
                <Button variant="outline" size="sm" onClick={() => onPair({ from: one, to: latest })}>
                  {t.withLatest(latest)}
                </Button>
              )}
              <Button variant="ghost" size="sm" onClick={onClear}>
                {t.clear}
              </Button>
            </div>
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="space-y-4 p-4">
      {/* In the sheet the title names the pair, and the bar under the list clears it. */}
      {!inSheet && (
        <div className="flex items-center gap-2">
          <h2 className="flex items-center gap-1.5 text-sm font-semibold tabular-nums">
            v{pair.from} <ArrowRight aria-hidden className="size-3.5" />
            <span className="sr-only">{t.to}</span> v{pair.to}
          </h2>
          <Button variant="ghost" size="sm" className="ml-auto" onClick={onClear}>
            <X aria-hidden />
            {t.clear}
          </Button>
        </div>
      )}
      <div className="grid grid-cols-2 gap-2">
        <Side label={t.from} version={pair.from} info={infoOf(pair.from)} />
        <Side label={t.roles.to} version={pair.to} info={infoOf(pair.to)} />
      </div>
      {diff.status === "loading" || diff.status === "idle" ? (
        <DiffSkeleton />
      ) : diff.status === "error" ? (
        <div className="rounded-lg border border-dashed px-4 py-6 text-center text-sm">
          <p className="font-medium">{t.compareFailed}</p>
          <p className="mt-1 text-muted-foreground [overflow-wrap:anywhere]">
            {diff.error}{" "}
            <button type="button" className="text-primary underline underline-offset-4" onClick={diff.retry}>
              {t.tryAgain}
            </button>
          </p>
        </div>
      ) : diff.diff ? (
        <DiffBody diff={diff.diff} pair={pair} />
      ) : null}
    </div>
  );
}

/** The comparison in one line, for the bar a phone shows under the list. */
export function CompareSummary({ pair, diff }: { pair: Pair; diff: DiffState }) {
  const t = VERSIONS_TEXT[useLang()];
  return (
    <span className="flex min-w-0 flex-col">
      <span className="flex items-center gap-1 text-sm font-semibold tabular-nums">
        v{pair.from} <ArrowRight aria-hidden className="size-3.5" />
        <span className="sr-only">{t.to}</span> v{pair.to}
      </span>
      <span className="truncate text-xs text-muted-foreground">
        {diff.status === "ready" && diff.diff
          ? diff.diff.changes.length === 0
            ? t.noTypeDifference
            : `${t.changes(diff.diff.changes.length)}${
                diff.diff.has_breaking_changes ? t.andBreaking(diffCounts(diff.diff.changes).breaking) : ""
              }`
          : diff.status === "error"
            ? t.couldNotCompare
            : t.comparing}
      </span>
    </span>
  );
}
