import { useState, type ReactNode } from "react";
import { ChevronRight } from "lucide-react";
import type { ChangeItem } from "@/api";
import { GradeChip, OpChip } from "@/components/semantic";
import { Badge } from "@/components/ui/badge";
import { reasonView, reasonWords, schemaLabel } from "@/lib/changes";
import { FEED_TEXT } from "@/lib/feedStrings";
import { useLang } from "@/lib/i18n";
import { changeKindLabel, changeOp, semLabel, semMeans } from "@/lib/semantics";
import { formatClock, formatDateTime, formatRelative } from "@/lib/time";
import { cn } from "@/lib/utils";
import { EntityLink, Predicate } from "./EntityLink";

/** The row said as a sentence: ends are links, the predicate sits on the arrow. */
export function ChangeSentence({ item, current }: { item: ChangeItem; current?: string }) {
  const end = (r: { id: string; label: string; type?: string }) =>
    r.id === current ? <span className="font-semibold">{r.label || r.id}</span> : <EntityLink id={r.id} label={r.label} type={r.type} />;

  if (item.edge) {
    const { source, predicate, target } = item.edge;
    return (
      <span className="[overflow-wrap:anywhere]">
        {end(source)} <Predicate name={predicate} /> {end(target)}
      </span>
    );
  }
  if (item.node) {
    return (
      <span className="[overflow-wrap:anywhere]">
        {end(item.node)}
        {item.node.type && <span className="ml-1.5 text-muted-foreground">{item.node.type}</span>}
      </span>
    );
  }
  if (item.triple) {
    const t = item.triple;
    return (
      <span className="[overflow-wrap:anywhere]">
        {t.subject} <Predicate name={t.predicate} /> {t.object}
      </span>
    );
  }
  return <span className="[overflow-wrap:anywhere]">{item.summary}</span>;
}

function Detail({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex gap-3">
      <dt className="w-24 shrink-0 text-muted-foreground">{label}</dt>
      <dd className="min-w-0 flex-1 [overflow-wrap:anywhere]">{children}</dd>
    </div>
  );
}

function ChangeDetails({ item }: { item: ChangeItem }) {
  const lang = useLang();
  const t = FEED_TEXT[lang];
  return (
    <dl className="space-y-1.5 text-xs">
      <Detail label={t.dWhen}>{formatDateTime(lang, item.at)}</Detail>
      <Detail label={t.dRow}>
        <span className="font-mono">{item.id}</span> <span className="text-muted-foreground">{t.rowKind(changeKindLabel(item.kind, lang), item.seq)}</span>
      </Detail>
      {item.reason && (
        <Detail label={t.dReason}>
          <span className="font-mono">{item.reason}</span> <span className="text-muted-foreground">{reasonWords(item.reason, lang)}</span>
        </Detail>
      )}
      {item.schema && <Detail label={t.dSchema}><span className="font-mono">{schemaLabel(item.schema)}</span></Detail>}
      {item.replaces && (
        <Detail label={t.dReplaces}>
          {item.replaces.summary} <span className="font-mono text-muted-foreground">{item.replaces.id}</span>
        </Detail>
      )}
      {item.replaced_by && (
        <Detail label={t.dReplacedBy}>
          {item.replaced_by.summary} <span className="font-mono text-muted-foreground">{item.replaced_by.id}</span>
        </Detail>
      )}
      {item.producer && <Detail label={t.dProducer}><span className="font-mono">{item.producer}</span></Detail>}
      {item.entities.length > 0 && (
        <Detail label={t.dTouched}>
          <span className="flex flex-wrap gap-x-3 gap-y-1">
            {item.entities.map((id) => (
              <EntityLink key={id} id={id} label={id} className="font-mono text-xs font-normal" />
            ))}
          </span>
        </Detail>
      )}
    </dl>
  );
}

