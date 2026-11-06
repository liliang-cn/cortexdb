// What the store says about one record: where it came from, how it is known, who decided what.
// Every field that names another record is a link, because the reason to read
// provenance is almost never one field: it is to walk from a fact to what
// disagrees with it, or to the decision that stands against it.

import { useEffect, useState, type ReactNode } from "react";
import { api, isAbort } from "@/api";
import type { RecordDecision, RecordDetail } from "@/api";
import { GradeChip } from "@/components/semantic";
import { cn } from "@/lib/utils";
import { selectNode } from "./actions";
import { Hint } from "./primitives";

export function useRecord(id: string | null, enabled: boolean): { rep: RecordDetail | null; error: string; loading: boolean } {
  const [state, setState] = useState<{ id: string | null; rep: RecordDetail | null; error: string }>({ id: null, rep: null, error: "" });
  useEffect(() => {
    if (!id || !enabled) return;
    const ctl = new AbortController();
    api
      .record(id, { signal: ctl.signal })
      .then((rep) => setState({ id, rep, error: "" }))
      .catch((err) => {
        if (!isAbort(err)) setState({ id, rep: null, error: err instanceof Error ? err.message : String(err) });
      });
    return () => ctl.abort();
  }, [id, enabled]);
  // One fetch per selection, cancelled by the next: a stale answer never shows under another record.
  const current = state.id === id;
  return { rep: current ? state.rep : null, error: current ? state.error : "", loading: !!id && enabled && !(current && (state.rep || state.error)) };
}

export function IdLink({ id, label }: { id: string; label?: string }) {
  return (
    <button type="button" onClick={() => selectNode(id)} title={id} className="max-w-full truncate text-left font-mono text-xs text-primary underline decoration-primary/30 underline-offset-2 hover:decoration-primary">
      {label ?? id}
    </button>
  );
}

function Field({ k, children }: { k: string; children?: ReactNode }) {
  if (children === undefined || children === null || children === "" || children === false) return null;
  return (
    <div className="grid grid-cols-[5.5rem_1fr] gap-2 py-0.5 text-[13px]">
      <dt className="text-xs leading-5 text-muted-foreground">{k}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </div>
  );
}

function Quote({ children, meta }: { children: ReactNode; meta?: string }) {
  return (
    <blockquote className="mt-1 max-h-40 overflow-auto whitespace-pre-wrap break-words rounded-md border-l-2 bg-muted/60 px-2.5 py-1.5 font-mono text-xs leading-relaxed">
      {meta && <span className="mb-0.5 block text-[10px] text-muted-foreground">{meta}</span>}
      {children}
    </blockquote>
  );
}

function Decision({ d }: { d: RecordDecision }) {
  const meta = [d.actor, d.kind, d.at, d.grade].filter(Boolean).join(" · ");
  return (
    <li className="rounded-lg border p-2 text-xs">
      <div className="flex items-center gap-2">
        {d.verdict && <span className="rounded-full border px-1.5 text-[11px]">{d.verdict}</span>}
        <IdLink id={d.id} />
      </div>
      {d.note && <p className="mt-1 text-muted-foreground">{d.note.length > 180 ? `${d.note.slice(0, 180)}…` : d.note}</p>}
      {meta && <p className="mt-0.5 text-[11px] text-muted-foreground">{meta}</p>}
      {(d.supersedes ?? []).length > 0 && (
        <p className="mt-0.5 flex flex-wrap items-center gap-1 text-[11px] text-muted-foreground">
          supersedes {d.supersedes?.map((x) => <IdLink key={x} id={x} />)}
        </p>
      )}
    </li>
  );
}

