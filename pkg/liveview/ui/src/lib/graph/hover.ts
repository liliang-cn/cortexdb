// Hover intent.
//
// A hovered node centres the view: its neighbourhood lights and everything
// else dims. Taken from the pointer as it is, that is a flash across the whole
// picture for every node a sweeping pointer crosses, and a flash back for
// every gap between them. What a person means by hovering is resting, so a
// value is taken only once the pointer has stayed on it for a moment, and let
// go only once the pointer has been off it for a moment longer. Moving from
// one node to the next passes straight from the first to the second, without
// the unfocused picture in between.

export interface HoverIntentOptions {
  /** How long the pointer rests on a value before it is taken. */
  enterMs: number;
  /** How long the pointer is off every value before the current one is let go. */
  leaveMs: number;
}

export const HOVER_INTENT: HoverIntentOptions = { enterMs: 110, leaveMs: 220 };

export interface HoverIntent<T> {
  /** What is under the pointer now, or null for nothing. */
  point(value: T | null): void;
  /** Take a value now, without waiting (a click, a release after a drag). */
  commit(value: T | null): void;
  /** Forget anything pending. */
  cancel(): void;
}

interface Timers {
  set(fn: () => void, ms: number): unknown;
  clear(handle: unknown): void;
}

const realTimers: Timers = {
  set: (fn, ms) => setTimeout(fn, ms),
  clear: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
};

/**
 * `apply` receives each value that has been held long enough; `current`
 * reports the value last applied, so pointing back at it cancels a pending
 * change instead of re-applying it.
 */
export function hoverIntent<T>(
  apply: (value: T | null) => void,
  current: () => T | null,
  opts: HoverIntentOptions = HOVER_INTENT,
  timers: Timers = realTimers,
): HoverIntent<T> {
  let handle: unknown = null;
  let pending: { value: T | null } | null = null;

  const cancel = () => {
    if (handle !== null) timers.clear(handle);
    handle = null;
    pending = null;
  };

  return {
    point(value) {
      if (pending && pending.value === value) return;
      cancel();
      if (value === current()) return;
      pending = { value };
      handle = timers.set(() => {
        handle = null;
        pending = null;
        apply(value);
      }, value === null ? opts.leaveMs : opts.enterMs);
    },
    commit(value) {
      cancel();
      if (value !== current()) apply(value);
    },
    cancel,
  };
}
