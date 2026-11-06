import type { ActivityEvent } from "@/api";
import { ACTIVITY_ORDER, activityTone, type ActivityTone } from "@/lib/semantics";

/** The semantic key an event is drawn as; a failed call is "failed" whatever it tried. */
export function activityKeyOf(e: ActivityEvent): ActivityTone {
  return activityTone(e).key as ActivityTone;
}

export function filterActivity(events: ReadonlyArray<ActivityEvent>, kinds: ReadonlyArray<string>): ActivityEvent[] {
  if (kinds.length === 0) return [...events];
  return events.filter((e) => kinds.includes(activityKeyOf(e)));
}

/** Events at or before an instant: a paused view keeps what it had. */
export function activityUpTo(events: ReadonlyArray<ActivityEvent>, at: number | null): ActivityEvent[] {
  return at === null ? [...events] : events.filter((e) => e.at <= at);
}

export { ACTIVITY_ORDER };
