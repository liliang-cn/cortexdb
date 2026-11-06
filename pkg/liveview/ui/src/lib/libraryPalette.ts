// The reading room's own colours: walnut, oak, parchment, brass and leathers.
//
// The library keeps a palette of its own rather than the app's tokens, because
// it is drawn as a room and a room is made of materials: a shelf of indigo
// books would read as the app's accent, not as a shelf. The panels over the
// scene use the app's tokens; only what is inside the room uses these.
//
// There is no violet anywhere. The app's accent is indigo, and a violet spine
// on a shelf would look like a selection or a link where there is none.

/** One set of leathers per wing, the largest first; a book takes one by the hash of its id. */
export const LEATHERS: readonly (readonly string[])[] = [
  ["#1f5e4b", "#2f7a5f", "#17483a", "#3b8a6c", "#245f4f"],
  ["#8a2f2f", "#a3443a", "#6e2424", "#b0533f", "#7d3328"],
  ["#2c4a6b", "#3b5f86", "#23395a", "#4a6f96", "#30506f"],
  ["#b8862f", "#9c6f22", "#c99a3b", "#a87a2a", "#d0a24a"],
  ["#b5552b", "#9a4524", "#c86a3a", "#a85230"],
  ["#2f6f73", "#3c8287", "#245a5e", "#4a9196"],
  ["#5e6b2a", "#717f33", "#4c5722", "#86944a"],
];

/** The general stacks: plain brown cloth, because nothing filed them. */
export const STACK_CLOTH: readonly string[] = ["#6b4a32", "#845c3e", "#5a3c28", "#93694a", "#7a553a", "#a07a55"];

/** Reference volumes: cream buckram with gilt bands. */
export const REFERENCE_BUCKRAM: readonly string[] = ["#e3d3ad", "#d6c299", "#cdb68a", "#e8dbbb"];

/** The annex mixes the earth tones of every wing, since its shelves belong to none. */
export const ANNEX_EARTH: readonly string[] = ["#7a553a", "#8a2f2f", "#2c4a6b", "#b8862f", "#1f5e4b", "#b5552b"];

/** The materials that do not change between day and night. */
export const WOOD = {
  walnut: "#5b3a26",
  walnutDark: "#43291a",
  oak: "#a7764a",
  wainscot: "#5a3a26",
  brass: "#c99a3b",
  carpet: "#1f5e4b",
  parchment: "#f1e4c4",
  lamp: "#1f6b4f",
  wax: "#f4ead2",
  waxGlow: "#ffcf7a",
  flame: "#ffd27a",
  /** A recalled or opened book's halo. */
  glow: "#ffd46e",
  /** A book found by the search box or named by an open card. */
  found: "#45d3a0",
  /** A recall's thread to the reading table, and a book's to the lectern. */
  thread: "#f2b84b",
  /** A card's threads from the catalogue to its books. */
  ink: "#1f8a6a",
  /** The relation table's spokes. */
  line: "#5a3a26",
  /** The open book's paper edges. */
  paperEdge: "#efe2c1",
} as const;

/** What changes when the lights go down. */
export interface RoomTone {
  background: string;
  wall: string;
  glass: string;
  glassGlow: string;
  glassGlowIntensity: number;
  parquet: readonly [string, string];
  sky: string;
  ground: string;
  hemisphere: number;
  sun: string;
  sunIntensity: number;
  fill: string;
  fillIntensity: number;
  environment: number;
  exposure: number;
  lampGlow: number;
  waxGlow: number;
  haloOpacity: number;
  threadOpacity: number;
  /** Lamps and candles cast light only at night; by day the windows do. */
  candles: boolean;
  fog: boolean;
}

export const DAY: RoomTone = {
  background: "#e9dcc0",
  wall: "#efe2c6",
  glass: "#fdf2d6",
  glassGlow: "#fff3d0",
  glassGlowIntensity: 0.9,
  parquet: ["#c49a68", "#b48858"],
  sky: "#fff6e6",
  ground: "#c8b08a",
  hemisphere: 1.25,
  sun: "#fff0d0",
  sunIntensity: 2.4,
  fill: "#ffe7c2",
  fillIntensity: 0.6,
  environment: 0.45,
  exposure: 1.05,
  lampGlow: 0,
  waxGlow: 0.05,
  haloOpacity: 0.28,
  threadOpacity: 0.65,
  candles: false,
  fog: false,
};

export const NIGHT: RoomTone = {
  background: "#1b1712",
  wall: "#3a3026",
  glass: "#1d2c45",
  glassGlow: "#0e1828",
  glassGlowIntensity: 0.6,
  parquet: ["#6e4c32", "#5f402a"],
  sky: "#6a5a44",
  ground: "#1c140c",
  hemisphere: 0.75,
  sun: "#9fb3d6",
  sunIntensity: 0.25,
  fill: "#ffe7c2",
  fillIntensity: 0.15,
  environment: 0.12,
  exposure: 1.1,
  lampGlow: 0.5,
  waxGlow: 0.35,
  haloOpacity: 0.4,
  threadOpacity: 0.8,
  candles: true,
  fog: true,
};

export const roomTone = (night: boolean): RoomTone => (night ? NIGHT : DAY);

/** Lamp and candle light at night. */
export const LAMP_LIGHT = "#ffd08a";
export const CANDLE_LIGHT = "#ffc46b";

/** Ink on the open book's pages, which are parchment by day and by night. */
export const PAGE_INK = {
  ink: "#2a1d12",
  mute: "#7a634a",
  green: "#1f6b52",
  brass: "#9c6f22",
  paper: "#fbf2dc",
  paperEdge: "#ead9b4",
} as const;

/**
 * A serif for book text: the pages read as a book, and a serif with CJK
 * fallbacks keeps Chinese memories in the same voice as English ones.
 */
export const BOOK_SERIF =
  '"Iowan Old Style","Palatino Linotype",Palatino,Georgia,"Songti SC","Noto Serif SC","Source Han Serif SC",serif';

/** Hue in degrees and HSL saturation and lightness in 0..1, of a #rrggbb colour. */
export function hsl(hex: string): { h: number; s: number; l: number } {
  const m = /^#([0-9a-f]{6})$/i.exec(hex.trim());
  if (!m) throw new Error(`not a #rrggbb colour: ${hex}`);
  const n = parseInt(m[1] ?? "0", 16);
  const r = ((n >> 16) & 255) / 255;
  const g = ((n >> 8) & 255) / 255;
  const b = (n & 255) / 255;
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const l = (max + min) / 2;
  const d = max - min;
  if (d === 0) return { h: 0, s: 0, l };
  const s = d / (1 - Math.abs(2 * l - 1));
  let h: number;
  if (max === r) h = ((g - b) / d) % 6;
  else if (max === g) h = (b - r) / d + 2;
  else h = (r - g) / d + 4;
  h *= 60;
  if (h < 0) h += 360;
  return { h, s, l };
}

/**
 * Whether a colour reads as violet: a hue between blue and red, saturated
 * enough to be seen as a colour rather than a grey. The bound on saturation
 * keeps a blue-grey night window out of it; the hue band starts past the
 * blues the leathers and the night sky use.
 */
export function isViolet(hex: string): boolean {
  const { h, s } = hsl(hex);
  return h >= 250 && h <= 335 && s > 0.25;
}
