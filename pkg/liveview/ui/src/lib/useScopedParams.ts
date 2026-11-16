import { useCallback, useMemo } from "react";
import { useSearchParams, type NavigateOptions } from "react-router";
import { scopeParams, unscopeParams } from "@/lib/ontologyTabs";

export type SetScopedParams = (next: URLSearchParams | ((prev: URLSearchParams) => URLSearchParams), opts?: NavigateOptions) => void;

/**
 * useSearchParams for the parameters under `prefix`, read and written without
 * it. A panel that shares its URL with another reads and writes its own
 * parameters as if the URL were its alone; every other parameter stays.
 */
export function useScopedParams(prefix: string): [URLSearchParams, SetScopedParams] {
  const [raw, setRaw] = useSearchParams();
  const params = useMemo(() => scopeParams(raw, prefix), [raw, prefix]);
  const set = useCallback<SetScopedParams>(
    (next, opts) => setRaw((cur) => unscopeParams(typeof next === "function" ? next(scopeParams(cur, prefix)) : next, prefix, cur), opts),
    [setRaw, prefix],
  );
  return [params, set];
}
