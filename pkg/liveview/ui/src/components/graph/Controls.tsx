import { useState } from "react";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { useCapabilities, useLive } from "@/live";
import { Hint, SectionTitle, Segmented } from "./primitives";
import { useGraphUI } from "./state";
import type { ColorMode } from "@/lib/graph/scene";
import { pageShown } from "@/lib/pages";
import { GRAPH_TEXT } from "@/lib/graphStrings";
import { useText } from "@/lib/i18n";

/** The local date-time field takes wall-clock time; the wire takes unix milliseconds. The two meet only here. */
function localValue(ms: number): string {
  const d = new Date(ms);
  const p = (v: number) => String(v).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

function Toggle({ label, hint, checked, onChange, id }: { label: string; hint?: string; checked: boolean; onChange: (v: boolean) => void; id: string }) {
  return (
    <div className="flex items-start gap-3 py-1">
      <div className="min-w-0 flex-1">
        <label htmlFor={id} className="block text-[13px] leading-snug">{label}</label>
        {hint && <span className="block text-xs leading-snug text-muted-foreground">{hint}</span>}
      </div>
      <Switch id={id} checked={checked} onCheckedChange={onChange} className="mt-0.5" />
    </div>
  );
}

function AsOf() {
  const pinnedAt = useLive((s) => s.pinnedAt);
  const asOf = useLive((s) => s.asOf);
  const pinReason = useLive((s) => s.pinReason);
  const pin = useLive((s) => s.pin);
  const unpin = useLive((s) => s.unpin);
  const caps = useCapabilities();
  const [raw, setRaw] = useState(() => (asOf ? localValue(asOf) : ""));
  const [note, setNote] = useState("");
  const t = useText(GRAPH_TEXT);

  const doPin = () => {
    if (!raw) return setNote(t.pickInstant);
    const ms = new Date(raw).getTime();
    if (!(ms > 0)) return setNote(t.notInstant);
    setNote("");
    pin(ms);
  };

  return (
    <div className="space-y-2">
      <Input type="datetime-local" step={1} value={raw} onChange={(e) => setRaw(e.target.value)} aria-label={t.asOf} className="h-9 text-base md:text-sm" />
      <div className="flex gap-2">
        <Button size="sm" variant={asOf ? "default" : "secondary"} className="flex-1" onClick={doPin}>{t.pin}</Button>
        <Button size="sm" variant="secondary" className="flex-1" disabled={!asOf} onClick={() => { setNote(""); unpin(); }}>{t.backToNow}</Button>
      </div>
      {pinReason ? <Hint tone="bad">{t.stillNow(pinReason)}</Hint> : null}
      {note ? <Hint tone="bad">{note}</Hint> : null}
      {!note && !pinReason && !caps.temporal && <Hint>{t.noPast}</Hint>}
      {pinnedAt && <Hint>{t.pinnedStopped}</Hint>}
    </div>
  );
}

export function ControlsContent() {
  const colorMode = useGraphUI((s) => s.colorMode);
  const filters = useGraphUI((s) => s.filters);
  const flow = useGraphUI((s) => s.flow);
  const labels = useGraphUI((s) => s.labels);
  const query = useGraphUI((s) => s.query);
  const set = useGraphUI((s) => s.set);
  const setCoOccurs = useGraphUI((s) => s.setCoOccurs);
  const caps = useCapabilities();
  const t = useText(GRAPH_TEXT);

  return (
    <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-3 py-3">
      <SectionTitle>{t.findOnCanvas}</SectionTitle>
      <Input value={query} onChange={(e) => set({ query: e.target.value })} placeholder={t.highlightByName} aria-label={t.highlightByName} className="h-9 text-base md:text-sm" />

      <SectionTitle>{t.colourBy}</SectionTitle>
      <Segmented<ColorMode>
        value={colorMode}
        onChange={(v) => set({ colorMode: v })}
        options={[
          { value: "type", label: t.type },
          { value: "grade", label: t.grade, title: t.gradeTitle },
          // Colouring by recalls reads the recall log, which goes with the Recalls page when a host leaves it out.
          ...(pageShown("/recalls") ? [{ value: "recalls" as const, label: t.recalls, title: t.recallsTitle }] : []),
        ]}
      />
      {colorMode === "grade" && !caps.grades && <p className="mt-1.5"><Hint tone="bad">{t.noGradesShort}</Hint></p>}

      <SectionTitle>{t.show}</SectionTitle>
      <Toggle id="gc-co" label={t.coOccursLabel} hint={t.coOccursHint} checked={filters.showCoOccurs} onChange={setCoOccurs} />
      <Toggle id="gc-names" label={t.namesOnCanvas} checked={labels} onChange={(v) => set({ labels: v })} />
      <Toggle id="gc-flow" label={t.pulses} hint={t.pulsesHint} checked={flow} onChange={(v) => set({ flow: v })} />

      <SectionTitle>{t.asOf}</SectionTitle>
      <AsOf />
    </div>
  );
}
