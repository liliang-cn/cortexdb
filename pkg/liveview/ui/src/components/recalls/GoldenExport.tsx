import { useEffect, useState, type MouseEvent } from "react";
import { Download, Loader2 } from "lucide-react";
import { ApiError, apiUrl } from "@/api";
import { GOLDEN_PATH, recallsApi } from "@/api/recalls";
import { Button } from "@/components/ui/button";
import { countCases, goldenFileName, isLogOff } from "@/lib/recalls";
import type { RecallsText } from "@/lib/recalls-strings";
import { cn } from "@/lib/utils";

type State = { kind: "idle" } | { kind: "busy" } | { kind: "said"; text: string; bad?: boolean };

/**
 * The marks as recall-eval golden cases. The link is the real download, so
 * it can be copied or opened on its own; a click reads it here first, so an
 * export with no marks, or a log that is off, is said on the page instead of
 * saving an empty file or an error, and the file is named for the brain and
 * the day.
 */
export function GoldenExport({ source, t }: { source: string; t: RecallsText }) {
  const [state, setState] = useState<State>({ kind: "idle" });

  useEffect(() => {
    if (state.kind !== "said") return;
    const timer = setTimeout(() => setState({ kind: "idle" }), 6000);
    return () => clearTimeout(timer);
  }, [state]);

  const onClick = async (e: MouseEvent<HTMLAnchorElement>) => {
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button !== 0) return;
    e.preventDefault();
    if (state.kind === "busy") return;
    setState({ kind: "busy" });
    try {
      const text = await recallsApi.golden();
      const n = countCases(text);
      if (n === 0) {
        setState({ kind: "said", text: t.goldenNone });
        return;
      }
      const url = URL.createObjectURL(new Blob([text], { type: "application/x-ndjson" }));
      const a = document.createElement("a");
      a.href = url;
      a.download = goldenFileName(source, new Date());
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      setState({ kind: "said", text: t.goldenDone(n) });
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      const off = isLogOff(err instanceof ApiError ? err.status : null);
      setState({ kind: "said", text: off ? t.offTitle : t.goldenFailed(message), bad: true });
    }
  };

  return (
    <span className="inline-flex flex-wrap items-center gap-2">
      <Button asChild variant="outline" size="sm">
        <a href={apiUrl(GOLDEN_PATH).href} download onClick={onClick} title={t.goldenTitle} aria-busy={state.kind === "busy" || undefined}>
          {state.kind === "busy" ? <Loader2 aria-hidden className="animate-spin" /> : <Download aria-hidden />}
          {t.golden}
        </a>
      </Button>
      <span role="status" className={cn("text-xs", state.kind === "said" && state.bad ? "text-destructive" : "text-muted-foreground")}>
        {state.kind === "said" ? state.text : ""}
      </span>
    </span>
  );
}
