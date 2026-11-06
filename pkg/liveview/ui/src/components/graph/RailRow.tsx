// One line of the live rail: what kind of thing it was, what it said, how long ago.
// Driven entirely by the semantics module, so the integrator can fold it into the
// Changes page's row later.

import { memo } from "react";
import type { Semantic } from "@/lib/semantics";
import { formatRelative } from "@/lib/time";
import { cn } from "@/lib/utils";

export interface RailRowProps {
  semantic: Semantic;
  text: string;
  at: number;
  now: number;
  /** The producer of a change or the tool of a call, shown in the tooltip. */
  by?: string;
  failed?: boolean;
  /** Arrived within the last moments: the row fades in its colour. */
  fresh?: boolean;
  onHover: (on: boolean) => void;
  onSelect: () => void;
}

export const RailRow = memo(function RailRow({ semantic: s, text, at, now, by, failed, fresh, onHover, onSelect }: RailRowProps) {
  const Icon = s.icon;
  return (
    <li>
      <button
        type="button"
        onClick={onSelect}
        onMouseEnter={() => onHover(true)}
        onMouseLeave={() => onHover(false)}
        onFocus={() => onHover(true)}
        onBlur={() => onHover(false)}
        title={`${s.label}: ${text}${by ? ` (${by})` : ""}`}
        style={fresh ? ({ "--touched": s.color } as React.CSSProperties) : undefined}
        className={cn(
          "flex w-full items-center gap-2 rounded-md px-1.5 py-1 text-left outline-none transition-colors hover:bg-muted focus-visible:ring-[3px] focus-visible:ring-ring/50",
          fresh && "animate-touched",
        )}
      >
        <span className={cn("flex size-5 shrink-0 items-center justify-center rounded-md", s.chip)}>
          <Icon aria-hidden className="size-3" />
          <span className="sr-only">{s.label}</span>
        </span>
        <span className={cn("min-w-0 flex-1 truncate text-[13px]", failed && "text-muted-foreground line-through")}>{text}</span>
        <time dateTime={new Date(at).toISOString()} className="shrink-0 font-mono text-[11px] tabular-nums text-muted-foreground">
          {formatRelative(at, now)}
        </time>
      </button>
    </li>
  );
});
