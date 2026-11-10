// The panels over the reading room. They are the app's own cards, buttons and
// inputs, so the library reads as a page of the same app; only the book's
// text is set in a serif, because it is being read rather than scanned.

import { forwardRef, type ReactNode } from "react";
import { Link, useLocation } from "react-router";
import { ChevronLeft, ChevronRight, Search, X } from "lucide-react";
import type { ActivityEvent, FindResult } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Hint, SectionTitle } from "@/components/graph/primitives";
import { shortId, totals, whereBooks, whereOf, withOpening, type BookSpot, type Layout } from "@/lib/library";
import { bookText, metaLine, provenance } from "@/lib/libraryPages";
import { BOOK_SERIF } from "@/lib/libraryPalette";
import { useLang } from "@/lib/i18n";
import type { LibraryText } from "@/lib/libraryStrings";
import { formatTime } from "@/lib/time";
import { cn } from "@/lib/utils";
import type { Opened } from "./hooks";

/** A link that opens something else in the room and keeps every other option the URL carries. */
export function OpenLink({ kind, id, className, children, title, onClick }: { kind: "book" | "card"; id: string; className?: string; children: ReactNode; title?: string; onClick?: () => void }) {
  const { search } = useLocation();
  return (
    <Link to={{ search: withOpening(search, { kind, id }) }} className={className} title={title} onClick={onClick}>
      {children}
    </Link>
  );
}

const link = "rounded-sm text-foreground underline decoration-border underline-offset-[3px] outline-none hover:decoration-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50";

// ---- the header -----------------------------------------------------------

export function Stats({ layout, t, className }: { layout: Layout; t: LibraryText; className?: string }) {
  const { lib } = layout;
  const n = totals(lib);
  const items: Array<[string, string]> = [
    [n.books.toLocaleString(), t.books],
    [n.shelves.toLocaleString(), t.shelves],
    [lib.cards.toLocaleString(), t.cards],
    [lib.docsKnown ? lib.docs.length.toLocaleString() : "—", t.docs],
  ];
  return (
    <dl className={cn("grid grid-cols-4 gap-2", className)}>
      {items.map(([v, k]) => (
        <div key={k} className="min-w-0 border-t pt-1.5">
          <dt className="sr-only">{k}</dt>
          <dd className="font-mono text-base font-medium tabular-nums leading-tight">{v}</dd>
          <dd aria-hidden className="truncate text-[11px] text-muted-foreground">{k}</dd>
        </div>
      ))}
    </dl>
  );
}

export function Source({ layout, t }: { layout: Layout; t: LibraryText }) {
  const { lib } = layout;
  const line = lib.source + (lib.docsKnown ? "" : ` · ${t.docsOff}`);
  return (
    <p className="truncate font-mono text-[11px] text-muted-foreground" title={line}>
      {line}
    </p>
  );
}

// ---- finding --------------------------------------------------------------

export const FindBox = forwardRef<HTMLInputElement, { value: string; onChange: (v: string) => void; onFocus?: () => void; t: LibraryText; className?: string }>(function FindBox({ value, onChange, onFocus, t, className }, ref) {
  return (
    <div className={cn("relative", className)}>
      <Search aria-hidden className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        ref={ref}
        type="search"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onFocus={onFocus}
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            onChange("");
            e.currentTarget.blur();
          }
        }}
        placeholder={t.find}
        aria-label={t.findLabel}
        autoComplete="off"
        spellCheck={false}
        className="h-9 border-0 bg-transparent pl-8 pr-8 shadow-none focus-visible:ring-0 dark:bg-transparent [&::-webkit-search-cancel-button]:hidden"
      />
      {value && (
        <Button variant="ghost" size="icon" className="absolute right-0.5 top-1/2 size-7 -translate-y-1/2" aria-label={t.clearFind} onClick={() => onChange("")}>
          <X className="size-4" />
        </Button>
      )}
    </div>
  );
});

export interface Found {
  books: BookSpot[];
  cards: FindResult[];
  /** The source cannot be searched for index cards. */
  off: boolean;
  /** The card search has answered for this query. */
  done: boolean;
}

