import type { EntityRelation, HistoryLine, RelationGroup } from "@/api";
import { Dot } from "@/components/semantic";
import { describeInEffect, isPlainInEffect, pastEntries, statusWords, successorOf, versionFor, versionOther } from "@/lib/entity";
import { reasonWords, schemaLabel } from "@/lib/changes";
import { FEED_TEXT } from "@/lib/feedStrings";
import { useLang } from "@/lib/i18n";
import { grade } from "@/lib/semantics";
import { formatClock, formatRelative } from "@/lib/time";
import { cn } from "@/lib/utils";
import { EntityLink, Predicate } from "./EntityLink";

function isoMs(iso: string | undefined): number | undefined {
  if (!iso) return undefined;
  const t = Date.parse(iso);
  return Number.isNaN(t) ? undefined : t;
}

function When({ iso, now, prefix }: { iso: string | undefined; now: number; prefix: string }) {
  const ms = isoMs(iso);
  const lang = useLang();
  if (ms === undefined) return null;
  return <span title={formatClock(lang, ms)}>{FEED_TEXT[lang].when(prefix, formatRelative(lang, ms, now))}</span>;
}

function CurrentRelation({ rel, lines, now }: { rel: EntityRelation; lines: ReadonlyArray<HistoryLine>; now: number }) {
  const version = versionFor(lines, rel);
  const lang = useLang();
  const t = FEED_TEXT[lang];
  const ie = describeInEffect(version?.in_effect, lang);
  const showWhy = ie && !isPlainInEffect(version?.in_effect);
  return (
    <li className="py-1.5">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
        <Dot s={grade(rel.grade)} />
        <EntityLink id={rel.other.id} label={rel.other.label} type={rel.other.type} />
        {rel.other.type && <span className="text-xs text-muted-foreground">{rel.other.type}</span>}
        {ie?.several && (
          <span className="rounded-full bg-op-updated-soft px-2 py-px text-xs text-op-updated-ink">{t.oneOfSeveral}</span>
        )}
        {rel.recorded_at && (
          <span className="ml-auto text-xs text-muted-foreground" title={formatClock(lang, rel.recorded_at)}>
            {formatRelative(lang, rel.recorded_at, now)}
          </span>
        )}
      </div>
      {showWhy && ie && (
        <div className="mt-1 ml-4 border-l-2 border-primary/30 pl-2.5 text-xs leading-relaxed text-muted-foreground">
          <p>
            <span className="font-medium text-foreground">{ie.title}{lang === "zh" ? "。" : "."}</span> {ie.summary}
          </p>
          {ie.notes.map((n) => (
            <p key={n}>{n}</p>
          ))}
        </div>
      )}
    </li>
  );
}

function PastRelations({ lines, now }: { lines: ReadonlyArray<HistoryLine>; now: number }) {
  const past = pastEntries(lines);
  const lang = useLang();
  const t = FEED_TEXT[lang];
  if (past.length === 0) return null;
  return (
    <ul className="mt-1 space-y-2 border-t border-dashed pt-2 opacity-70">
      {past.map(({ line, version: v }) => {
        const other = versionOther(line, v);
        const next = successorOf(lines, v);
        const why = reasonWords(v.reason, lang);
        return (
          <li key={v.id} className="text-xs leading-relaxed">
            <div className="flex flex-wrap items-baseline gap-x-2">
              <EntityLink id={other.id} label={other.name} className="text-sm" />
              <span className="rounded-full bg-muted px-2 py-px">{statusWords(v.status, lang)}</span>
              <span className="text-muted-foreground">
                <When iso={v.valid_to || v.retracted_at || v.recorded_at} now={now} prefix={v.valid_to || v.retracted_at ? t.ended : t.recorded} />
              </span>
            </div>
            <p className="text-muted-foreground">
              {next && (
                <>
                  {t.replacedByLead} <EntityLink id={next.id} label={next.name} className="font-normal" />
                  {lang === "zh" ? "。" : ". "}
                </>
              )}
              {why && <>{t.because(why)} </>}
              {v.producer && (
                <>
                  {t.byLead} <span className="font-mono">{v.producer}</span>
                  {lang === "zh" ? "。" : ". "}
                </>
              )}
              {v.schema_id && <>{t.underSchema(schemaLabel({ id: v.schema_id, version: v.schema_version ?? 0 }))}</>}
            </p>
          </li>
        );
      })}
    </ul>
  );
}

/** One predicate on one side of the entity: the values in effect, then what they replaced. */
export function RelationGroupCard({
  group,
  direction,
  lines,
  now,
}: {
  group: RelationGroup;
  direction: "out" | "in";
  lines: ReadonlyArray<HistoryLine>;
  now: number;
}) {
  const t = FEED_TEXT[useLang()];
  return (
    <section aria-label={direction === "in" ? t.incomingOf(group.predicate) : t.outgoingOf(group.predicate)} className={cn("rounded-xl border bg-card p-3")}>
      <header className="flex items-center justify-between gap-2">
        <h3 className="text-sm">
          <Predicate name={group.predicate} />
        </h3>
        <span className="rounded-full bg-muted px-2 py-px text-xs tabular-nums text-muted-foreground">{group.edges.length}</span>
      </header>
      {group.edges.length > 0 && (
        <ul className="mt-1 divide-y divide-border/60">
          {group.edges.map((rel) => (
            <CurrentRelation key={rel.edge_id} rel={rel} lines={lines} now={now} />
          ))}
        </ul>
      )}
      {group.edges.length === 0 && <p className="mt-1 text-xs text-muted-foreground">{t.nothingInEffect}</p>}
      <PastRelations lines={lines} now={now} />
    </section>
  );
}
