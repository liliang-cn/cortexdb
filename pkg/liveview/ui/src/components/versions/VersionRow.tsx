import { memo } from "react";
import { GitCompareArrows, ShieldCheck } from "lucide-react";
import type { OntologyVersionInfo } from "@/api/ontologyHistory";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Toggle } from "@/components/ui/toggle";
import { activationView, sameMinute, summaryView, type PickRole } from "@/lib/ontologyHistory";
import { formatRelative } from "@/lib/time";
import { cn } from "@/lib/utils";
import { CountChips } from "./DiffChips";

// ---- clocks -----------------------------------------------------------------
// Local and to the minute, like the rest of the app; with seconds only where
// both ends of an interval fall in one minute and would otherwise print alike.

function clock(ms: number, seconds = false): string {
  return new Date(ms).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: seconds ? "2-digit" : undefined,
  });
}

function timeOfDay(ms: number, seconds = false): string {
  return new Date(ms).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: seconds ? "2-digit" : undefined });
}

const sameDay = (a: number, b: number) => new Date(a).toDateString() === new Date(b).toDateString();

export function fullClock(iso: string): string {
  const ms = Date.parse(iso);
  return Number.isFinite(ms) ? new Date(ms).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "medium" }) : iso;
}

// ---- pieces -------------------------------------------------------------------

const ROLE_WORDS: Record<PickRole, string> = { from: "From", to: "To", picked: "Picked" };

/** What the version changed against the one before it, as the list says it. */
export function SummaryLine({ v }: { v: OntologyVersionInfo }) {
  const s = summaryView(v.summary);
  switch (s.kind) {
    case "initial":
      return <Badge variant="secondary">First version</Badge>;
    case "unchanged":
      return <span className="text-xs text-muted-foreground">Saved again, definition unchanged</span>;
    case "uncounted":
      return <span className="text-xs text-muted-foreground">Changed actions, interfaces or object sets only</span>;
    case "unrecorded":
      return <span className="text-xs text-muted-foreground">No change summary: saved before the history kept them</span>;
    case "changes":
      return <CountChips counts={s.counts} />;
  }
}

function Activations({ v }: { v: OntologyVersionInfo }) {
  if (v.activations.length === 0) {
    return <p className="mt-1.5 text-xs text-muted-foreground">Never the active validator</p>;
  }
  return (
    <ul className="mt-1.5 space-y-1 text-xs">
      {v.activations.map((a) => {
        const view = activationView(a);
        const open = view.to === null;
        const tight = view.to !== null && sameMinute(view.from, view.to);
        const end =
          view.to === null ? "now" : sameDay(view.from, view.to) ? timeOfDay(view.to, tight) : clock(view.to, tight);
        return (
          <li key={a.active_from} className="flex items-start gap-1.5">
            <ShieldCheck aria-hidden className={cn("mt-px size-3.5 shrink-0", open ? "text-op-added" : "text-muted-foreground")} />
            <span className="min-w-0 [overflow-wrap:anywhere]">
              <span className={open ? "font-medium text-op-added-ink" : "text-foreground"}>
                {open ? "Validating since " : "Validated "}
                <time dateTime={a.active_from} title={fullClock(a.active_from)}>{clock(view.from, tight)}</time>
                {!open && (
                  <>
                    {" to "}
                    <time dateTime={a.active_to} title={a.active_to ? fullClock(a.active_to) : undefined}>{end}</time>
                  </>
                )}
              </span>
              {view.activatedBy && <span className="text-muted-foreground">, switched on by {view.activatedBy}</span>}
              {view.ended && (
                <span className="text-muted-foreground">
                  . Ended: {view.ended}
                  {view.endedBy ? `, by ${view.endedBy}` : ""}
                </span>
              )}
            </span>
          </li>
        );
      })}
    </ul>
  );
}

export interface VersionRowProps {
  v: OntologyVersionInfo;
  now: number;
  /** The schema's current version, or 0 for a deleted schema. */
  latest: number;
  role: PickRole | null;
  /** The name this version gave the schema, when it changed it. */
  renamed: string;
  /** Saved while the page was open: flash once. */
  fresh: boolean;
  onToggle: (version: number) => void;
  onPrevious: (version: number) => void;
}

/** One saved version: when and by whom, what it changed, when it validated, and the controls to compare it. */
export const VersionRow = memo(function VersionRow({ v, now, latest, role, renamed, fresh, onToggle, onPrevious }: VersionRowProps) {
  const saved = Date.parse(v.saved_at);
  return (
    <div
      className={cn(
        "flex gap-3 px-4 py-3 transition-colors",
        role && "bg-accent/45",
        fresh && "animate-arrive motion-reduce:animate-none",
      )}
    >
      <Toggle
        variant="outline"
        size="sm"
        pressed={role !== null}
        onPressedChange={() => onToggle(v.version)}
        aria-label={`Pick v${v.version} to compare`}
        className="h-7 w-16 shrink-0 text-xs data-[state=on]:border-primary data-[state=on]:bg-primary data-[state=on]:text-primary-foreground"
      >
        {role ? ROLE_WORDS[role] : "Pick"}
      </Toggle>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="font-semibold tabular-nums">v{v.version}</span>
          {v.version === latest && <Badge variant="outline">Latest</Badge>}
          {v.active && (
            <Badge variant="outline" className="border-transparent bg-op-added-soft text-op-added-ink">
              <ShieldCheck aria-hidden />
              Active validator
            </Badge>
          )}
          <time
            dateTime={v.saved_at}
            title={fullClock(v.saved_at)}
            className="ml-auto shrink-0 text-xs tabular-nums text-muted-foreground"
          >
            {Number.isFinite(saved) ? formatRelative(saved, now) : ""}
          </time>
        </div>
        <p className="mt-0.5 text-xs text-muted-foreground [overflow-wrap:anywhere]">
          {v.saved_by ? <>Saved by <span className="text-foreground">{v.saved_by}</span></> : "Writer not recorded"}
          {renamed ? <> · renamed to <span className="text-foreground">{renamed}</span></> : null}
          {v.strict_actions ? <> · strict actions</> : null}
        </p>
        <div className="mt-1.5">
          <SummaryLine v={v} />
        </div>
        <Activations v={v} />
        {v.version > 1 && (
          <Button variant="link" size="xs" className="mt-1 h-auto px-0 py-0.5" onClick={() => onPrevious(v.version)}>
            <GitCompareArrows aria-hidden />
            Diff with v{v.version - 1}
          </Button>
        )}
      </div>
    </div>
  );
});
