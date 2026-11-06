// Colours for the things the 3D scene draws that are not one of the shared
// meanings (change op, activity kind, grade; those live in lib/semantics.ts).
//
// A relation's colour is a function of its predicate alone: the same word is
// the same colour in every session, on a light ground and on a dark one, with
// no table to keep in step and nothing that depends on the order predicates
// arrive in. Twelve hues are laid round the OKLCH wheel and a hash picks one;
// two predicates can share a hue, and the legend, which always carries the
// word, is what tells them apart.

export const CO_OCCURS = "co_occurs";

/** A neutral for the one predicate that says nothing about how two things relate. */
export const CO_OCCURS_COLOR = "#8a94a6";

export type Rgb = readonly [number, number, number];

/** FNV-1a, 32 bit. Stable across runs and engines, which `Math.random` and object order are not. */
export function hash32(s: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}

const clamp01 = (v: number) => Math.min(1, Math.max(0, v));
const encode = (v: number) => {
  const c = clamp01(v);
  return c <= 0.0031308 ? 12.92 * c : 1.055 * Math.pow(c, 1 / 2.4) - 0.055;
};

/** OKLCH to sRGB, each channel 0..255. */
export function oklchToRgb(l: number, c: number, hDeg: number): Rgb {
  const h = (hDeg * Math.PI) / 180;
  const a = c * Math.cos(h);
  const b = c * Math.sin(h);
  const l_ = (l + 0.3963377774 * a + 0.2158037573 * b) ** 3;
  const m_ = (l - 0.1055613458 * a - 0.0638541728 * b) ** 3;
  const s_ = (l - 0.0894841775 * a - 1.291485548 * b) ** 3;
  const r = 4.0767416621 * l_ - 3.3077115913 * m_ + 0.2309699292 * s_;
  const g = -1.2684380046 * l_ + 2.6097574011 * m_ - 0.3413193965 * s_;
  const bl = -0.0041960863 * l_ - 0.7034186147 * m_ + 1.707614701 * s_;
  return [Math.round(encode(r) * 255), Math.round(encode(g) * 255), Math.round(encode(bl) * 255)];
}

export function rgbToHex([r, g, b]: Rgb): string {
  return `#${[r, g, b].map((v) => v.toString(16).padStart(2, "0")).join("")}`;
}

export function hexToRgb(hex: string): Rgb {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex.trim());
  if (!m || !m[1]) return [128, 128, 128];
  const n = parseInt(m[1], 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

/** Linear mix of two hex colours; t = 0 is a, t = 1 is b. */
export function mixHex(a: string, b: string, t: number): string {
  const x = hexToRgb(a);
  const y = hexToRgb(b);
  const k = clamp01(t);
  return rgbToHex([
    Math.round(x[0] + (y[0] - x[0]) * k),
    Math.round(x[1] + (y[1] - x[1]) * k),
    Math.round(x[2] + (y[2] - x[2]) * k),
  ]);
}

// Hue order is interleaved so that two slots next to each other on the wheel
// are never next to each other in the table: a hash that lands near another
// still lands far from it in colour.
const HUE_ORDER = [0, 5, 10, 3, 8, 1, 6, 11, 4, 9, 2, 7];
const PREDICATE_L = 0.64;
const PREDICATE_C = 0.15;

export const PREDICATE_PALETTE: readonly string[] = HUE_ORDER.map((slot) =>
  rgbToHex(oklchToRgb(PREDICATE_L, PREDICATE_C, 18 + slot * 30)),
);

export function predicateSlot(predicate: string): number {
  return hash32(predicate) % PREDICATE_PALETTE.length;
}

/** One colour per predicate, the same on a light and a dark ground. */
export function predicateColor(predicate: string): string {
  if (!predicate) return CO_OCCURS_COLOR;
  if (predicate === CO_OCCURS) return CO_OCCURS_COLOR;
  return PREDICATE_PALETTE[predicateSlot(predicate)] ?? CO_OCCURS_COLOR;
}

/**
 * A node type's colour. Unlike a predicate, a type is drawn as a filled
 * sphere, so it is lighter on a dark ground and deeper on a light one.
 */
export function typeColor(type: string, dark: boolean): string {
  if (!type) return dark ? "#7c8ba1" : "#94a3b8";
  const hue = (hash32(`type:${type}`) % 360 + 0) % 360;
  // Skip the narrow band that reads as the selection indigo of the shell.
  // And the yellow-green band, which turns to olive at the depth a sphere needs.
  const h = hue >= 262 && hue < 292 ? (hue + 36) % 360 : hue >= 80 && hue < 135 ? hue + 55 : hue;
  return rgbToHex(oklchToRgb(dark ? 0.76 : 0.56, dark ? 0.13 : 0.14, h));
}

/** Cool (reached once) to hot (reached most) on a log scale; 0 is the ground's grey. */
export function heatColor(count: number, max: number, dark: boolean): string {
  if (!count) return dark ? "#334155" : "#cbd5e1";
  const k = max > 1 ? Math.log(count) / Math.log(max) : 1;
  return rgbToHex(oklchToRgb(dark ? 0.76 : 0.62, 0.16, 250 - 215 * clamp01(k)));
}