export function FindResults({ found, t, onPick }: { found: Found; t: LibraryText; onPick: () => void }) {
  const row = "block rounded-md px-2 py-1.5 text-sm outline-none hover:bg-accent focus-visible:bg-accent";
  const sub = "block truncate text-xs text-muted-foreground";
  return (
    <div className="min-h-0 overflow-y-auto p-1.5">
      {found.books.length > 0 && (
        <section>
          <h3 className="px-2 pb-1 pt-1.5 text-xs font-medium text-muted-foreground">{t.hBooks}</h3>
          {found.books.map((b) => (
            <OpenLink key={b.id} kind="book" id={b.id} className={row} onClick={onPick}>
              <span className="block truncate">{shortId(b.id)}</span>
              <span className={sub}>{[whereOf(b, t), b.label].filter(Boolean).join(" · ")}</span>
            </OpenLink>
          ))}
        </section>
      )}
      {found.cards.length > 0 && (
        <section>
          <h3 className="px-2 pb-1 pt-1.5 text-xs font-medium text-muted-foreground">{t.hCards}</h3>
          {found.cards.map((c) => (
            <OpenLink key={c.id} kind="card" id={c.id} className={row} onClick={onPick}>
              <span className="block truncate">{c.label || shortId(c.id)}</span>
              <span className={sub}>{c.type}</span>
            </OpenLink>
          ))}
        </section>
      )}
      {found.off && <p className="px-2 py-1.5 text-xs text-muted-foreground">{t.findOff}</p>}
      {found.done && !found.off && !found.books.length && !found.cards.length && <p className="px-2 py-1.5 text-xs text-muted-foreground">{t.noMatch}</p>}
    </div>
  );
}

// ---- an open book ---------------------------------------------------------

export function BookTitle({ id }: { id: string }) {
  return (
    <span className="font-medium" style={{ fontFamily: BOOK_SERIF }}>
      {shortId(id)}
    </span>
  );
}

export function BookBody({ opened, layout, t, records }: { opened: Extract<Opened, { kind: "book" }>; layout: Layout; t: LibraryText; records: boolean }) {
  const { book } = opened;
  const spot = layout.byId.get(book.id);
  const meta = metaLine(book.record, t);
  const prov = provenance(book.record, t);
  const index = book.index ?? [];
  const seeAlso = book.see_also ?? [];
  return (
    <div className="min-h-0 flex-1 overflow-y-auto px-3 pb-4 pt-2">
      <p className="text-xs text-muted-foreground">{[whereOf(spot, t), meta].filter(Boolean).join(" · ")}</p>
      {!book.found && <Hint tone="bad">{t.notFound}</Hint>}
      <SectionTitle>{t.text}</SectionTitle>
      <div className="whitespace-pre-wrap break-words text-[15px] leading-relaxed text-foreground" style={{ fontFamily: BOOK_SERIF }}>
        {records ? bookText(book, t.noRecords) : t.noRecords}
      </div>

      {index.length > 0 && (
        <>
          <SectionTitle>{t.index}</SectionTitle>
          <ul className="flex flex-wrap gap-1.5">
            {index.map((term) => (
              <li key={term.id}>
                <OpenLink
                  kind="card"
                  id={term.id}
                  title={term.type}
                  className={cn(
                    "inline-flex h-6 items-center rounded-full border px-2.5 text-xs outline-none hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring/50",
                    term.project ? "border-foreground/40 font-medium text-foreground" : "text-muted-foreground hover:text-foreground",
                  )}
                >
                  {term.label || shortId(term.id)}
                </OpenLink>
              </li>
            ))}
          </ul>
        </>
      )}

      {seeAlso.length > 0 && (
        <>
          <SectionTitle>{t.seeAlso}</SectionTitle>
          <ul className="space-y-1.5">
            {seeAlso.map((s) => (
              <li key={s.id} className="text-sm">
                <OpenLink kind="book" id={s.id} className={link}>
                  {shortId(s.id)}
                </OpenLink>
                <span className="ml-2 text-xs text-muted-foreground">{[whereOf(layout.byId.get(s.id), t), t.shared(s.shared)].filter(Boolean).join(" · ")}</span>
              </li>
            ))}
          </ul>
        </>
      )}

      {prov.length > 0 && (
        <>
          <SectionTitle>{t.provenance}</SectionTitle>
          <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
            {prov.map(([k, v]) => (
              <div key={k} className="contents">
                <dt className="text-muted-foreground">{k}</dt>
                <dd className="min-w-0 break-words font-mono">{v}</dd>
              </div>
            ))}
          </dl>
        </>
      )}
    </div>
  );
}

