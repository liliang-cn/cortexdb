import { useEffect, useState } from "react";
import { api, isAbort } from "@/api";
import type { ContractReport, ContractRow } from "@/api";
import { GradeChip } from "@/components/semantic";
import { GRADES, grade } from "@/lib/semantics";
import { useLive } from "@/live";
import { GRAPH_TEXT } from "@/lib/graphStrings";
import { useLang, useText } from "@/lib/i18n";
import { formatNumber } from "@/lib/time";
import { cn } from "@/lib/utils";
import { Hint, SectionTitle } from "./primitives";
import { selectNode } from "./actions";

/** Mirrors liveview.ContractInterval: two aggregate scans, so it is read slowly and only while someone is looking. */
const CONTRACT_MS = 15000;

function rowSemantic(r: ContractRow) {
  if (r.kind === "untagged") return GRADES.untagged;
  if (r.kind === "unknown") return GRADES.unknown;
  return grade(r.grade);
}

function Bar({ rows }: { rows: ContractRow[] }) {
  // Scaled to the largest row, untagged included, so the graded bars read as
  // the fraction of the shelf they actually are, which on a real shelf is a
  // sliver. The floor keeps a row that has some from drawing like one that has
  // none.
  const max = Math.max(1, ...rows.map((r) => r.nodes + r.edges));
  const seg = (v: number) => (v > 0 ? Math.max(0.9, (100 * v) / max) : 0);
  const lang = useLang();
  const t = GRAPH_TEXT[lang];
  const num = (n: number) => formatNumber(lang, n);
  return (
    <div className="space-y-1.5">
      {rows.map((r) => {
        const s = rowSemantic(r);
        const name = r.kind === "untagged" ? t.untaggedRow : r.grade || t.blank;
        return (
          <div key={`${r.kind}:${r.grade}`} className="grid grid-cols-[6.5rem_1fr_5rem] items-center gap-2 text-xs">
            <span className={cn("truncate", r.kind === "untagged" && "italic text-muted-foreground", r.kind === "unknown" && "text-destructive")} title={name}>{r.kind === "unknown" ? `! ${name}` : name}</span>
            <span className="flex h-2.5 overflow-hidden rounded-sm bg-muted">
              <i className={cn("block h-full", s.dot)} style={{ width: `${seg(r.nodes)}%` }} />
              <i
                className={cn("block h-full", s.dot)}
                style={{ width: `${seg(r.edges)}%`, backgroundImage: "repeating-linear-gradient(135deg, var(--card) 0 2px, transparent 2px 4px)", backgroundBlendMode: "normal" }}
              />
            </span>
            <span className="text-right font-mono tabular-nums text-muted-foreground">{num(r.nodes)} · {num(r.edges)}</span>
          </div>
        );
      })}
      <div className="flex gap-4 pt-1 text-[11px] text-muted-foreground">
        <span className="inline-flex items-center gap-1.5"><i className="inline-block h-2 w-4 rounded-sm bg-muted-foreground/70" />{t.nodes}</span>
        <span className="inline-flex items-center gap-1.5"><i className="inline-block h-2 w-4 rounded-sm bg-muted-foreground/70" style={{ backgroundImage: "repeating-linear-gradient(135deg, var(--card) 0 2px, transparent 2px 4px)" }} />{t.edges}</span>
      </div>
    </div>
  );
}

function Report({ rep }: { rep: ContractReport }) {
  const lang = useLang();
  const t = GRAPH_TEXT[lang];
  const num = (n: number) => formatNumber(lang, n);
  if (!rep.available) {
    return <Hint>{t.cannotReadContract}{rep.reason ? ` ${rep.reason}` : ""}</Hint>;
  }
  const att = rep.attention ?? [];
  return (
    <>
      {!rep.graded && !rep.untagged ? (
        <Hint>{t.nothingOnShelf}</Hint>
      ) : !rep.graded ? (
        <Hint>{t.noneGraded[0]}<b className="text-foreground">{num(rep.nodes)}</b>{t.noneGraded[1]}<b className="text-foreground">{num(rep.edges)}</b>{t.noneGraded[2]}</Hint>
      ) : (
        <>
          <Bar rows={rep.rows ?? []} />
          <p className="mt-2"><Hint>{t.countedWhole}</Hint></p>
        </>
      )}
      <SectionTitle>{t.needsPerson}</SectionTitle>
      {att.length === 0 ? (
        <Hint>{t.nothingHeld}</Hint>
      ) : (
        <ul className="space-y-2">
          {att.map((r) => {
            const meta = [r.by ? t.byWhom(r.grade === "refused", r.by) : "", r.state, r.producer, r.source, r.at].filter(Boolean).join(" · ");
            return (
              <li key={r.id} className="rounded-lg border p-2 text-xs">
                <div className="flex items-start gap-2">
                  <GradeChip value={r.grade} />
                  <button type="button" className="min-w-0 flex-1 break-words text-left text-[13px] hover:underline" onClick={() => !r.edge && selectNode(r.id)}>
                    {(r.content || r.id).slice(0, 90)}
                    {r.edge && <span className="ml-1 text-muted-foreground">{t.edge}</span>}
                  </button>
                </div>
                {r.why ? <p className="mt-1 text-muted-foreground">{r.why}</p> : <p className="mt-1 text-destructive">{t.noReason}</p>}
                {meta && <p className="mt-0.5 break-words text-[11px] text-muted-foreground">{meta}</p>}
              </li>
            );
          })}
        </ul>
      )}
      {rep.truncated && <p className="mt-1"><Hint>{t.showingOf(num(att.length), num(rep.total))}</Hint></p>}
    </>
  );
}

export function ContractContent() {
  const pinned = useLive((s) => s.pinned);
  const [rep, setRep] = useState<ContractReport | null>(null);
  const [error, setError] = useState("");
  const t = useText(GRAPH_TEXT);

  useEffect(() => {
    if (pinned) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let ctl = new AbortController();
    const load = async () => {
      ctl = new AbortController();
      try {
        setRep(await api.contract({ signal: ctl.signal }));
        setError("");
      } catch (err) {
        if (isAbort(err)) return;
        setError(err instanceof Error ? err.message : String(err));
      }
      timer = setTimeout(load, CONTRACT_MS);
    };
    void load();
    return () => {
      clearTimeout(timer);
      ctl.abort();
    };
  }, [pinned]);

  return (
    <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-3 py-3">
      {pinned ? (
        <Hint>{t.pinnedContract}</Hint>
      ) : error ? (
        <Hint tone="bad">{error}</Hint>
      ) : rep ? (
        <Report rep={rep} />
      ) : (
        <Hint>{t.readingContract}</Hint>
      )}
    </div>
  );
}
