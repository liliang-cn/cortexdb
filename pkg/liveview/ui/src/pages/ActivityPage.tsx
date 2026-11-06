import { useMemo, useState } from "react";
import { Pause, Play } from "lucide-react";
import { Link } from "react-router";
import { ActivityRow } from "@/components/feed/ActivityRow";
import { Chip } from "@/components/semantic";
import { Empty, Page, PageHeader } from "@/components/Page";
import { Button } from "@/components/ui/button";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { ACTIVITY_ORDER, activityUpTo, filterActivity } from "@/lib/activity";
import { ACTIVITY_KINDS } from "@/lib/semantics";
import { useNow } from "@/lib/useNow";
import { useActivity, useCapabilities, useLive } from "@/live";

/** Tool calls as they happen: what an agent read, wrote and linked. */
export function ActivityPage() {
  const events = useActivity();
  const caps = useCapabilities();
  const ready = useLive((s) => s.ready);
  const now = useNow(5000);
  const [kinds, setKinds] = useState<string[]>([]);
  const [pausedAt, setPausedAt] = useState<number | null>(null);

  const shown = useMemo(() => filterActivity(activityUpTo(events, pausedAt), kinds), [events, pausedAt, kinds]);
  const waiting = pausedAt === null ? 0 : filterActivity(events, kinds).length - shown.length;
  const firstSeen = useMemo(() => Date.now(), []);

  let body;
  if (!ready) {
    body = <Empty title="Connecting">Waiting for the live stream.</Empty>;
  } else if (!caps.activity && events.length === 0) {
    body = (
      <Empty title="This view cannot see tool calls">
        Tool calls only show when the view is served by the agent's MCP server (<span className="font-mono">serve_graph_3d</span>); the command-line view reads the
        database alone. <Link to="/changes" className="text-primary underline underline-offset-4">Changes</Link> still lists every write.
      </Empty>
    );
  } else if (shown.length === 0) {
    body =
      kinds.length > 0 ? (
        <Empty title="No calls of that kind">Pick another kind, or clear the filter to see every call.</Empty>
      ) : (
        <Empty title="No calls yet">Calls appear here the moment an agent makes them.</Empty>
      );
  } else {
    body = (
      <ol className="divide-y overflow-hidden rounded-xl border bg-card">
        {shown.map((e) => (
          <li key={`${e.seq}:${e.at}`}>
            <ActivityRow event={e} now={now} fresh={e.at > firstSeen} />
          </li>
        ))}
      </ol>
    );
  }

  return (
    <Page>
      <PageHeader
        title="Activity"
        description="Tool calls the brain just handled: what was read, written and linked, and the terms each one named."
        actions={
          caps.activity ? (
            <Button variant="outline" size="sm" aria-pressed={pausedAt !== null} onClick={() => setPausedAt((p) => (p === null ? Date.now() : null))}>
              {pausedAt === null ? <Pause aria-hidden /> : <Play aria-hidden />}
              {pausedAt === null ? "Pause live" : waiting > 0 ? `Resume (${waiting} new)` : "Resume live"}
            </Button>
          ) : undefined
        }
      />
      {caps.activity && (
        <ToggleGroup type="multiple" value={kinds} onValueChange={setKinds} variant="outline" size="sm" className="mb-5 flex-wrap justify-start" aria-label="Filter by kind">
          {ACTIVITY_ORDER.map((k) => (
            <ToggleGroupItem key={k} value={k} aria-label={ACTIVITY_KINDS[k].label} className="gap-1.5 px-2.5">
              <Chip s={ACTIVITY_KINDS[k]} className="bg-transparent px-0" />
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      )}
      {body}
    </Page>
  );
}