/** Page turns for the open book: the lectern shows the page, this turns it. */
export function Pager({ page, pages, onPage, onClose, closeable = true, t, className }: { page: number; pages: number; onPage: (n: number) => void; onClose: () => void; closeable?: boolean; t: LibraryText; className?: string }) {
  return (
    <div className={cn("flex items-center gap-1", className)}>
      <Button variant="ghost" size="icon" className="size-8" aria-label={t.prev} disabled={page <= 0} onClick={() => onPage(page - 1)}>
        <ChevronLeft className="size-4" />
      </Button>
      <span className="min-w-[6.5rem] text-center font-mono text-xs tabular-nums" aria-live="polite">
        {t.page(page + 1, Math.max(1, pages))}
      </span>
      <Button variant="ghost" size="icon" className="size-8" aria-label={t.next} disabled={page >= pages - 1} onClick={() => onPage(page + 1)}>
        <ChevronRight className="size-4" />
      </Button>
      {closeable && (
        <Button variant="outline" size="sm" className="ml-1 h-8 rounded-full" onClick={onClose}>
          {t.close}
        </Button>
      )}
    </div>
  );
}

// ---- an open card ---------------------------------------------------------

export function CardTitle({ opened }: { opened: Extract<Opened, { kind: "card" }> }) {
  return <span>{opened.card.label || shortId(opened.id)}</span>;
}

export function CardBody({ opened, layout, t }: { opened: Extract<Opened, { kind: "card" }>; layout: Layout; t: LibraryText }) {
  const { card } = opened;
  const books = card.books ?? [];
  const rels = card.relations ?? [];
  const where = whereBooks(layout.lib, layout.byId, books, t);
  return (
    <div className="min-h-0 flex-1 overflow-y-auto px-3 pb-4 pt-2">
      <p className="text-xs text-muted-foreground">{[card.type, t.cardBooks(books.length)].filter(Boolean).join(" · ")}</p>
      {!card.found && <Hint tone="bad">{t.notFound}</Hint>}
      <SectionTitle>{t.whereBooks}</SectionTitle>
      <p className="text-sm">{where.length ? where.map(([k, n]) => `${k} ×${n}`).join(" · ") : t.none}</p>
      <SectionTitle aside={<span className="font-mono text-xs tabular-nums text-muted-foreground">{rels.length + (card.more ?? 0)}</span>}>{t.rels}</SectionTitle>
      {rels.length ? (
        <ul className="space-y-1">
          {rels.map((r) => (
            <li key={`${r.rel}\u0000${r.id}\u0000${r.out}`} className="flex min-w-0 items-baseline gap-2 text-sm">
              <code className="w-28 shrink-0 truncate font-mono text-[11px] text-muted-foreground" title={r.rel}>
                {r.out ? r.rel : `← ${r.rel}`}
              </code>
              <OpenLink kind={layout.byId.has(r.id) ? "book" : "card"} id={r.id} className={cn(link, "min-w-0 truncate")} title={r.type}>
                {r.label || shortId(r.id)}
              </OpenLink>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-sm text-muted-foreground">{t.none}</p>
      )}
      {!!card.more && <p className="mt-2 text-xs text-muted-foreground">{t.more(card.more)}</p>}
      {books.length > 0 && (
        <>
          <SectionTitle>{t.hBooks}</SectionTitle>
          <ul className="space-y-1">
            {books.map((id) => (
              <li key={id} className="min-w-0 truncate text-sm">
                <OpenLink kind="book" id={id} className={link}>
                  {shortId(id)}
                </OpenLink>
                <span className="ml-2 text-xs text-muted-foreground">{whereOf(layout.byId.get(id), t)}</span>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

// ---- what is happening ----------------------------------------------------

export function Feed({ events, live, t }: { events: ReadonlyArray<ActivityEvent>; live: boolean; t: LibraryText }) {
  const last = events.slice(0, 4);
  const lang = useLang();
  return (
    <div className="px-3 py-2.5">
      <h2 className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
        <span aria-hidden className={cn("size-1.5 rounded-full", live ? "bg-link-live" : "bg-muted-foreground/50")} />
        {t.feed}
        <span className="sr-only">{live ? t.live : t.notLive}</span>
      </h2>
      <ul className="mt-1.5">
        {last.length ? (
          last.map((e) => (
            <li key={`${e.seq}:${e.at}`} className="flex min-w-0 gap-2 border-t py-1.5 text-[13px] first:border-t-0">
              <span className={cn("min-w-0 flex-1 truncate", e.failed && "text-destructive")}>{e.text || e.tool}</span>
              <time className="shrink-0 font-mono text-[11px] tabular-nums text-muted-foreground" dateTime={new Date(e.at).toISOString()}>
                {formatTime(lang, e.at)}
              </time>
            </li>
          ))
        ) : (
          <li className="py-1.5 text-[13px] text-muted-foreground">{t.quiet}</li>
        )}
      </ul>
    </div>
  );
}
