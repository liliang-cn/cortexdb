import { useEffect, useRef, useState } from "react";
import { ArrowUpRight, CornerDownLeft, Search, X } from "lucide-react";
import type { AskAnswer, CypherAnswer, FindResult } from "@/api";
import { Button } from "@/components/ui/button";
import { typeColor } from "@/lib/graph/colors";
import { GRAPH_TEXT, type GraphText } from "@/lib/graphStrings";
import { useText } from "@/lib/i18n";
import { GRADES, grade } from "@/lib/semantics";
import { useCapabilities } from "@/live";
import { cn } from "@/lib/utils";
import { bringIn, clearExplore, focusNode, runExplore, setHighlight, shareLink } from "./actions";
import { getScene } from "./controller";
import { useDark, useMerged } from "./hooks";
import { Hint, Segmented } from "./primitives";
import { useGraphUI, type ExploreMode } from "./state";

// The Cypher example is code, the same in either language.
const CYPHER_EXAMPLE = "MATCH (a)-[r]->(b) RETURN a, r, b LIMIT 25";

const placeholder = (t: GraphText): Record<ExploreMode, string> => ({
  find: t.findPlaceholder,
  ask: t.askPlaceholder,
  cypher: CYPHER_EXAMPLE,
});

const field =
  "w-full min-w-0 rounded-lg border border-input bg-background/80 px-3 text-base shadow-xs outline-none transition-[color,box-shadow] placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 md:text-sm";

export function ExploreBar({ compact }: { compact?: boolean }) {
  const mode = useGraphUI((s) => s.mode);
  const set = useGraphUI((s) => s.set);
  const lastQuery = useGraphUI((s) => s.lastQuery);
  const focusSearch = useGraphUI((s) => s.focusSearch);
  const loading = useGraphUI((s) => s.result.status === "loading");
  const hasResult = useGraphUI((s) => s.result.status !== "idle");
  const caps = useCapabilities();
  const [text, setText] = useState(() => lastQuery?.text ?? "");
  const inputRef = useRef<HTMLInputElement>(null);
  const areaRef = useRef<HTMLTextAreaElement>(null);
  const t = useText(GRAPH_TEXT);

  // A query run from outside (a link, a message from the page that frames this one) fills the box.
  useEffect(() => {
    if (lastQuery) setText(lastQuery.text);
  }, [lastQuery]);
  useEffect(() => {
    if (focusSearch > 0) (mode === "cypher" ? areaRef.current : inputRef.current)?.focus();
  }, [focusSearch, mode]);

  const submit = () => void runExplore(mode, text);
  const disabled = !caps.explore;

  return (
    <div className="flex flex-col gap-2 p-2">
      {!compact && (
        <div className="flex items-center gap-2">
          <Segmented<ExploreMode>
            value={mode}
            onChange={(m) => set({ mode: m })}
            options={[
              { value: "find", label: t.find, title: t.findTitle },
              { value: "ask", label: t.ask, title: t.askTitle },
              { value: "cypher", label: t.cypher, title: t.cypherTitle },
            ]}
          />
          {hasResult && (
            <Button variant="ghost" size="icon" className="ml-auto size-7" onClick={() => { clearExplore(); setText(""); }} aria-label={t.clearSearch}>
              <X className="size-4" />
            </Button>
          )}
        </div>
      )}
      <form
        className="flex items-start gap-1.5"
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        {mode === "cypher" && !compact ? (
          <textarea
            ref={areaRef}
            value={text}
            rows={3}
            disabled={disabled}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                e.preventDefault();
                submit();
              }
            }}
            placeholder={CYPHER_EXAMPLE}
            aria-label={t.cypherLabel}
            className={cn(field, "resize-y py-2 font-mono text-xs leading-relaxed md:text-xs")}
          />
        ) : (
          <div className="relative min-w-0 flex-1">
            <Search aria-hidden className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <input
              ref={inputRef}
              value={text}
              disabled={disabled}
              onChange={(e) => setText(e.target.value)}
              placeholder={disabled ? t.cannotExplore : placeholder(t)[mode]}
              aria-label={mode === "ask" ? t.askLabel : t.findLabel}
              enterKeyHint="search"
              autoComplete="off"
              className={cn(field, "h-9 pl-8")}
            />
          </div>
        )}
        <Button type="submit" size="sm" className="h-9 shrink-0 gap-1.5 px-3" disabled={disabled || loading || !text.trim()}>
          {mode === "cypher" && !compact ? t.run : <CornerDownLeft className="size-4" aria-label={t.go} />}
        </Button>
      </form>
      {compact && (
        <Segmented<ExploreMode>
          size="sm"
          value={mode}
          onChange={(m) => set({ mode: m })}
          options={[{ value: "find", label: t.find }, { value: "ask", label: t.ask }, { value: "cypher", label: t.cypher }]}
        />
      )}
    </div>
  );
}

