import { createContext, createElement, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { useSearchParams } from "react-router";

// The whole app speaks one language, chosen in one place: LangProvider, which
// the app shell mounts above every page. ?lang= wins when it is given, then
// the choice the viewer made with the top bar's toggle (kept in this
// browser's storage), then the browser's own language. Chinese in any region
// is zh; everything else reads English.

export type Lang = "en" | "zh";

/** Each language's copy of an area's strings. */
export type Strings<T> = Record<Lang, T>;

/** Where the viewer's choice is kept, beside the theme's (lib/theme.ts). */
export const LANG_KEY = "cortexdb.liveview.lang";

/** The query parameter that picks the language for one link. */
export const LANG_PARAM = "lang";

export const LANGS: readonly Lang[] = ["en", "zh"];

export const isLang = (v: unknown): v is Lang => v === "en" || v === "zh";

/** The BCP 47 tag for a language: the document's lang and Intl's locale. */
export const localeOf = (lang: Lang): string => (lang === "zh" ? "zh-CN" : "en");

/** zh for any Chinese tag, en for anything else, en when there is none. */
export function pickLang(param: string | null | undefined, browser: string | null | undefined): Lang {
  return (param || browser || "en").toLowerCase().startsWith("zh") ? "zh" : "en";
}

/**
 * The language to show: ?lang= when it says anything, then the saved choice,
 * then the browser's.
 */
export function resolveLang(
  param: string | null | undefined,
  saved: Lang | null | undefined,
  browser: string | null | undefined,
): Lang {
  if (param) return pickLang(param, null);
  if (saved) return saved;
  return pickLang(null, browser);
}

interface KeyStore {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

function defaultStore(): KeyStore | undefined {
  // Reading the property itself throws where storage is blocked.
  try {
    return typeof localStorage === "undefined" ? undefined : localStorage;
  } catch {
    return undefined;
  }
}

/** The viewer's saved choice, or null when there is none or storage is blocked. */
export function readSavedLang(store: KeyStore | undefined = defaultStore()): Lang | null {
  try {
    const v = store?.getItem(LANG_KEY);
    return isLang(v) ? v : null;
  } catch {
    return null;
  }
}

/** Keep the choice; where storage is blocked it holds for this load only. */
export function saveLang(lang: Lang, store: KeyStore | undefined = defaultStore()): void {
  try {
    store?.setItem(LANG_KEY, lang);
  } catch {
    // not persisted
  }
}

/**
 * The language a ?lang= link asks to be remembered as the viewer's choice, or
 * null when it asks nothing new. A link is how the viewer chose: the pages it
 * leads to drop the parameter, and without this the next click would fall
 * back to the browser's language mid-visit.
 */
export function adoptedLang(param: string | null | undefined, saved: Lang | null | undefined): Lang | null {
  if (!param) return null;
  const asked = pickLang(param, null);
  return asked === saved ? null : asked;
}

const browserLang = () => (typeof navigator === "undefined" ? undefined : navigator.language);

interface LangState {
  lang: Lang;
  setLang: (next: Lang) => void;
}

const LangContext = createContext<LangState | null>(null);

/**
 * LangProvider holds the app's language and marks the document with it, so a
 * screen reader and the browser's hyphenation read the text as what it is.
 * Choosing a language saves it and drops ?lang= from the address, or the
 * parameter would keep overriding the choice. A ?lang= link is a choice too,
 * and is kept the same way, so it holds on every page the visit goes to.
 */
export function LangProvider({ children }: { children: ReactNode }) {
  const [params, setParams] = useSearchParams();
  const [saved, setSaved] = useState<Lang | null>(() => readSavedLang());
  const param = params.get(LANG_PARAM);
  const lang = resolveLang(param, saved, browserLang());

  useEffect(() => {
    document.documentElement.lang = localeOf(lang);
  }, [lang]);

  useEffect(() => {
    const adopted = adoptedLang(param, saved);
    if (adopted) {
      saveLang(adopted);
      setSaved(adopted);
    }
  }, [param, saved]);

  const setLang = useCallback(
    (next: Lang) => {
      saveLang(next);
      setSaved(next);
      if (param !== null) {
        setParams(
          (p) => {
            const q = new URLSearchParams(p);
            q.delete(LANG_PARAM);
            return q;
          },
          { replace: true },
        );
      }
    },
    [param, setParams],
  );

  const value = useMemo(() => ({ lang, setLang }), [lang, setLang]);
  return createElement(LangContext.Provider, { value }, children);
}

/** The app's language. Outside a provider (a test, a stray root) it is the browser's. */
export function useLang(): Lang {
  return useContext(LangContext)?.lang ?? pickLang(null, browserLang());
}

/** The setter the language toggle calls. */
export function useSetLang(): (next: Lang) => void {
  const ctx = useContext(LangContext);
  return ctx ? ctx.setLang : noop;
}

const noop = () => {};

/** An area's strings in the app's language. */
export function useText<T>(strings: Strings<T>): T {
  return strings[useLang()];
}