/** The little Markdown memories are written in: headings, bullets, bold, inline code, nothing else. */
function Memo({ text }: { text: string }) {
  const inline = (t: string, k: number) =>
    t.split(/(`[^`]+`|\*\*[^*]+\*\*)/g).map((part, i) =>
      part.startsWith("`") && part.endsWith("`") && part.length > 2 ? <code key={`${k}-${i}`} className="rounded bg-muted px-1">{part.slice(1, -1)}</code>
      : part.startsWith("**") && part.endsWith("**") && part.length > 4 ? <b key={`${k}-${i}`}>{part.slice(2, -2)}</b>
      : part,
    );
  return (
    <div className="space-y-0.5 text-[13px] leading-relaxed">
      {text.split("\n").map((line, i) => {
        const h = /^(#{1,4})\s+(.*)$/.exec(line);
        const li = /^\s*[-*]\s+(.*)$/.exec(line);
        if (h) return <div key={i} className="pt-1 font-semibold">{inline(h[2] ?? "", i)}</div>;
        if (li) return <div key={i} className="pl-3 -indent-3 before:mr-1.5 before:content-['•']">{inline(li[1] ?? "", i)}</div>;
        return line.trim() ? <div key={i}>{inline(line, i)}</div> : <div key={i} className="h-1.5" />;
      })}
    </div>
  );
}

export function RecordView({ rep, error, loading, available }: { rep: RecordDetail | null; error: string; loading: boolean; available: boolean }) {
  if (!available) return <Hint>This view's source cannot look a record up, so there is nothing to show about where this came from. That is not the same as a record that carries nothing.</Hint>;
  if (loading) return <Hint>Reading the record...</Hint>;
  if (error) return <Hint tone="bad">{error}</Hint>;
  if (!rep) return null;
  if (!rep.available) return <Hint>This view cannot look the record up.{rep.reason ? ` ${rep.reason}` : ""}</Hint>;
  if (!rep.found) return <Hint>The source has no record with this id any more: it was on screen and is not on the shelf.</Hint>;

  const g = rep.grade ?? "";
  const m = rep.memory;
  return (
    <div className="text-sm">
      {m && (
        <section className="mb-3">
          <div className="rounded-lg border bg-muted/40 p-2.5"><Memo text={m.content} /></div>
          <dl className="mt-1.5">
            <Field k="Saved">{m.created_at ? m.created_at.replace("T", " ").replace("Z", " UTC") : undefined}</Field>
            <Field k="Scope">{[m.scope, m.namespace && m.namespace !== "default" ? m.namespace : "", m.user_id].filter(Boolean).join(" · ")}</Field>
            <Field k="Importance">{m.importance ? String(m.importance) : undefined}</Field>
            {(m.facts ?? []).map((f) => <Field key={f.key} k={f.key.replaceAll("_", " ")}>{f.value}</Field>)}
          </dl>
        </section>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <GradeChip value={g} />
        {!g && <span className="text-xs text-muted-foreground">Nothing on this record says how it is known.</span>}
      </div>
      {rep.why ? (
        <p className="mt-1.5 text-[13px] leading-relaxed text-muted-foreground">{rep.why}</p>
      ) : (
        (g === "held" || g === "refused") && <p className="mt-1.5 text-xs text-destructive">No reason given: the contract requires one for {g}.</p>
      )}
      <dl className="mt-2">
        <Field k="State">{rep.state}</Field>
        <Field k="Source">{rep.source}</Field>
        <Field k="Chunk">{rep.chunk}</Field>
        <Field k="Producer">{rep.producer}</Field>
        <Field k="Produced at">{rep.at}</Field>
        <Field k="Asserted by">{rep.by}</Field>
        <Field k="Confidence">{rep.confidence}</Field>
        <Field k="Valid from">{rep.valid_from}</Field>
        {rep.edge && <Field k="From">{rep.from ? <IdLink id={rep.from} /> : undefined}</Field>}
        {rep.edge && <Field k="To">{rep.to ? <IdLink id={rep.to} /> : undefined}</Field>}
        {rep.inferred && <Field k="Inferred by">{rep.rule || "(unnamed rule)"}</Field>}
        <Field k="Document">{rep.document_id}</Field>
      </dl>

      {(rep.contradicts ?? []).length > 0 && (
        <Block title="Contradicts">
          <div className="flex flex-wrap gap-x-2 gap-y-1">{rep.contradicts?.map((x) => <IdLink key={x} id={x} />)}</div>
        </Block>
      )}
      {(rep.text ?? []).length > 0 ? (
        <Block title="Drawn from">
          {rep.text?.map((t) => <Quote key={t.chunk_id} meta={[t.chunk_id, t.document_id].filter(Boolean).join(" · ")}>{t.content}</Quote>)}
          {(rep.chunk_ids ?? []).length > (rep.text?.length ?? 0) && <p className="mt-1"><Hint>Showing {rep.text?.length} of {rep.chunk_ids?.length} chunks.</Hint></p>}
        </Block>
      ) : (rep.chunk_ids ?? []).length > 0 ? (
        <Block title="Drawn from"><Hint>{rep.chunk_ids?.length} chunks cited, none of them loaded.</Hint></Block>
      ) : null}
      {(rep.missing_chunks ?? []).length > 0 && (
        <p className="mt-2 text-xs text-destructive">{rep.missing_chunks?.length} cited chunk(s) no longer exist: {rep.missing_chunks?.join(", ")}</p>
      )}
      {(rep.decisions ?? []).length > 0 && <Block title="Decided"><ul className="space-y-1.5">{rep.decisions?.map((d) => <Decision key={d.id} d={d} />)}</ul></Block>}
      {(rep.chain ?? []).length > 0 && (
        <Block title="Rests on">
          <ul className="space-y-1.5">{rep.chain?.map((d) => <Decision key={d.id} d={d} />)}</ul>
          {rep.chain_truncated && <p className="mt-1"><Hint>The chain goes further than this: it was cut, not finished.</Hint></p>}
        </Block>
      )}
      {rep.grade === "verified" && !(rep.decisions ?? []).length && !rep.edge && rep.type !== "Decision" && (
        <p className="mt-3"><Hint>Graded verified, and no decision in the ledger names it.</Hint></p>
      )}
      {(rep.notes ?? []).map((n) => <p key={n} className={cn("mt-1")}><Hint>{n}</Hint></p>)}
    </div>
  );
}

function Block({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="mt-3 border-t pt-2.5">
      <h4 className="mb-1.5 text-xs font-medium text-muted-foreground">{title}</h4>
      {children}
    </section>
  );
}
