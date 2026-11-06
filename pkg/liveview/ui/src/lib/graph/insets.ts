// How much of the graph's stage the panels drawn over it cover, side by side,
// so the camera can put a node in the middle of what is left visible rather
// than under a phone's bottom sheet or behind a desktop side panel.
//
// A panel opts in by carrying data-occludes="top" | "right" | "bottom" | "left":
// the side of the stage it covers.

export type Side = "top" | "right" | "bottom" | "left";

export interface Insets {
  top: number;
  right: number;
  bottom: number;
  left: number;
}

export const NO_INSETS: Insets = { top: 0, right: 0, bottom: 0, left: 0 };

interface Box {
  top: number;
  right: number;
  bottom: number;
  left: number;
}

/** The share of each axis that always stays uncovered, however much the panels claim. */
const KEEP = 0.25;

/** The insets a set of covering panels leave, from their boxes and the stage's, in the same coordinates. */
export function insetsOf(stage: Box, covers: ReadonlyArray<{ side: Side; box: Box }>): Insets {
  const out = { ...NO_INSETS };
  for (const { side, box } of covers) {
    // A panel that does not overlap the stage covers none of it.
    if (box.right <= stage.left || box.left >= stage.right || box.bottom <= stage.top || box.top >= stage.bottom) continue;
    if (box.right <= box.left || box.bottom <= box.top) continue;
    if (side === "top") out.top = Math.max(out.top, box.bottom - stage.top);
    else if (side === "bottom") out.bottom = Math.max(out.bottom, stage.bottom - box.top);
    else if (side === "left") out.left = Math.max(out.left, box.right - stage.left);
    else out.right = Math.max(out.right, stage.right - box.left);
  }
  return {
    ...fitAxis(out.top, out.bottom, stage.bottom - stage.top, "top", "bottom"),
    ...fitAxis(out.left, out.right, stage.right - stage.left, "left", "right"),
  } as Insets;
}

/** Shrinks a pair of insets in proportion until at least KEEP of the length is left between them. */
function fitAxis<A extends Side, B extends Side>(a: number, b: number, length: number, ka: A, kb: B): Record<A | B, number> {
  const max = Math.max(0, length * (1 - KEEP));
  const sum = a + b;
  const k = sum > max && sum > 0 ? max / sum : 1;
  return { [ka]: Math.max(0, a * k), [kb]: Math.max(0, b * k) } as Record<A | B, number>;
}

interface Vec {
  x: number;
  y: number;
  z: number;
}

/**
 * Where a perspective camera looking along forward should aim, relative to a
 * point at distance away, for that point to land in the middle of the part of
 * a heightPx-tall view the insets leave uncovered. Zero when nothing is covered
 * or the view has no size.
 */
export function aimOffset(forward: Vec, up: Vec, fovDeg: number, heightPx: number, distance: number, insets: Insets): Vec {
  const zero = { x: 0, y: 0, z: 0 };
  if (!(heightPx > 0) || !(fovDeg > 0)) return zero;
  // The screen's right and up, from the line of sight and the camera's up.
  let rx = forward.y * up.z - forward.z * up.y;
  let ry = forward.z * up.x - forward.x * up.z;
  let rz = forward.x * up.y - forward.y * up.x;
  const rl = Math.hypot(rx, ry, rz);
  if (rl < 1e-6) return zero; // looking straight along up: no screen right
  rx /= rl;
  ry /= rl;
  rz /= rl;
  const ux = ry * forward.z - rz * forward.y;
  const uy = rz * forward.x - rx * forward.z;
  const uz = rx * forward.y - ry * forward.x;
  // World units per screen pixel at that distance (pixels are square).
  const perPx = (2 * distance * Math.tan((fovDeg * Math.PI) / 360)) / heightPx;
  // Where the uncovered middle is, from the view's centre, in pixels: right and down.
  const px = ((insets.left - insets.right) / 2) * perPx;
  const py = ((insets.top - insets.bottom) / 2) * perPx;
  // Aiming left of a point puts it right of centre; aiming above it puts it below.
  return { x: -rx * px + ux * py, y: -ry * px + uy * py, z: -rz * px + uz * py };
}

const SIDES: ReadonlySet<string> = new Set<Side>(["top", "right", "bottom", "left"]);

/** The insets the panels marked data-occludes inside a stage cover now. */
export function coveredInsets(stage: HTMLElement | null | undefined): Insets {
  if (!stage) return NO_INSETS;
  const covers: { side: Side; box: Box }[] = [];
  for (const el of stage.querySelectorAll<HTMLElement>("[data-occludes]")) {
    const side = el.dataset.occludes ?? "";
    if (SIDES.has(side)) covers.push({ side: side as Side, box: el.getBoundingClientRect() });
  }
  return insetsOf(stage.getBoundingClientRect(), covers);
}