function Hit({ n, meta }: { n: FindResult; meta?: string }) {
  const g = useMerged();
  const dark = useDark();
  const colorMode = useGraphUI((s) => s.colorMode);
  const t = useText(GRAPH_TEXT);
  const inView = g.nodes.has(n.id);
  const dot = colorMode === "grade" ? undefined : typeColor(n.type, dark);
  return (
    <li>
      <button
        type="button"
        onClick={() => void focusNode(n.id)}
        className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[13px] outline-none hover:bg-muted focus-visible:ring-[3px] focus-visible:ring-ring/50"
      >
        <span className={cn("size-2.5 shrink-0 rounded-full", colorMode === "grade" && GRADES[grade(n.grade).key as keyof typeof GRADES].dot)} style={dot ? { background: dot } : undefined} />
        <span className="min-w-0 flex-1 truncate">{n.label || n.id}</span>
        {!inView && <ArrowUpRight aria-label={t.notInView} className="size-3.5 shrink-0 text-op-superseded" />}
        <span className="shrink-0 text-xs text-muted-foreground">{meta ?? n.type}</span>
      </button>
    </li>
  );
}

function FindResults({ results }: { results: FindResult[] }) {
  const g = useMerged();
  const t = useText(GRAPH_TEXT);
  if (!results.length) return <Hint>{t.findNothing}</Hint>;
  const inView = results.filter((r) => g.nodes.has(r.id)).length;
  return (
    <>
      <p className="px-2 pb-1 text-xs text-muted-foreground">
        {t.found(results.length, inView)}{results.length > inView ? t.arrowMarks : ""}
      </p>
      <ul>{results.map((n) => <Hit key={n.id} n={n} meta={[n.type, n.match && n.match !== "exact" ? n.match : ""].filter(Boolean).join(" · ")} />)}</ul>
    </>
  );
}