export interface ChangeRowProps {
  item: ChangeItem;
  now: number;
  /** One line, no details: for rails and side panels. */
  compact?: boolean;
  /** Flash the row: it just arrived. */
  fresh?: boolean;
  /** The entity whose page this is on; its name is not made a link to itself. */
  current?: string;
  className?: string;
}

/** One change as a readable line, openable for ids, links and the raw reason. */
export function ChangeRow({ item, now, compact, fresh, current, className }: ChangeRowProps) {
  const [open, setOpen] = useState(false);
  const sem = changeOp(item.op);
  const lang = useLang();
  const t = FEED_TEXT[lang];
  const word = semLabel(sem, lang);
  const why = reasonView(item, lang);
  const time = (
    <time
      dateTime={new Date(item.at).toISOString()}
      title={formatClock(lang, item.at)}
      className="shrink-0 text-xs tabular-nums text-muted-foreground"
    >
      {formatRelative(lang, item.at, now)}
    </time>
  );

  if (compact) {
    return (
      <div className={cn("flex items-start gap-2 px-2 py-1.5 text-sm", fresh && "animate-arrive motion-reduce:animate-none", className)}>
        <span className={cn("mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full", sem.chip)} title={`${word}: ${semMeans(sem, lang)}`}>
          <sem.icon aria-hidden className="size-3" />
          <span className="sr-only">{word}</span>
        </span>
        <div className="min-w-0 flex-1 leading-snug">
          <ChangeSentence item={item} current={current} />
          {why && why.text && <span className="ml-1.5 text-xs text-muted-foreground">{why.text}</span>}
        </div>
        {time}
      </div>
    );
  }

  return (
    <div
      className={cn("relative border-l-[3px] px-3 py-2.5 sm:px-4", sem.rail, fresh && "animate-arrive motion-reduce:animate-none", className)}
      data-op={item.op}
    >
      <div
        className="flex cursor-pointer items-start gap-2.5"
        onClick={(e) => {
          if ((e.target as HTMLElement).closest("a,button")) return;
          setOpen((o) => !o);
        }}
      >
        <button
          type="button"
          aria-expanded={open}
          aria-label={open ? t.hideDetails : t.showDetails}
          onClick={() => setOpen((o) => !o)}
          className="mt-0.5 -ml-1 flex size-5 shrink-0 items-center justify-center rounded-sm text-muted-foreground outline-none hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50"
        >
          <ChevronRight aria-hidden className={cn("size-4 transition-transform motion-reduce:transition-none", open && "rotate-90")} />
        </button>
        <OpChip op={item.op} className="mt-px w-[6.25rem] shrink-0 justify-start max-sm:w-auto" />
        <div className="min-w-0 flex-1">
          <div className="text-sm leading-6">
            {item.kind !== "node" && item.kind !== "edge" && (
              <Badge variant="outline" className="mr-2 align-middle font-normal text-muted-foreground">
                {changeKindLabel(item.kind, lang)}
              </Badge>
            )}
            <ChangeSentence item={item} current={current} />
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
            {why && why.text && (
              <Badge variant="outline" className={cn("border-transparent font-normal", sem.chip)}>
                {why.text}
              </Badge>
            )}
            {why && why.schema && (
              <span className="rounded-sm border px-1.5 py-px" title={t.schemaTitle}>
                {t.schemaChip(why.schema)}
              </span>
            )}
            {item.grade && <GradeChip value={item.grade} />}
            {item.producer && <span className="font-mono">{item.producer}</span>}
          </div>
        </div>
        <div className="flex shrink-0 flex-col items-end gap-0.5">
          {time}
          <span className="font-mono text-[0.68rem] tabular-nums text-muted-foreground/70">#{item.seq}</span>
        </div>
      </div>
      {open && (
        <div className="mt-3 ml-7 rounded-lg border bg-muted/40 p-3">
          <ChangeDetails item={item} />
        </div>
      )}
    </div>
  );
}
