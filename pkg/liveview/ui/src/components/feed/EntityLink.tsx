import { Link } from "react-router";
import { cn } from "@/lib/utils";

export function entityPath(id: string): string {
  return `/entity?id=${encodeURIComponent(id)}`;
}

/** A link to an entity's page; the text is its label, or the id when nothing is known. */
export function EntityLink({ id, label, type, className }: { id: string; label?: string; type?: string; className?: string }) {
  return (
    <Link
      to={entityPath(id)}
      onClick={(e) => e.stopPropagation()}
      title={type ? `${label || id} (${type})` : id}
      className={cn(
        "rounded-sm font-medium text-foreground underline decoration-border decoration-1 underline-offset-4 outline-none",
        "hover:text-primary hover:decoration-primary focus-visible:ring-[3px] focus-visible:ring-ring/50",
        className,
      )}
    >
      {label || id}
    </Link>
  );
}

/** A relation drawn as a line with its predicate on it: —predicate→ */
export function Predicate({ name, className }: { name: string; className?: string }) {
  return (
    <span className={cn("inline-flex items-center align-middle text-muted-foreground", className)} aria-label={name}>
      <span aria-hidden className="h-px w-2.5 bg-current opacity-60" />
      <span className="rounded-sm bg-secondary px-1.5 py-0.5 font-mono text-[0.78rem] leading-none text-secondary-foreground">{name}</span>
      <span aria-hidden className="h-px w-2.5 bg-current opacity-60" />
      <svg aria-hidden viewBox="0 0 6 8" className="-ml-px h-2 w-1.5 fill-current opacity-60">
        <path d="M0 0 L6 4 L0 8 Z" />
      </svg>
    </span>
  );
}
