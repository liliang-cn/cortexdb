import { Orbit, Radio, ScanSearch, ShieldCheck, SlidersHorizontal, Route, Tag, Layers } from "lucide-react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { clearPath, startPathPick } from "./actions";
import { ContractContent } from "./Contract";
import { ControlsContent } from "./Controls";
import { getScene } from "./controller";
import { CardHeader, IconButton } from "./primitives";
import { useGraphUI } from "./state";
import { OPTS } from "./state";

function PopoverButton({ label, icon, open, onOpenChange, title, children, width = "w-80" }: { label: string; icon: React.ReactNode; open: boolean; onOpenChange: (o: boolean) => void; title: string; children: React.ReactNode; width?: string }) {
  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      <Tooltip>
        <TooltipTrigger asChild>
          <PopoverTrigger asChild>
            <Button variant="ghost" size="icon" aria-label={label} className={cn("size-8 text-muted-foreground hover:text-foreground [&>svg]:size-[1.05rem]", open && "bg-accent text-accent-foreground")}>
              {icon}
            </Button>
          </PopoverTrigger>
        </TooltipTrigger>
        <TooltipContent side="left">{label}</TooltipContent>
      </Tooltip>
      <PopoverContent side="left" align="start" sideOffset={10} className={cn("flex max-h-[min(34rem,calc(100dvh-8rem))] flex-col overflow-hidden p-0", width)}>
        <CardHeader title={title} className="h-9" />
        {children}
      </PopoverContent>
    </Popover>
  );
}

/** The canvas's own controls, as a strip beside the rail. */
export function Toolbar() {
  const panels = useGraphUI((s) => s.panels);
  const setPanel = useGraphUI((s) => s.setPanel);
  const orbit = useGraphUI((s) => s.orbit);
  const labels = useGraphUI((s) => s.labels);
  const pick = useGraphUI((s) => s.pick);
  const path = useGraphUI((s) => s.path);
  const set = useGraphUI((s) => s.set);
  const pathActive = pick !== "off" || !!path;

  return (
    <div role="toolbar" aria-label="Canvas" className="pointer-events-auto flex flex-col gap-0.5 rounded-xl border bg-card/92 p-1 shadow-[0_1px_2px_rgb(0_0_0/0.04),0_8px_24px_-12px_rgb(0_0_0/0.18)] backdrop-blur-md">
      <IconButton label="Fit the graph" kbd="F" icon={<ScanSearch />} onClick={() => getScene()?.fit(700)} />
      <IconButton label={orbit ? "Stop orbiting" : "Orbit"} icon={<Orbit />} active={orbit} onClick={() => set({ orbit: !orbit })} />
      <IconButton label={labels ? "Hide names" : "Show names"} icon={<Tag />} active={labels} onClick={() => set({ labels: !labels })} />
      <IconButton label={pathActive ? "Clear the path" : "Trace a path between two nodes"} icon={<Route />} active={pathActive} onClick={() => (pathActive ? clearPath() : startPathPick())} />
      <span aria-hidden className="mx-1.5 my-0.5 h-px bg-border" />
      <PopoverButton label="View settings" title="View" icon={<SlidersHorizontal />} open={panels.controls} onOpenChange={(o) => setPanel("controls", o)}>
        <ControlsContent />
      </PopoverButton>
      {OPTS.contract && (
        <PopoverButton label="Knowledge contract" title="Knowledge contract" icon={<ShieldCheck />} open={panels.contract} onOpenChange={(o) => setPanel("contract", o)} width="w-96">
          <ContractContent />
        </PopoverButton>
      )}
      <IconButton label={panels.legend ? "Hide the key" : "Show the key"} icon={<Layers />} active={panels.legend} onClick={() => setPanel("legend", !panels.legend)} />
      <IconButton label={panels.rail ? "Hide live changes" : "Show live changes"} icon={<Radio />} active={panels.rail} onClick={() => setPanel("rail", !panels.rail)} />
    </div>
  );
}

