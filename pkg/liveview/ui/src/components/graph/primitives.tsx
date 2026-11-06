// Small pieces the graph panels share.

import { useSyncExternalStore, type ReactNode } from "react";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import type { Side } from "@/lib/graph/insets";
import { cn } from "@/lib/utils";

const NARROW = "(max-width: 767px)";

/** True below the md breakpoint, where panels become sheets. */
export function useIsNarrow(): boolean {
  return useSyncExternalStore(
    (cb) => {
      const m = window.matchMedia(NARROW);
      m.addEventListener("change", cb);
      return () => m.removeEventListener("change", cb);
    },
    () => window.matchMedia(NARROW).matches,
    () => false,
  );
}

/**
 * A floating card: the chrome every desktop panel on the canvas shares.
 * occludes names the side of the stage it covers, so the camera aims clear of it.
 */
export function Card({ className, children, occludes }: { className?: string; children: ReactNode; occludes?: Side }) {
  return (
    <section
      data-occludes={occludes}
      className={cn(
        "pointer-events-auto flex min-h-0 flex-col overflow-hidden rounded-xl border bg-card/92 text-card-foreground shadow-[0_1px_2px_rgb(0_0_0/0.04),0_8px_24px_-12px_rgb(0_0_0/0.18)] backdrop-blur-md",
        className,
      )}
    >
      {children}
    </section>
  );
}

export function CardHeader({ title, icon, children, onClose, className }: { title: ReactNode; icon?: ReactNode; children?: ReactNode; onClose?: () => void; className?: string }) {
  return (
    <header className={cn("flex h-10 shrink-0 items-center gap-2 border-b px-3", className)}>
      {icon && <span className="text-muted-foreground [&>svg]:size-4">{icon}</span>}
      <h2 className="min-w-0 flex-1 truncate text-sm font-medium">{title}</h2>
      {children}
      {onClose && (
        <Button variant="ghost" size="icon" className="-mr-1.5 size-7" onClick={onClose} aria-label="Close">
          <X className="size-4" />
        </Button>
      )}
    </header>
  );
}

/** A bottom sheet that leaves the canvas above it live: a phone's panel. */
export function BottomSheet({ open, onClose, title, icon, tall, children }: { open: boolean; onClose: () => void; title: ReactNode; icon?: ReactNode; tall?: boolean; children: ReactNode }) {
  if (!open) return null;
  return (
    <section
      role="dialog"
      aria-label={typeof title === "string" ? title : undefined}
      data-occludes="bottom"
      className={cn(
        "pointer-events-auto absolute inset-x-0 bottom-0 z-30 flex flex-col overflow-hidden rounded-t-2xl border-x border-t bg-card text-card-foreground shadow-[0_-12px_32px_-12px_rgb(0_0_0/0.28)]",
        "animate-in slide-in-from-bottom-6 duration-200",
        tall ? "h-[72%]" : "h-[46%]",
      )}
      style={{ paddingBottom: "env(safe-area-inset-bottom)" }}
    >
      <div aria-hidden className="mx-auto mt-1.5 h-1 w-9 shrink-0 rounded-full bg-border" />
      <CardHeader title={title} icon={icon} onClose={onClose} className="h-9" />
      <div className="flex min-h-0 flex-1 flex-col">{children}</div>
    </section>
  );
}

export function IconButton({ label, icon, active, onClick, className, kbd }: { label: string; icon: ReactNode; active?: boolean; onClick?: () => void; className?: string; kbd?: string }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label={label}
          aria-pressed={active}
          onClick={onClick}
          className={cn("size-9 text-muted-foreground hover:text-foreground md:size-8 [&>svg]:size-[1.05rem]", active && "bg-accent text-accent-foreground hover:bg-accent", className)}
        >
          {icon}
        </Button>
      </TooltipTrigger>
      <TooltipContent side="left">
        {label}
        {kbd && <kbd className="ml-2 font-mono text-[10px] opacity-70">{kbd}</kbd>}
      </TooltipContent>
    </Tooltip>
  );
}

export function Segmented<T extends string>({ value, onChange, options, className, size = "md" }: { value: T; onChange: (v: T) => void; options: Array<{ value: T; label: ReactNode; title?: string }>; className?: string; size?: "sm" | "md" }) {
  return (
    <div role="radiogroup" className={cn("inline-flex rounded-lg bg-muted p-0.5", className)}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={value === o.value}
          title={o.title}
          onClick={() => onChange(o.value)}
          className={cn(
            "rounded-md px-2.5 font-medium text-muted-foreground transition-colors outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50",
            size === "sm" ? "h-6 text-xs" : "h-7 text-[13px]",
            value === o.value ? "bg-card text-foreground shadow-sm" : "hover:text-foreground",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function SectionTitle({ children, aside }: { children: ReactNode; aside?: ReactNode }) {
  return (
    <div className="mb-1.5 mt-4 flex items-center gap-2 first:mt-0">
      <h3 className="text-xs font-medium text-muted-foreground">{children}</h3>
      {aside && <span className="ml-auto">{aside}</span>}
    </div>
  );
}

export function Hint({ children, tone }: { children: ReactNode; tone?: "bad" }) {
  return <p className={cn("text-xs leading-relaxed", tone === "bad" ? "text-destructive" : "text-muted-foreground")}>{children}</p>;
}
