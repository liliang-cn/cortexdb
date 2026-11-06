// The graph page: a full-bleed 3D canvas with the panels that read it.
//
// This module is loaded lazily (three.js is most of the app), so the other
// pages never pay for it. The scene itself lives in lib/graph/scene.ts and is
// driven by bind.ts; this file lays out the panels around it and carries the
// page-level wiring: keyboard, the messages a framing page may send, and the
// URL options that open the page somewhere.

import { useEffect, useRef } from "react";
import { ChevronDown, ChevronUp, Layers, Loader2, Radio, Route, SlidersHorizontal, ScanSearch, X } from "lucide-react";
import { Empty } from "@/components/Page";
import { Button } from "@/components/ui/button";
import { GraphScene } from "@/lib/graph/scene";
import { setMode } from "@/lib/theme";
import { useCapabilities, useLive } from "@/live";
import { cn } from "@/lib/utils";
import { clearHighlight, clearPath, clearSelection, focusNode, onNodeClick, runExplore, runOpeningActions, selectEdge, startPathPick } from "./actions";
import { bindScene } from "./bind";
import { setScene, getScene } from "./controller";
import { ContractContent } from "./Contract";
import { ControlsContent } from "./Controls";
import { ExploreBar, ResultsContent } from "./Explore";
import { HoverTip } from "./HoverTip";
import { EdgeInspector, NodeInspector, NodeTitle } from "./Inspector";
import { LegendContent } from "./Legend";
import { PinBanner } from "./PinBanner";
import { BottomSheet, Card, CardHeader, IconButton, useIsNarrow } from "./primitives";
import { LiveDot, RailContent } from "./Rail";
import { OPTS, isNarrow, useGraphUI } from "./state";
import { Toolbar } from "./Toolbar";
import { useMerged } from "./hooks";

const FIT_AT = OPTS.fit ? [400, 1600, 3400, 6000] : [400];

