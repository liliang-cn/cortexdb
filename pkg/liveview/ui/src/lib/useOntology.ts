import { useCallback, useEffect, useMemo, useState } from "react";
import { isAbort } from "@/api";
import { fetchOntology, type OntologyAnswer } from "@/api/ontology";
import { useLive } from "@/live";
import { ONTOLOGY_MS, failedAnswer, latestSchemaChange, ontologyQuery, queryKey, type OntologyOptions } from "./ontology";

export interface OntologyData {
  /** The answer to the question the options ask, or null until it arrives. */
  rep: OntologyAnswer | null;
  /** A read is in flight. The answer above stays on screen meanwhile. */
  loading: boolean;
  reload: () => void;
}

/**
 * The ontology, read again every ONTOLOGY_MS while the tab is visible, at once
 * when it comes forward, and at once when the live stream carries a change to
 * a saved schema, so a save shows up without waiting for the poll.
 *
 * Nothing is read while nobody is looking: the overlay is two aggregate scans
 * over the store, and a tab left in the background has no reader to be stale
 * to. An answer is kept with the question it answers, so a switch flipped
 * mid-read never shows the old question's answer under the new switch.
 */
export function useOntology(opts: OntologyOptions): OntologyData {
  const query = useMemo(
    () => ontologyQuery(opts),
    // The query depends on these and nothing else; listing them keeps a new
    // options object with the same values from starting a read.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [opts.draft, opts.gap, opts.schema, opts.min, opts.minNodes, opts.minEdges],
  );
  const key = queryKey(query);
  const draft = opts.draft;
  const [answer, setAnswer] = useState<{ key: string; rep: OntologyAnswer } | null>(null);
  const [loading, setLoading] = useState(false);
  const [nudge, setNudge] = useState(0);
  const schemaSeq = useLive((s) => latestSchemaChange(s.changes));

  useEffect(() => {
    let ctl: AbortController | null = null;
    const load = () => {
      ctl?.abort();
      const mine = new AbortController();
      ctl = mine;
      setLoading(true);
      fetchOntology(query, mine.signal)
        .then((rep) => setAnswer({ key, rep }))
        .catch((err: unknown) => {
          if (isAbort(err)) return;
          setAnswer({ key, rep: failedAnswer(draft, err instanceof Error ? err.message : String(err)) });
        })
        .finally(() => {
          if (ctl === mine) setLoading(false);
        });
    };
    load();
    const timer = setInterval(() => {
      if (!document.hidden) load();
    }, ONTOLOGY_MS);
    const onVisible = () => {
      if (!document.hidden) load();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisible);
      ctl?.abort();
    };
  }, [key, query, draft, schemaSeq, nudge]);

  const reload = useCallback(() => setNudge((n) => n + 1), []);
  return { rep: answer && answer.key === key ? answer.rep : null, loading, reload };
}
