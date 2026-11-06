// Small fetch helpers. Every URL is built from document.baseURI, never from an
// absolute "/api/..." path, so the view keeps working when it is mounted under
// a prefix of another mux.

/** An answer the server refused, or one that never arrived. */
export class ApiError extends Error {
  readonly status: number;
  constructor(message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

export type Params = Record<string, string | number | boolean | readonly (string | number)[] | undefined | null>;

/** apiUrl resolves `api/...` against the page's <base>. */
export function apiUrl(path: string, params?: Params): URL {
  const url = new URL(path.replace(/^\/+/, ""), document.baseURI);
  for (const [key, value] of Object.entries(params ?? {})) {
    if (value === undefined || value === null || value === "") continue;
    if (Array.isArray(value)) {
      for (const v of value) url.searchParams.append(key, String(v));
    } else {
      url.searchParams.set(key, String(value));
    }
  }
  return url;
}

/** The router basename for the page's <base>: "" at the root, "/prefix" under one. */
export function routerBasename(): string {
  return new URL(document.baseURI).pathname.replace(/\/+$/, "");
}

async function readError(res: Response): Promise<string> {
  const text = await res.text().catch(() => "");
  try {
    const body = JSON.parse(text) as { error?: string };
    if (body.error) return body.error;
  } catch {
    // not JSON; fall through to the status line
  }
  return text.trim().slice(0, 200) || `${res.status} ${res.statusText}`;
}

async function send<T>(url: URL, init: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(url, init);
  } catch (err) {
    if (err instanceof DOMException && err.name === "AbortError") throw err;
    throw new ApiError(err instanceof Error ? err.message : "network error", 0);
  }
  if (!res.ok) throw new ApiError(await readError(res), res.status);
  return (await res.json()) as T;
}

export function getJSON<T>(path: string, params?: Params, signal?: AbortSignal): Promise<T> {
  return send<T>(apiUrl(path, params), { signal, headers: { Accept: "application/json" } });
}

export function postJSON<T>(path: string, body: unknown, signal?: AbortSignal): Promise<T> {
  return send<T>(apiUrl(path), {
    method: "POST",
    signal,
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

export function isAbort(err: unknown): boolean {
  return err instanceof DOMException && err.name === "AbortError";
}