export default function GraphView() {
  const stage = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLDivElement>(null);
  const labels = useRef<HTMLDivElement>(null);
  const moved = useRef(false);
  const ready = useLive((s) => s.ready);
  const narrow = useIsNarrow();

  // The scene is created once and torn down with the page.
  useEffect(() => {
    const c = canvas.current;
    const l = labels.current;
    if (!c || !l) return;
    const ui = () => useGraphUI.getState();
    const scene = new GraphScene(c, l, {
      onNodeClick,
      onNodeHover: (id) => {
        if (!isNarrow()) ui().set({ hovered: id });
      },
      onLinkClick: (key) => {
        if (ui().pick === "off") selectEdge(key);
      },
      onLinkHover: (key) => ui().set({ hoverEdge: key }),
      onBackgroundClick: () => {
        if (ui().pick !== "off") return;
        const s = ui();
        if (s.selected || s.selectedEdge) clearSelection();
        else if (s.highlight.size) clearHighlight();
        else if (s.path) clearPath();
      },
      onCameraMoved: () => {
        moved.current = true;
        if (ui().orbit) ui().set({ orbit: false });
      },
    });
    setScene(scene);
    if (import.meta.env.DEV) (window as unknown as Record<string, unknown>).__graph = { scene, ui: useGraphUI, live: useLive };
    const unbind = bindScene(scene);
    return () => {
      unbind();
      setScene(null);
      scene.dispose();
    };
  }, []);

  useEffect(() => getScene()?.setCompact(narrow), [narrow]);
  const orbit = useGraphUI((s) => s.orbit);
  useEffect(() => getScene()?.setOrbit(orbit ? Math.max(1, OPTS.spin) : 0), [orbit]);

  // Fitting once is always wrong: the layout is still expanding when the first
  // frame arrives. Fit again as it settles, and stop as soon as the person
  // touches the camera, because re-framing under someone's hand is worse than a loose fit.
  const nodeCount = useLive((s) => s.nodes.size);
  const fitted = useRef(false);
  useEffect(() => {
    if (!ready || fitted.current || nodeCount === 0) return;
    fitted.current = true;
    const timers = FIT_AT.map((ms) => setTimeout(() => { if (!moved.current) getScene()?.fit(700); }, ms));
    if (OPTS.spin > 0) timers.push(setTimeout(() => { if (!moved.current) useGraphUI.getState().set({ orbit: true }); }, (FIT_AT.at(-1) ?? 0) + 900));
    return () => timers.forEach(clearTimeout);
  }, [ready, nodeCount]);

  useEffect(() => {
    if (ready) runOpeningActions();
  }, [ready]);

  // Keyboard: "/" to search, Escape to let go of everything, "f" to fit.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null;
      const typing = !!t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable);
      if (e.key === "Escape") {
        if (typing) return void (t as HTMLElement).blur();
        const s = useGraphUI.getState();
        s.set({ selected: null, selectedEdge: null, highlight: new Set(), preview: null, mobile: "none" });
        clearPath();
        return;
      }
      if (typing || e.metaKey || e.ctrlKey || e.altKey) return;
      if (e.key === "/") {
        e.preventDefault();
        const s = useGraphUI.getState();
        s.set({ focusSearch: s.focusSearch + 1 });
      } else if (e.key === "f") getScene()?.fit(700);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  // An embedding page cannot press the buttons, so it sends messages. Unknown
  // ones are ignored: a page that speaks a later dialect gets a view that
  // works, not one that throws.
  useEffect(() => {
    const onMessage = (ev: MessageEvent) => {
      const m = ev.data as { type?: unknown; id?: unknown; query?: unknown; theme?: unknown; mode?: unknown } | null;
      if (!m || typeof m.type !== "string") return;
      if (m.type === "cortexdb:focus" && typeof m.id === "string") void focusNode(m.id, 0);
      else if (m.type === "cortexdb:find" && typeof m.query === "string") void runExplore("find", m.query);
      else if (m.type === "cortexdb:ask" && typeof m.query === "string") void runExplore("ask", m.query);
      else if (m.type === "cortexdb:cypher" && typeof m.query === "string") void runExplore("cypher", m.query);
      else if (m.type === "cortexdb:highlight") useGraphUI.getState().set({ query: typeof m.query === "string" ? m.query : "" });
      else if (m.type === "cortexdb:theme" && (m.mode === "light" || m.mode === "dark" || m.mode === "auto")) setMode(m.mode);
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, []);

  const stageStyle = OPTS.bg ? { background: OPTS.bg } : undefined;

  return (
    <div className="relative flex h-full min-h-0 flex-col">
      <PinBanner />
      <div ref={stage} className="relative min-h-0 flex-1 overflow-hidden bg-background" style={stageStyle}>
        <div ref={canvas} className="absolute inset-0" />
        <div ref={labels} className="pointer-events-none absolute inset-0 overflow-hidden" aria-hidden />
        <HoverTip stage={stage} />
        <Status ready={ready} />
        {OPTS.panels && (narrow ? <MobileChrome /> : <DesktopChrome />)}
        <PathChip narrow={narrow} />
        <Toast narrow={narrow} />
      </div>
    </div>
  );
}

function Status({ ready }: { ready: boolean }) {
  const n = useLive((s) => s.nodes.size);
  if (!ready) {
    return (
      <div className="pointer-events-none absolute inset-0 flex items-center justify-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" />Reading the brain
      </div>
    );
  }
  if (n === 0) {
    return (
      <div className="pointer-events-none absolute inset-0 flex items-center justify-center p-6">
        <div className="max-w-sm"><Empty title="Nothing in the graph yet">Entities and relations show up here as they are written.</Empty></div>
      </div>
    );
  }
  return null;
}

// ---- desktop ------------------------------------------------------------

function InspectorCard() {
  const selected = useGraphUI((s) => s.selected);
  const selectedEdge = useGraphUI((s) => s.selectedEdge);
  if (!selected && !selectedEdge) return null;
  return (
    <Card className="min-h-0 flex-1">
      <CardHeader title={selected ? <NodeTitle id={selected} /> : "Relation"} onClose={clearSelection} />
      {selected ? <NodeInspector id={selected} /> : selectedEdge ? <EdgeInspector edgeKey={selectedEdge} /> : null}
    </Card>
  );
}

function FoldCard({ title, icon, open, onToggle, className, children }: { title: string; icon: React.ReactNode; open: boolean; onToggle: () => void; className?: string; children: React.ReactNode }) {
  return (
    <Card className={cn(open && "min-h-0", className)}>
      <button type="button" onClick={onToggle} aria-expanded={open} className={cn("flex h-10 w-full shrink-0 items-center gap-2 px-3 text-left outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:ring-inset", open && "border-b")}>
        <span className="text-muted-foreground [&>svg]:size-4">{icon}</span>
        <h2 className="flex-1 text-sm font-medium">{title}</h2>
        {open ? <ChevronDown className="size-4 text-muted-foreground" /> : <ChevronUp className="size-4 text-muted-foreground" />}
      </button>
      {open && children}
    </Card>
  );
}

function DesktopChrome() {
  const panels = useGraphUI((s) => s.panels);
  const setPanel = useGraphUI((s) => s.setPanel);
  const resultsOpen = useGraphUI((s) => s.resultsOpen);
  const hasResult = useGraphUI((s) => s.result.status !== "idle");
  const hasSelection = useGraphUI((s) => !!s.selected || !!s.selectedEdge);
  const set = useGraphUI((s) => s.set);
  const caps = useCapabilities();
  const showExplore = OPTS.explore && caps.explore;
  const results = showExplore && hasResult && resultsOpen;
  return (
    <div className="pointer-events-none absolute inset-3 flex min-h-0 gap-3">
      <div className="flex min-h-0 w-[22rem] shrink-0 flex-col gap-3">
        {showExplore && (
          <div className="relative z-20 shrink-0">
            <Card>
              <ExploreBar />
            </Card>
            {results && (
              <Card className="absolute inset-x-0 top-full mt-2 max-h-[min(30rem,calc(100dvh-14rem))]">
                <ResultsContent />
              </Card>
            )}
            {showExplore && hasResult && !resultsOpen && (
              <Button variant="secondary" size="sm" className="pointer-events-auto absolute right-2 top-full mt-1.5 h-7 shadow-sm" onClick={() => set({ resultsOpen: true })}>Show results</Button>
            )}
          </div>
        )}
        {hasSelection ? <InspectorCard /> : <div className="min-h-0 flex-1" />}
        <FoldCard title="Key" icon={<Layers />} open={panels.legend} onToggle={() => setPanel("legend", !panels.legend)} className={cn(panels.legend && (hasSelection ? "max-h-[32%]" : "max-h-[60%]"))}>
          <LegendContent />
        </FoldCard>
      </div>
      <div className="min-w-0 flex-1" />
      <div className="flex min-h-0 items-stretch gap-3">
        <div className="self-start"><Toolbar /></div>
        {panels.rail && (
          <Card className="h-full w-80 shrink-0">
            <CardHeader title="Live" icon={<Radio />}><LiveDot /></CardHeader>
            <RailContent />
          </Card>
        )}
      </div>
    </div>
  );
}

// ---- phone --------------------------------------------------------------

function MobileChrome() {
  const mobile = useGraphUI((s) => s.mobile);
  const set = useGraphUI((s) => s.set);
  const selected = useGraphUI((s) => s.selected);
  const selectedEdge = useGraphUI((s) => s.selectedEdge);
  const caps = useCapabilities();
  const g = useMerged();
  const close = () => set({ mobile: "none" });
  const open = (m: typeof mobile) => set({ mobile: mobile === m ? "none" : m });
  const showExplore = OPTS.explore && caps.explore;
  void g;
  return (
    <div className="pointer-events-none absolute inset-0">
      {showExplore && (
        <div className="absolute inset-x-3 top-3 z-10">
          <Card><ExploreBar compact /></Card>
        </div>
      )}

      <Card className="absolute bottom-3 left-1/2 z-10 -translate-x-1/2 flex-row gap-0.5 rounded-full p-1" >
        <IconButton label="Key" icon={<Layers />} active={mobile === "legend"} onClick={() => open("legend")} className="rounded-full" />
        <IconButton label="Live changes" icon={<Radio />} active={mobile === "rail"} onClick={() => open("rail")} className="rounded-full" />
        <IconButton label="View settings" icon={<SlidersHorizontal />} active={mobile === "controls"} onClick={() => open("controls")} className="rounded-full" />
        <IconButton label="Fit the graph" icon={<ScanSearch />} onClick={() => getScene()?.fit(700)} className="rounded-full" />
        <IconButton label="Trace a path" icon={<Route />} onClick={() => { close(); startPathPick(); }} className="rounded-full" />
      </Card>

      <BottomSheet open={mobile === "inspector" && !!(selected || selectedEdge)} onClose={() => { clearSelection(); }} title={selected ? <NodeTitle id={selected} /> : "Relation"}>
        {selected ? <NodeInspector id={selected} /> : selectedEdge ? <EdgeInspector edgeKey={selectedEdge} /> : null}
      </BottomSheet>
      <BottomSheet open={mobile === "results"} onClose={close} title="Results" tall>
        <ResultsContent />
      </BottomSheet>
      <BottomSheet open={mobile === "legend"} onClose={close} title="Key" icon={<Layers />} tall>
        <LegendContent />
      </BottomSheet>
      <BottomSheet open={mobile === "rail"} onClose={close} title="Live" icon={<Radio />} tall>
        <RailContent />
      </BottomSheet>
      <BottomSheet open={mobile === "controls"} onClose={close} title="View" icon={<SlidersHorizontal />} tall>
        <ControlsContent />
        {OPTS.contract && <div className="border-t"><ContractContent /></div>}
      </BottomSheet>
    </div>
  );
}

// ---- transient notes ----------------------------------------------------

function PathChip({ narrow }: { narrow: boolean }) {
  const note = useGraphUI((s) => s.pathNote);
  const pick = useGraphUI((s) => s.pick);
  if (!note) return null;
  return (
    <div className={cn("pointer-events-none absolute inset-x-0 flex justify-center px-3", narrow ? "bottom-16" : "bottom-3")}>
      <div className="pointer-events-auto flex max-w-xl items-center gap-2 rounded-full border bg-card/95 py-1 pl-3.5 pr-1.5 text-sm shadow-md backdrop-blur-md">
        <Route aria-hidden className="size-4 shrink-0 text-muted-foreground" />
        <span className="min-w-0 truncate">{note}</span>
        <Button variant="ghost" size="icon" className="size-7 shrink-0" onClick={clearPath} aria-label={pick !== "off" ? "Cancel" : "Clear the path"}><X className="size-4" /></Button>
      </div>
    </div>
  );
}

function Toast({ narrow }: { narrow: boolean }) {
  const text = useGraphUI((s) => s.toast);
  return (
    <div role="status" aria-live="polite" className={cn("pointer-events-none absolute inset-x-0 flex justify-center transition-opacity duration-200", narrow ? "bottom-[4.5rem]" : "bottom-14", text ? "opacity-100" : "opacity-0")}>
      {text && <div className="rounded-full bg-foreground px-4 py-1.5 text-sm text-background shadow-lg">{text}</div>}
    </div>
  );
}
