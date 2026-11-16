import { Empty } from "@/components/Page";
import { isLogOff } from "@/lib/recalls";
import type { RecallsText } from "@/lib/recalls-strings";
import type { Failure } from "@/lib/useRecalls";

/** The log is opt-in on the brain; a brain without it says so, not an empty timeline. */
export function LogOff({ t }: { t: RecallsText }) {
  return <Empty title={t.offTitle} />;
}

/**
 * Whatever went wrong reading the log, in the page's words: off, not
 * readable from this kind of source, or the server's own sentence.
 */
export function ReadFailure({ error, title, t, onRetry }: { error: Failure; title: string; t: RecallsText; onRetry: () => void }) {
  if (isLogOff(error.status)) return <LogOff t={t} />;
  // 501 is the server saying this source has no tools to run (errNotExplorable).
  if (error.status === 501) return <Empty title={t.noSourceTitle} />;
  return (
    <Empty title={title}>
      <span className="[overflow-wrap:anywhere]">{error.message}</span>{" "}
      <button type="button" className="text-primary underline underline-offset-4" onClick={onRetry}>
        {t.retry}
      </button>
    </Empty>
  );
}
