import { useMemo } from "react";
import { Chip } from "@/components/semantic";
import { Empty, Page, PageHeader } from "@/components/Page";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { useCapabilities, useCounts, useLive, useNodes } from "@/live";
import {
  ACTIVITY_KINDS,
  ACTIVITY_ORDER,
  CHANGE_OPS,
  CHANGE_OP_ORDER,
  GRADES,
  GRADE_ORDER,
  grade,
  type GradeKey,
} from "@/lib/semantics";

// Placeholder for the graph page: a live summary read from the store, plus the
// legend of the shared meanings. The 3D scene replaces the body.
export function GraphPage() {
  const nodes = useNodes();
  const { nodes: n, edges: e } = useCounts();
  const caps = useCapabilities();
  const ready = useLive((s) => s.ready);

  const byGrade = useMemo(() => {
    const m = new Map<GradeKey, number>();
    for (const node of nodes) {
      const k = grade(node.grade).key as GradeKey;
      m.set(k, (m.get(k) ?? 0) + 1);
    }
    return m;
  }, [nodes]);
  const byType = useMemo(() => {
    const m = new Map<string, number>();
    for (const node of nodes) m.set(node.type, (m.get(node.type) ?? 0) + 1);
    return [...m].sort((a, b) => b[1] - a[1]).slice(0, 8);
  }, [nodes]);

  return (
    <Page>
      <PageHeader title="Graph" description="The brain as it is right now. This page updates as the stream delivers changes." />
      {ready && n === 0 ? (
        <Empty title="Nothing in the graph yet">Entities and relations show up here as they are written.</Empty>
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle className="text-base">What is in it</CardTitle>
              <CardDescription>
                <span className="font-mono tabular-nums text-foreground">{n.toLocaleString()}</span> entities,{" "}
                <span className="font-mono tabular-nums text-foreground">{e.toLocaleString()}</span> relations
              </CardDescription>
            </CardHeader>
            <CardContent>
              <ul className="space-y-1.5 text-sm">
                {byType.map(([type, count]) => (
                  <li key={type} className="flex items-center justify-between gap-3">
                    <span className="truncate">{type || "untyped"}</span>
                    <span className="font-mono text-xs tabular-nums text-muted-foreground">{count}</span>
                  </li>
                ))}
              </ul>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="text-base">How well established</CardTitle>
              <CardDescription>
                {caps.grades ? "Entities by knowledge-contract grade." : "This source does not report grades."}
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-2">
              {GRADE_ORDER.filter((k) => byGrade.get(k)).map((k) => {
                const count = byGrade.get(k) ?? 0;
                return (
                  <div key={k} className="flex items-center gap-3 text-sm">
                    <span className="w-28 shrink-0">{GRADES[k].label}</span>
                    <span className="h-2 flex-1 overflow-hidden rounded-full bg-muted">
                      <span className={`block h-full rounded-full ${GRADES[k].dot}`} style={{ width: `${Math.max(2, (count / Math.max(1, n)) * 100)}%` }} />
                    </span>
                    <span className="w-10 text-right font-mono text-xs tabular-nums text-muted-foreground">{count}</span>
                  </div>
                );
              })}
            </CardContent>
          </Card>

          <Card className="md:col-span-2">
            <CardHeader>
              <CardTitle className="text-base">Colour key</CardTitle>
              <CardDescription>The same colour and icon mean the same thing on every page.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <Legend title="Changes" items={CHANGE_OP_ORDER.map((k) => CHANGE_OPS[k])} />
              <Legend title="Activity" items={ACTIVITY_ORDER.map((k) => ACTIVITY_KINDS[k])} />
              <Legend title="Grades" items={GRADE_ORDER.map((k) => GRADES[k])} />
            </CardContent>
          </Card>
        </div>
      )}
    </Page>
  );
}

function Legend({ title, items }: { title: string; items: (typeof GRADES)[GradeKey][] }) {
  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-2">
      <span className="w-20 shrink-0 text-sm text-muted-foreground">{title}</span>
      {items.map((s) => (
        <Chip key={s.key} s={s} />
      ))}
    </div>
  );
}
