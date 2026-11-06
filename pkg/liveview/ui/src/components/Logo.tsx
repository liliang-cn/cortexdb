import { cn } from "@/lib/utils";

/** Three linked nodes: the smallest thing that is still a graph. */
export function LogoMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" aria-hidden className={cn("size-6", className)}>
      <rect width="32" height="32" rx="8" className="fill-primary" />
      <path d="M10 21 16 9 22 21Z" fill="none" strokeWidth="2" strokeLinejoin="round" className="stroke-primary-foreground" />
      <g className="fill-primary-foreground">
        <circle cx="10" cy="21" r="3" />
        <circle cx="16" cy="9" r="3" />
        <circle cx="22" cy="21" r="3" />
      </g>
    </svg>
  );
}

export function Wordmark({ className }: { className?: string }) {
  return (
    <span className={cn("flex items-center gap-2 font-semibold tracking-tight", className)}>
      <LogoMark />
      <span>CortexDB</span>
    </span>
  );
}
