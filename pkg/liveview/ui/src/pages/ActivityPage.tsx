import { useMemo, useState } from "react";
import { Pause, Play } from "lucide-react";
import { Link } from "react-router";
import { ActivityRow } from "@/components/feed/ActivityRow";
import { Chip } from "@/components/semantic";
import { Empty, Page, PageHeader } from "@/components/Page";
import { Button } from "@/components/ui/button";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { ACTIVITY_ORDER, activityUpTo, filterActivity } from "@/lib/activity";
import { FEED_TEXT } from "@/lib/feedStrings";
import { useLang } from "@/lib/i18n";
import { ACTIVITY_KINDS, semLabel } from "@/lib/semantics";
import { useNow } from "@/lib/useNow";
import { useActivity, useCapabilities, useLive } from "@/live";
import { pageShown } from "@/lib/pages";

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
  const lang = useLang();
  const t = FEED_TEXT[lang];
  const [toolLead, toolTail] = t.noCallsViewBody.split("{tool}");
  const [changesLead, changesTail] = t.changesStillLists.split("{changes}");

  let body;
  if (!ready) {
    body = <Empty title={t.connecting}>{t.waitingStream}</Empty>;
  } else if (!caps.activity && events.length === 0) {
    body = (
      <Empty title={t.noCallsViewTitle}>
        {toolLead}
        <span className="font-mono">serve_graph_3d</span>
        {toolTail}
        {pageShown("/changes") && (
          <>
            {" "}
            {changesLead}
            <Link to="/changes" className="text-primary underline underline-offset-4">{t.changesLink}</Link>
            {changesTail}
          </>
        )}
      </Empty>
    );
  } else if (shown.length === 0) {
    body =
      kinds.length > 0 ? (
        <Empty title={t.noKindTitle}>{t.noKindBody}</Empty>
      ) : (
        <Empty title={t.noCallsTitle}>{t.noCallsBody}</Empty>
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
        title={t.activityTitle}
        description={t.activityDescription}
        actions={
          caps.activity ? (
            <Button variant="outline" size="sm" aria-pressed={pausedAt !== null} onClick={() => setPausedAt((p) => (p === null ? Date.now() : null))}>
              {pausedAt === null ? <Pause aria-hidden /> : <Play aria-hidden />}
              {pausedAt === null ? t.pauseLive : waiting > 0 ? t.resumeNew(waiting) : t.resumeLive}
            </Button>
          ) : undefined
        }
      />
      {caps.activity && (
        <ToggleGroup type="multiple" value={kinds} onValueChange={setKinds} variant="outline" size="sm" className="mb-5 flex-wrap justify-start" aria-label={t.filterByKind}>
          {ACTIVITY_ORDER.map((k) => (
            <ToggleGroupItem key={k} value={k} aria-label={semLabel(ACTIVITY_KINDS[k], lang)} className="gap-1.5 px-2.5">
              <Chip s={ACTIVITY_KINDS[k]} className="bg-transparent px-0" />
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      )}
      {body}
    </Page>
  );
}
