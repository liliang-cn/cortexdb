// Easing for the scene's emphasis.
//
// Dimming everything outside a focus in one frame reads as a flash, and so
// does undimming it. Each node and link instead carries how dimmed it is now,
// a number from 0 to 1, which follows its target exponentially: quick at
// first, settling smoothly, and the same whatever the frame rate.

/** The time constant of the follow: about 95% of the way after three of these. */
export const EMPHASIS_TAU_MS = 65;

/**
 * `v` moved toward `target` over `dt` milliseconds, with time constant `tau`.
 * Within a hair of the target it lands on it exactly, so a settled value can
 * be compared with `===`. A `tau` of 0 or less jumps (reduced motion).
 */
export function approach(v: number, target: number, dt: number, tau: number = EMPHASIS_TAU_MS): number {
  if (tau <= 0 || v === target) return target;
  const next = v + (target - v) * (1 - Math.exp(-Math.max(0, dt) / tau));
  return Math.abs(target - next) < 0.004 ? target : next;
}
