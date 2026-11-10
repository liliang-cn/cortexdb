import type { ReactNode } from "react";
import { FlaskConical, RefreshCw } from "lucide-react";
import { DRAFT_STATES, isDraft, type OntologyAnswer } from "@/api/ontology";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Toggle } from "@/components/ui/toggle";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import type { OntologyOptions } from "@/lib/ontology";
import { schemaChoices } from "@/lib/ontologyFinding";
import type { OntologyPalette } from "@/lib/ontologyPalette";
import { useText } from "@/lib/i18n";
import { ONTOLOGY_TEXT } from "@/lib/ontologyStrings";
import { cn } from "@/lib/utils";

/** The thresholds offered without editing the URL; any other value still works from ?min=. */
const THRESHOLDS = ["0", "1", "2", "3", "5", "10", "25"];

function Tip({ tip, children }: { tip: string; children: ReactNode }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>{children}</TooltipTrigger>
      <TooltipContent className="max-w-xs">{tip}</TooltipContent>
    </Tooltip>
  );
}

function Labelled({ id, label, tip, checked, disabled, onChange }: { id: string; label: string; tip: string; checked: boolean; disabled?: boolean; onChange: (v: boolean) => void }) {
  return (
    <Tip tip={tip}>
      <span className={cn("inline-flex items-center gap-2 text-sm", disabled && "text-muted-foreground")}>
        <Switch id={id} checked={checked} disabled={disabled} onCheckedChange={onChange} />
        <label htmlFor={id} className={cn(!disabled && "cursor-pointer")}>
          {label}
        </label>
      </span>
    </Tip>
  );
}

export function Controls({
  opts,
  set,
  rep,
  loading,
  reload,
  pal,
}: {
  opts: OntologyOptions;
  set: (patch: Partial<OntologyOptions>) => void;
  rep: OntologyAnswer | null;
  loading: boolean;
  reload: () => void;
  pal: OntologyPalette;
}) {
  const t = useText(ONTOLOGY_TEXT);
  const choices = rep ? schemaChoices(rep) : [];
  const draft = rep && isDraft(rep) ? rep : null;
  // The threshold the draft actually ran with, which is the number a reader
  // is being invited to disagree with, rather than the one the URL hoped for.
  // A draft nobody could derive ran with nothing, so it shows the URL's.
  const ran = draft && draft.state !== DRAFT_STATES.undraftable ? (draft.min_nodes === draft.min_edges ? String(draft.min_nodes) : `${draft.min_nodes}/${draft.min_edges}`) : (opts.min || "3");
  const thresholds = THRESHOLDS.includes(ran) ? THRESHOLDS : [...THRESHOLDS, ran];

  return (
    <div className="flex flex-wrap items-center gap-x-5 gap-y-3" role="group" aria-label={t.whatToDraw}>
      {/* The gap has nothing to switch off on a draft, so the switch is
          disabled rather than left looking like a lever that does nothing.
          It keeps its position and stops claiming to hold one. */}
      <Labelled
        id="ontology-gap"
        label={t.gap}
        tip={opts.draft ? t.gapTipDraft : t.gapTip}
        checked={opts.gap && !opts.draft}
        disabled={opts.draft}
        onChange={(gap) => set({ gap })}
      />
      <Labelled id="ontology-strays" label={t.strays} tip={t.straysTip} checked={opts.strays} onChange={(strays) => set({ strays })} />
      <Tip tip={t.draftTip}>
        <Toggle
          variant="outline"
          size="sm"
          pressed={opts.draft}
          onPressedChange={(on) => set({ draft: on })}
          className="px-2.5"
          // The one control that changes what the whole page is about wears
          // the draft's own colour rather than the declarations'.
          style={opts.draft ? { borderColor: pal.draft, color: pal.draftInk, background: `color-mix(in oklab, ${pal.draft} 12%, var(--card))` } : undefined}
        >
          <FlaskConical aria-hidden />
          {t.draft}
        </Toggle>
      </Tip>
      {opts.draft && (
        <span className="inline-flex items-center gap-2 text-sm">
          <label htmlFor="ontology-min" className="text-muted-foreground">
            {t.threshold}
          </label>
          <Select value={ran} onValueChange={(v) => !v.includes("/") && set({ min: v, minNodes: "", minEdges: "" })}>
            <SelectTrigger id="ontology-min" size="sm" className="min-w-16" aria-label={t.thresholdLabel}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {thresholds.map((th) => (
                <SelectItem key={th} value={th} disabled={th.includes("/")}>
                  {th.includes("/") ? t.fromUrl(th) : th}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </span>
      )}
      {choices.length > 0 && (
        <span className="inline-flex items-center gap-2 text-sm">
          <label htmlFor="ontology-schema" className="text-muted-foreground">
            {t.schema}
          </label>
          <Select value={choices.find((c) => c.current)?.schema_id ?? ""} onValueChange={(schema) => set({ schema })}>
            <SelectTrigger id="ontology-schema" size="sm" className="max-w-56">
              <SelectValue placeholder={t.chooseSchema} />
            </SelectTrigger>
            <SelectContent>
              {choices.map((c) => (
                <SelectItem key={c.schema_id} value={c.schema_id}>
                  <span className="truncate">{c.schema_id}</span>
                  {c.active && <span className="text-xs text-muted-foreground">{t.active}</span>}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </span>
      )}
      <Button variant="ghost" size="sm" onClick={reload} aria-label={t.refreshLabel} className="text-muted-foreground">
        <RefreshCw aria-hidden className={cn(loading && "animate-spin motion-reduce:animate-none")} />
        {t.refresh}
      </Button>
    </div>
  );
}
