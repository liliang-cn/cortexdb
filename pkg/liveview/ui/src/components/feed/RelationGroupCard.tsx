import type { EntityRelation, HistoryLine, RelationGroup } from "@/api";
import { Dot } from "@/components/semantic";
import { describeInEffect, isPlainInEffect, pastEntries, statusWords, successorOf, versionFor, versionOther } from "@/lib/entity";
import { reasonWords, schemaLabel } from "@/lib/changes";
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
  if (ms === undefined) return null;
  return (
    <span title={formatClock(ms)}>
      {prefix} {formatRelative(ms, now)}
    </span>
  );
}

function CurrentRelation({ rel, lines, now }: { rel: EntityRelation; lines: ReadonlyArray<HistoryLine>; now: number }) {
  const version = versionFor(lines, rel);
  const ie = describeInEffect(version?.in_effect);
  const showWhy = ie && !isPlainInEffect(version?.in_effect);
  return (
    <li className="py-1.5">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
        <Dot s={grade(rel.grade)} />
        <EntityLink id={rel.other.id} label={rel.other.label} type={rel.other.type} />
        {rel.other.type && <span className="text-xs text-muted-foreground">{rel.other.type}</span>}
        {ie?.several && (
          <span className="rounded-full bg-op-updated-soft px-2 py-px text-xs text-op-updated-ink">one of several in effect</span>
        )}
        {rel.recorded_at && (
          <span className="ml-auto text-xs text-muted-foreground" title={formatClock(rel.recorded_at)}>
            {formatRelative(rel.recorded_at, now)}
          </span>
        )}
      </div>
      {showWhy && ie && (
        <div className="mt-1 ml-4 border-l-2 border-primary/30 pl-2.5 text-xs leading-relaxed text-muted-foreground">
          <p>
            <span className="font-medium text-foreground">{ie.title}.</span> {ie.summary}
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
  if (past.length === 0) return null;
  return (
    <ul className="mt-1 space-y-2 border-t border-dashed pt-2 opacity-70">
      {past.map(({ line, version: v }) => {
        const other = versionOther(line, v);
        const next = successorOf(lines, v);
        const why = reasonWords(v.reason);
        return (
          <li key={v.id} className="text-xs leading-relaxed">
            <div className="flex flex-wrap items-baseline gap-x-2">
              <EntityLink id={other.id} label={other.name} className="text-sm" />
              <span className="rounded-full bg-muted px-2 py-px">{statusWords(v.status)}</span>
              <span className="text-muted-foreground">
                <When iso={v.valid_to || v.retracted_at || v.recorded_at} now={now} prefix={v.valid_to || v.retracted_at ? "ended" : "recorded"} />
              </span>
            </div>
            <p className="text-muted-foreground">
              {next && (
                <>
                  Replaced by <EntityLink id={next.id} label={next.name} className="font-normal" />.{" "}
                </>
              )}
              {why && <>Because {why}. </>}
              {v.producer && <>By <span className="font-mono">{v.producer}</span>. </>}
              {v.schema_id && <>Under schema {schemaLabel({ id: v.schema_id, version: v.schema_version ?? 0 })}.</>}
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
  return (
    <section aria-label={`${group.predicate}, ${direction === "in" ? "incoming" : "outgoing"}`} className={cn("rounded-xl border bg-card p-3")}>
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
      {group.edges.length === 0 && <p className="mt-1 text-xs text-muted-foreground">Nothing in effect now.</p>}
      <PastRelations lines={lines} now={now} />
    </section>
  );
}
