import { useEffect } from "react";
import { useSearchParams } from "react-router";

// The pages that speak two languages (Recalls, Library) pick one the way the
// pages they replaced did: ?lang= when it is given, otherwise the browser's.
// Chinese in any region is zh; everything else reads English.

export type Lang = "en" | "zh";

/** Each language's copy of a page's strings. */
export type Strings<T> = Record<Lang, T>;

export function pickLang(param: string | null | undefined, browser: string | null | undefined): Lang {
  return (param || browser || "en").toLowerCase().startsWith("zh") ? "zh" : "en";
}

/**
 * useLang is the language for a page that has both, and marks the document
 * with it while that page is open, so a screen reader and the browser's
 * hyphenation read the text as what it is.
 */
export function useLang(): Lang {
  const [params] = useSearchParams();
  const lang = pickLang(params.get("lang"), typeof navigator === "undefined" ? undefined : navigator.language);
  useEffect(() => {
    const root = document.documentElement;
    const before = root.lang;
    root.lang = lang === "zh" ? "zh-CN" : "en";
    return () => {
      root.lang = before;
    };
  }, [lang]);
  return lang;
}
