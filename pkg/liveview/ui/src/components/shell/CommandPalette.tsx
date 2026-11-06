import { useEffect, useState } from "react";
import { Search } from "lucide-react";
import { useNavigate } from "react-router";
import { api, isAbort, type FindResult } from "@/api";
import { entityPath } from "@/components/feed/EntityLink";
import { Button } from "@/components/ui/button";
import { Command, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { APP_NAV } from "./nav";
import { PALETTE_EVENT, openPalette } from "./palette";

/** The top bar's button; the shortcut itself lives in CommandPalette. */
export function PaletteButton() {
  const mac = typeof navigator !== "undefined" && /mac/i.test(navigator.platform);
  return (
    <Button variant="outline" size="sm" className="gap-2 text-muted-foreground" onClick={() => openPalette()} aria-label="Search entities and pages">
      <Search aria-hidden className="size-3.5" />
      <span className="hidden sm:inline">Search</span>
      <kbd className="hidden rounded border bg-muted px-1 font-mono text-[0.68rem] sm:inline">{mac ? "⌘K" : "Ctrl K"}</kbd>
    </Button>
  );
}

/** Jump to an entity or a page: Cmd-K or Ctrl-K from anywhere. */
export function CommandPalette() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<FindResult[]>([]);
  const [searching, setSearching] = useState(false);
  const navigate = useNavigate();

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((o) => !o);
      }
    };
    const onOpen = (e: Event) => {
      setQuery((e as CustomEvent<string>).detail ?? "");
      setOpen(true);
    };
    window.addEventListener("keydown", onKey);
    window.addEventListener(PALETTE_EVENT, onOpen);
    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener(PALETTE_EVENT, onOpen);
    };
  }, []);

  useEffect(() => {
    const q = query.trim();
    if (!open || q === "") {
      setResults([]);
      setSearching(false);
      return;
    }
    setSearching(true);
    const ctl = new AbortController();
    const t = setTimeout(() => {
      api
        .find(q, undefined, { signal: ctl.signal })
        .then((r) => {
          setResults(r.results ?? []);
          setSearching(false);
        })
        .catch((err: unknown) => {
          if (isAbort(err)) return;
          setResults([]);
          setSearching(false);
        });
    }, 180);
    return () => {
      clearTimeout(t);
      ctl.abort();
    };
  }, [query, open]);

  const close = (o: boolean) => {
    setOpen(o);
    if (!o) setQuery("");
  };
  const go = (path: string) => {
    close(false);
    void navigate(path);
  };

  const q = query.trim().toLowerCase();
  const pages = APP_NAV.filter((p) => q === "" || p.label.toLowerCase().includes(q));

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogHeader className="sr-only">
        <DialogTitle>Search</DialogTitle>
        <DialogDescription>Jump to an entity or a page</DialogDescription>
      </DialogHeader>
      <DialogContent className="top-[20%] translate-y-0 gap-0 overflow-hidden p-0 sm:max-w-lg" showCloseButton={false}>
        {/* Entities come from the server already matched, so cmdk must not filter again. */}
        <Command shouldFilter={false}>
      <CommandInput value={query} onValueChange={setQuery} placeholder="Find an entity or go to a page" />
      <CommandList>
        {!searching && results.length === 0 && pages.length === 0 && (
          <div className="px-3 py-6 text-center text-sm text-muted-foreground">Nothing matches that.</div>
        )}
        {results.length > 0 && (
          <CommandGroup heading="Entities">
            {results.slice(0, 12).map((r) => (
              <CommandItem key={r.id} value={`entity ${r.id}`} onSelect={() => go(entityPath(r.id))}>
                <span className="truncate font-medium">{r.label}</span>
                <span className="ml-auto shrink-0 text-xs text-muted-foreground">{r.type}</span>
              </CommandItem>
            ))}
          </CommandGroup>
        )}
        {searching && results.length === 0 && <div className="px-3 py-3 text-sm text-muted-foreground">Searching...</div>}
        {pages.length > 0 && (
          <CommandGroup heading="Pages">
            {pages.map(({ to, label, icon: Icon }) => (
              <CommandItem key={to} value={`page ${label}`} onSelect={() => go(to)}>
                <Icon aria-hidden /> {label}
              </CommandItem>
            ))}
          </CommandGroup>
        )}
      </CommandList>
        </Command>
      </DialogContent>
    </Dialog>
  );
}
