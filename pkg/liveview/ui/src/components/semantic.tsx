// Chips for the three shared meanings. Pages use these (or the Semantic they
// are built from) rather than choosing a colour, so a meaning looks the same
// everywhere.

import type { ChangeOp } from "@/api";
import { Badge } from "@/components/ui/badge";
import { activityTone, changeOp, grade, type Semantic } from "@/lib/semantics";
import { cn } from "@/lib/utils";

export function Chip({ s, label, className, iconOnly }: { s: Semantic; label?: string; className?: string; iconOnly?: boolean }) {
  const Icon = s.icon;
  return (
    <Badge variant="outline" title={s.means} className={cn("gap-1 border-transparent font-medium", s.chip, className)}>
      <Icon aria-hidden className="size-3" />
      {iconOnly ? <span className="sr-only">{label ?? s.label}</span> : (label ?? s.label)}
    </Badge>
  );
}

export const OpChip = ({ op, ...rest }: { op: ChangeOp | string; className?: string; iconOnly?: boolean }) => (
  <Chip s={changeOp(op)} {...rest} />
);

export const KindChip = ({ event, ...rest }: { event: { kind: string; failed?: boolean }; className?: string; iconOnly?: boolean }) => (
  <Chip s={activityTone(event)} {...rest} />
);

export const GradeChip = ({ value, ...rest }: { value?: string | null; className?: string; iconOnly?: boolean }) => (
  <Chip s={grade(value)} {...rest} />
);

/** A coloured dot with the meaning as its accessible name. */
export function Dot({ s, className }: { s: Semantic; className?: string }) {
  return <span role="img" aria-label={s.label} title={s.label} className={cn("inline-block size-2 shrink-0 rounded-full", s.dot, className)} />;
}
