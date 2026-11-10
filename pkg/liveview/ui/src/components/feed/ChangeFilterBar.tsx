import { useEffect, useState, type ReactNode } from "react";
import { Check, ChevronDown, X } from "lucide-react";
import { api, isAbort, type FindResult } from "@/api";
import { Button } from "@/components/ui/button";
import { Command, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { hasFilters, noFilters, toggleValue, type ChangeFilters, type FilterKey } from "@/lib/changes";
import { FEED_TEXT } from "@/lib/feedStrings";
import { useLang, useText } from "@/lib/i18n";
import { CHANGE_KIND_LABELS, CHANGE_OPS, CHANGE_OP_ORDER, changeKindLabel, semLabel } from "@/lib/semantics";
import { cn } from "@/lib/utils";
import { useLive } from "@/live";

interface Option {
  value: string;
  label: ReactNode;
  /** Text cmdk matches the search against. */
  text: string;
}

function Picker({
  title,
  selected,
  options,
  onToggle,
  searchPlaceholder,
  remote,
  custom,
}: {
  title: string;
  selected: string[];
  options: Option[];
  onToggle: (value: string) => void;
  searchPlaceholder: string;
  /** Options come from the server as the person types, not from `options`. */
  remote?: { search: (q: string, signal: AbortSignal) => Promise<Option[]> };
  /** A typed value that is not in the list may be used as is. */
  custom?: boolean;
}) {
  const [query, setQuery] = useState("");
  const [found, setFound] = useState<Option[]>([]);
  const t = useText(FEED_TEXT);

  useEffect(() => {
    if (!remote) return;
    const q = query.trim();
    if (q === "") {
      setFound([]);
      return;
    }
    const ctl = new AbortController();
    const t = setTimeout(() => {
      remote
        .search(q, ctl.signal)
        .then(setFound)
        .catch((err: unknown) => {
          if (!isAbort(err)) setFound([]);
        });
    }, 180);
    return () => {
      clearTimeout(t);
      ctl.abort();
    };
    // `remote` is rebuilt every render; its behaviour depends on nothing but the query.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query]);

  const list = remote ? found : options;
  const typed = query.trim();
  const canUseTyped = custom && typed !== "" && !options.some((o) => o.value === typed);

  return (
    <Popover onOpenChange={(o) => !o && setQuery("")}>
      <PopoverTrigger asChild>
        <Button variant="outline" size="sm" className={cn("gap-1.5", selected.length > 0 && "border-primary/50 bg-accent text-accent-foreground")}>
          {title}
          {selected.length > 0 && <span className="rounded-full bg-primary px-1.5 text-[0.68rem] leading-4 text-primary-foreground">{selected.length}</span>}
          <ChevronDown aria-hidden className="size-3.5 opacity-60" />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-64 p-0">
        <Command shouldFilter={!remote}>
          <CommandInput placeholder={searchPlaceholder} value={query} onValueChange={setQuery} />
          <CommandList>
            {list.length === 0 && !canUseTyped && (
              <div className="px-3 py-4 text-center text-sm text-muted-foreground">{remote && typed === "" ? t.typeToSearch : t.nothingToPick}</div>
            )}
            <CommandGroup>
              {canUseTyped && (
                <CommandItem value={`use ${typed}`} onSelect={() => { onToggle(typed); setQuery(""); }}>
                  {t.use} <span className="font-mono">{typed}</span>
                </CommandItem>
              )}
              {list.map((o) => {
                const on = selected.includes(o.value);
                return (
                  <CommandItem key={o.value} value={o.text} onSelect={() => onToggle(o.value)}>
                    <Check aria-hidden className={cn("size-4", on ? "opacity-100" : "opacity-0")} />
                    {o.label}
                  </CommandItem>
                );
              })}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}

function Tag({ label, onRemove, what }: { label: ReactNode; onRemove: () => void; what: string }) {
  const t = useText(FEED_TEXT);
  return (
    <span className="inline-flex max-w-full items-center gap-1 rounded-full border bg-card py-0.5 pr-1 pl-2.5 text-xs">
      <span className="text-muted-foreground">{what}</span>
      <span className="truncate font-medium">{label}</span>
      <button
        type="button"
        onClick={onRemove}
        aria-label={t.removeFilter(what)}
        className="flex size-4 items-center justify-center rounded-full text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50"
      >
        <X aria-hidden className="size-3" />
      </button>
    </span>
  );
}

/** Kind, op, producer and entity filters; the page keeps them in the URL. */
export function ChangeFilterBar({ filters, producers, onChange }: { filters: ChangeFilters; producers: string[]; onChange: (f: ChangeFilters) => void }) {
  const nodes = useLive((s) => s.nodes);
  const nameOf = (id: string) => nodes.get(id)?.label ?? id;
  const toggle = (k: FilterKey) => (v: string) => onChange({ ...filters, [k]: toggleValue(filters[k], v) });
  const lang = useLang();
  const t = FEED_TEXT[lang];
  const opWord = (op: string) => {
    const s = CHANGE_OPS[op as keyof typeof CHANGE_OPS];
    return s ? semLabel(s, lang) : op;
  };

  // A kind or op is matched by its word in either language and by its wire value.
  const kindOptions: Option[] = Object.keys(CHANGE_KIND_LABELS.en).map((k) => ({
    value: k,
    text: `${changeKindLabel(k, "en")} ${changeKindLabel(k, "zh")} ${k}`,
    label: changeKindLabel(k, lang),
  }));
  const opOptions: Option[] = CHANGE_OP_ORDER.map((op) => {
    const s = CHANGE_OPS[op];
    return {
      value: op,
      text: `${semLabel(s, "en")} ${semLabel(s, "zh")} ${op}`,
      label: (
        <span className="flex items-center gap-2">
          <span className={cn("size-2 rounded-full", s.dot)} aria-hidden />
          {semLabel(s, lang)}
        </span>
      ),
    };
  });
  const producerOptions: Option[] = producers.map((p) => ({ value: p, text: p, label: <span className="font-mono text-xs">{p}</span> }));
  const searchEntities = async (q: string, signal: AbortSignal): Promise<Option[]> => {
    const r = await api.find(q, undefined, { signal });
    return (r.results ?? []).slice(0, 12).map((n: FindResult) => ({
      value: n.id,
      text: n.id,
      label: (
        <span className="flex min-w-0 flex-1 items-baseline justify-between gap-2">
          <span className="truncate">{n.label}</span>
          <span className="shrink-0 text-xs text-muted-foreground">{n.type}</span>
        </span>
      ),
    }));
  };

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <Picker title={t.kind} selected={filters.kind} options={kindOptions} onToggle={toggle("kind")} searchPlaceholder={t.filterKinds} />
        <Picker title={t.op} selected={filters.op} options={opOptions} onToggle={toggle("op")} searchPlaceholder={t.filterOps} />
        <Picker title={t.producer} selected={filters.producer} options={producerOptions} onToggle={toggle("producer")} searchPlaceholder={t.producerName} custom />
        <Picker title={t.entity} selected={filters.entity} options={[]} onToggle={toggle("entity")} searchPlaceholder={t.searchEntities} remote={{ search: searchEntities }} />
        {hasFilters(filters) && (
          <Button variant="ghost" size="sm" onClick={() => onChange(noFilters)}>
            {t.clearFilters}
          </Button>
        )}
      </div>
      {hasFilters(filters) && (
        <div className="flex flex-wrap gap-1.5">
          {filters.kind.map((v) => <Tag key={`k${v}`} what={t.kind} label={changeKindLabel(v, lang)} onRemove={() => toggle("kind")(v)} />)}
          {filters.op.map((v) => <Tag key={`o${v}`} what={t.op} label={opWord(v)} onRemove={() => toggle("op")(v)} />)}
          {filters.producer.map((v) => <Tag key={`p${v}`} what={t.producer} label={<span className="font-mono">{v}</span>} onRemove={() => toggle("producer")(v)} />)}
          {filters.entity.map((v) => <Tag key={`e${v}`} what={t.entity} label={nameOf(v)} onRemove={() => toggle("entity")(v)} />)}
        </div>
      )}
    </div>
  );
}
