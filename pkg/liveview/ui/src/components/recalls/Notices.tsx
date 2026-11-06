import { Empty } from "@/components/Page";
import { isLogOff } from "@/lib/recalls";
import { splitSlots, type RecallsText } from "@/lib/recalls-strings";
import type { Failure } from "@/lib/useRecalls";

const SLOTS: Record<string, string> = { on: "CORTEXDB_RECALL_LOG=1", days: "CORTEXDB_RECALL_LOG_DAYS" };

/** The log is opt-in on the brain; a brain without it is told how to turn it on, not shown an empty timeline. */
export function LogOff({ t }: { t: RecallsText }) {
  return (
    <Empty title={t.offTitle}>
      {splitSlots(t.offHow).map((part, i) =>
        typeof part === "string" ? (
          <span key={i}>{part}</span>
        ) : (
          <code key={i} className="rounded bg-muted px-1.5 py-0.5 font-mono text-[0.8rem] text-foreground">
            {SLOTS[part.slot] ?? part.slot}
          </code>
        ),
      )}
    </Empty>
  );
}

/**
 * Whatever went wrong reading the log, in the page's words: off, not
 * readable from this kind of source, or the server's own sentence.
 */
export function ReadFailure({ error, title, t, onRetry }: { error: Failure; title: string; t: RecallsText; onRetry: () => void }) {
  if (isLogOff(error.message)) return <LogOff t={t} />;
  // 501 is the server saying this source has no tools to run (errNotExplorable).
  if (error.status === 501) return <Empty title={t.noSourceTitle}>{t.noSourceBody}</Empty>;
  return (
    <Empty title={title}>
      <span className="[overflow-wrap:anywhere]">{error.message}</span>{" "}
      <button type="button" className="text-primary underline underline-offset-4" onClick={onRetry}>
        {t.retry}
      </button>
    </Empty>
  );
}