function AskResults({ answer }: { answer: AskAnswer }) {
  const g = useMerged();
  const touched = answer.touched ?? [];
  const missing = touched.filter((x) => !g.nodes.has(x));
  const nothing = !answer.facts?.length && !answer.memories?.length && !answer.passages?.length;
  const t = useText(GRAPH_TEXT);
  if (nothing) return <Hint>{t.askNothing}</Hint>;
  return (
    <div className="space-y-3 px-1">
      <p className="text-xs text-muted-foreground">
        {t.named(touched.length, touched.length - missing.length)}
        {missing.length > 0 && (
          <>
            {". "}
            <button type="button" className="text-primary hover:underline" onClick={() => void bringIn(touched)}>{t.bringIn(missing.length)}</button>
          </>
        )}
        {answer.mode ? ` · ${answer.mode}` : ""}
      </p>
      {answer.facts?.length > 0 && (
        <section>
          <h4 className="mb-1 text-xs font-medium text-muted-foreground">{t.facts}</h4>
          {answer.facts.map((f, i) => (
            <button key={i} type="button" onClick={() => f.subject_id && void focusNode(f.subject_id)} className="block w-full rounded-md px-1.5 py-1 text-left text-[13px] hover:bg-muted">
              {f.subject} <span className="text-primary">{f.predicate}</span> {f.object}
            </button>
          ))}
        </section>
      )}
      {answer.memories?.length > 0 && (
        <section>
          <h4 className="mb-1 text-xs font-medium text-muted-foreground">{t.memories}</h4>
          {answer.memories.map((m) => (
            <blockquote key={m.id} className="mb-1.5 border-l-2 border-primary/40 pl-2.5 text-[13px] leading-snug">
              {m.text}
              <span className="mt-0.5 block font-mono text-[10px] text-muted-foreground">{m.id}{m.score ? ` · ${m.score.toFixed(2)}` : ""}</span>
            </blockquote>
          ))}
        </section>
      )}
      {answer.passages?.length > 0 && (
        <section>
          <h4 className="mb-1 text-xs font-medium text-muted-foreground">{t.knowledge}</h4>
          {answer.passages.map((m) => (
            <blockquote key={m.id} className="mb-1.5 border-l-2 border-primary/40 pl-2.5 text-[13px] leading-snug">
              {m.title && <b className="font-semibold">{m.title} </b>}
              {m.text}
              <span className="mt-0.5 block font-mono text-[10px] text-muted-foreground">{m.id}</span>
            </blockquote>
          ))}
        </section>
      )}
    </div>
  );
}

function CypherResults({ answer, fresh }: { answer: CypherAnswer; fresh: number }) {
  const cols = answer.columns ?? [];
  const rows = answer.rows ?? [];
  const n = answer.row_count || rows.length;
  const t = useText(GRAPH_TEXT);
  return (
    <>
      <p className="px-1 pb-1.5 text-xs text-muted-foreground">
        {t.rows(n)}
        {answer.truncated ? t.truncated : ""} · {t.nodesN((answer.nodes ?? []).length)}{fresh ? t.broughtIn(fresh) : ""}
      </p>
      {cols.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse font-mono text-[11px]">
            <thead>
              <tr>{cols.map((c) => <th key={c} className="sticky top-0 border-b bg-card px-1.5 py-1 text-left font-medium text-muted-foreground">{c}</th>)}</tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr key={i}>{r.map((c, j) => <td key={j} className="max-w-48 break-words border-b px-1.5 py-1 align-top">{c}</td>)}</tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

export function ResultsContent() {
  const result = useGraphUI((s) => s.result);
  const lit = useGraphUI((s) => s.highlight.size);
  const lastQuery = useGraphUI((s) => s.lastQuery);
  const highlight = useGraphUI((s) => s.highlight);
  const set = useGraphUI((s) => s.set);
  const t = useText(GRAPH_TEXT);
  if (result.status === "idle") return null;
  return (
    <>
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-2 py-2">
        {result.status === "loading" && <Hint>{result.mode === "ask" ? t.asking : result.mode === "cypher" ? t.running : t.looking}</Hint>}
        {result.status === "error" && <Hint tone="bad">{result.message}</Hint>}
        {result.status === "find" && <FindResults results={result.results} />}
        {result.status === "ask" && <AskResults answer={result.answer} />}
        {result.status === "cypher" && <CypherResults answer={result.answer} fresh={result.fresh} />}
      </div>
      {lit > 0 && (
        <div className="flex shrink-0 items-center gap-1 border-t px-2 py-1.5 text-xs text-muted-foreground">
          <span className="mr-auto pl-1">{t.lit(lit)}</span>
          <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={() => getScene()?.fit(800, highlight)}>{t.frame}</Button>
          <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={() => setHighlight([], false)}>{t.clear}</Button>
          <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={() => shareLink(lastQuery ? [[lastQuery.mode, lastQuery.text]] : [])}>{t.share}</Button>
          <Button variant="ghost" size="sm" className="h-7 px-2 text-xs md:inline-flex hidden" onClick={() => set({ resultsOpen: false })}>{t.hide}</Button>
        </div>
      )}
    </>
  );
}
