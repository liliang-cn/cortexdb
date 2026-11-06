import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useLinkView } from "@/live";
import { LINK_TONES } from "@/lib/semantics";
import { cn } from "@/lib/utils";

export function ConnectionPill({ className }: { className?: string }) {
  const view = useLinkView();
  const tone = LINK_TONES[view.kind];
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          data-link={view.kind}
          className={cn(
            "inline-flex h-8 max-w-[11rem] items-center gap-2 rounded-full border bg-card px-3 text-xs font-medium sm:max-w-none",
            "outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50",
            tone.text,
            className,
          )}
        >
          <span className="relative flex size-2 shrink-0">
            {tone.pulse && <span className={cn("absolute inline-flex size-full animate-ping rounded-full opacity-50 motion-reduce:animate-none", tone.dot)} />}
            <span className={cn("relative inline-flex size-2 rounded-full", tone.dot)} />
          </span>
          <span className="truncate">{view.label}</span>
        </button>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs">{view.detail}</TooltipContent>
    </Tooltip>
  );
}
