import { KindChip } from "@/components/semantic";
import { Empty, Page, PageHeader } from "@/components/Page";
import { useActivity, useCapabilities } from "@/live";
import { formatRelative } from "@/lib/time";
import { useNow } from "@/lib/useNow";

// Placeholder for the activity timeline: the store's ring of tool calls.
export function ActivityPage() {
  const events = useActivity();
  const caps = useCapabilities();
  const now = useNow();

  return (
    <Page>
      <PageHeader title="Activity" description="Tool calls the brain just handled: reads, writes and relations." />
      {events.length === 0 ? (
        <Empty title={caps.activity ? "No calls yet" : "No activity from this source"}>
          {caps.activity ? "Calls appear here the moment an agent makes them." : "This process does not observe tool calls, so only the graph is live."}
        </Empty>
      ) : (
        <ol className="divide-y rounded-xl border bg-card">
          {events.map((e) => (
            <li key={`${e.seq}:${e.at}`} className="flex items-center gap-3 px-4 py-2.5 text-sm">
              <KindChip event={e} className="w-24 shrink-0 justify-start" />
              <span className="min-w-0 flex-1 truncate">{e.text}</span>
              <span className="hidden shrink-0 font-mono text-xs text-muted-foreground sm:inline">{e.tool}</span>
              <time className="shrink-0 text-xs text-muted-foreground" dateTime={new Date(e.at).toISOString()}>
                {formatRelative(e.at, now)}
              </time>
            </li>
          ))}
        </ol>
      )}
    </Page>
  );
}
