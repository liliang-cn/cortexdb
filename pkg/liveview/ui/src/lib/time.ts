import { localeOf, type Lang } from "@/lib/i18n";

// Every time the app shows is in its language: "2 min ago" or "2分钟前", a
// date the way that language writes one.

const rtfs = new Map<Lang, Intl.RelativeTimeFormat>();
function rtf(lang: Lang): Intl.RelativeTimeFormat {
  let f = rtfs.get(lang);
  if (!f) {
    f = new Intl.RelativeTimeFormat(localeOf(lang), { numeric: "auto", style: "short" });
    rtfs.set(lang, f);
  }
  return f;
}

/** "3 s ago", "2 min ago", "yesterday" (or 刚刚, 2分钟前, 昨天); the date after a week. */
export function formatRelative(lang: Lang, ms: number, now: number = Date.now()): string {
  if (!Number.isFinite(ms)) return "";
  const diff = Math.round((ms - now) / 1000);
  const abs = Math.abs(diff);
  if (abs < 5) return lang === "zh" ? "刚刚" : "just now";
  if (abs < 60) return rtf(lang).format(diff, "second");
  if (abs < 3600) return rtf(lang).format(Math.round(diff / 60), "minute");
  if (abs < 86400) return rtf(lang).format(Math.round(diff / 3600), "hour");
  if (abs < 7 * 86400) return rtf(lang).format(Math.round(diff / 86400), "day");
  return formatDate(lang, ms);
}

export function formatDate(lang: Lang, ms: number): string {
  return new Date(ms).toLocaleDateString(localeOf(lang), { year: "numeric", month: "short", day: "numeric" });
}

/** Local date and time, to the minute. */
export function formatClock(lang: Lang, ms: number): string {
  return new Date(ms).toLocaleString(localeOf(lang), {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** Local time with seconds, for event rows. */
export function formatTime(lang: Lang, ms: number): string {
  return new Date(ms).toLocaleTimeString(localeOf(lang), { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

/** The full local date and time, for a title or a detail row. */
export function formatDateTime(lang: Lang, ms: number): string {
  return Number.isFinite(ms) ? new Date(ms).toLocaleString(localeOf(lang), { dateStyle: "medium", timeStyle: "medium" }) : "";
}

/** A count in the language's digits grouping. */
export function formatNumber(lang: Lang, n: number): string {
  return Number(n || 0).toLocaleString(localeOf(lang));
}
