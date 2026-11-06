// The library's API: the reading room, an opened index card and an opened
// book. The shapes are the Go types in pkg/liveview/library.go, field for
// field; every path is relative, so the page keeps working when the view is
// mounted under a prefix of another mux.

import { getJSON } from "./client";
import type { RecordDetail } from "./types";

/** One memory on a shelf (LibraryBook). */
export interface LibraryBook {
  id: string;
  label: string;
  /** How many distinct things the memory mentions: its index, and the book's thickness. */
  terms: number;
}

/** One project's books (LibraryShelf). */
export interface LibraryShelf {
  id: string;
  project: string;
  books: LibraryBook[] | null;
}

/** A community of shelves, named after its largest (LibraryWing). */
export interface LibraryWing {
  name: string;
  books: number;
  shelves: LibraryShelf[] | null;
}

/** One document in the reference room (LibraryDoc). */
export interface LibraryDoc {
  id: string;
  title: string;
  chunks: number;
}

/** The whole reading room, largest wing first (Library). */
export interface Library {
  version: number;
  source: string;
  wings: LibraryWing[] | null;
  /** Shelves of wings too small to stand on their own. */
  annex: LibraryShelf[] | null;
  /** Memories that name no project. */
  stacks: LibraryBook[] | null;
  /** How many index cards there are: every node that is not a memory. */
  cards: number;
  docs: LibraryDoc[] | null;
  /** False when the source cannot be asked for documents, which is not the same as having none. */
  docs_known: boolean;
  /** Whether a book can be opened: its text read through the source. */
  records: boolean;
}

/** One relation on a card, from the card's side (LibraryRelation). */
export interface LibraryRelation {
  rel: string;
  id: string;
  label: string;
  type: string;
  /** True when the card is the relation's subject. */
  out: boolean;
}

/** An opened index card: what mentions it, and what it is related to (LibraryCard). */
export interface LibraryCard {
  id: string;
  label: string;
  type: string;
  found: boolean;
  books: string[] | null;
  relations: LibraryRelation[] | null;
  /** How many relations were left out past the cap. */
  more?: number;
}

/** One entry in a book's index (LibraryTerm). */
export interface LibraryTerm {
  id: string;
  label: string;
  type: string;
  project?: boolean;
}

/** Another book sharing index terms with this one (LibrarySeeAlso). */
export interface LibrarySeeAlso {
  id: string;
  label: string;
  shared: number;
}

/**
 * The back of an opened book: its index and what to read next, with its text
 * and provenance through the source (LibraryPage in library.go; renamed here
 * because LibraryPage is the route's component).
 */
export interface LibraryBookPage {
  id: string;
  label: string;
  found: boolean;
  index: LibraryTerm[] | null;
  see_also: LibrarySeeAlso[] | null;
  record: RecordDetail;
}

/** The paths the library reads, relative to the page's <base>. */
export const LIBRARY_PATHS = {
  library: "api/library",
  card: "api/library/card",
  book: "api/library/book",
} as const;

export const libraryApi = {
  library: (signal?: AbortSignal) => getJSON<Library>(LIBRARY_PATHS.library, undefined, signal),
  card: (id: string, signal?: AbortSignal) => getJSON<LibraryCard>(LIBRARY_PATHS.card, { id }, signal),
  book: (id: string, signal?: AbortSignal) => getJSON<LibraryBookPage>(LIBRARY_PATHS.book, { id }, signal),
};
