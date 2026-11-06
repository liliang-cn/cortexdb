// The ontology diagram's own colours, one set per theme.
//
// Everything neutral on the page (surfaces, borders, text) comes from the
// app's tokens through Tailwind classes, so it follows the theme with the rest
// of the app. What lives here is the colour that carries a meaning the tokens
// have no word for:
//
//   - lane accents, one per interface. Deliberately not the graph page's node
//     type palette: these colour interfaces, which that page has no concept
//     of, and reusing its hues would suggest a correspondence that does not
//     exist;
//   - the draft's amber. Everything a draft adds wears it and never the blue
//     the declarations do, because colour is the fastest of the signals that
//     say "nobody signed this", and the one a reader takes in before any
//     sentence;
//   - the band outside the model, in a warm grey no declared thing uses.
//
// No violet or magenta anywhere: a test holds every colour here to that.

export interface OntologyPalette {
  /** One accent per interface lane, cycled. */
  lanes: readonly string[];
  /** The lane of object types implementing nothing. */
  loose: string;
  /** A link type with edges in the store. */
  link: string;
  /** A link type nothing uses, or one whose count was not taken. */
  linkIdle: string;
  /** The end that declares a foreign key, and the glyph on it. */
  fk: string;
  fkInk: string;
  /** Everything a draft adds: its pill, its switch, its review half. */
  draft: string;
  draftInk: string;
  /** A type's lifecycle when it is not active (experimental, deprecated). */
  status: string;
  /** The band of types nobody declared. */
  stray: { border: string; ink: string; count: string; fill: string };
}

export const LIGHT_PALETTE: OntologyPalette = {
  lanes: ["#0284c7", "#ea580c", "#059669", "#e11d48", "#2563eb", "#4d7c0f", "#0d9488", "#b91c1c"],
  loose: "#64748b",
  link: "#3567a8",
  linkIdle: "#94a3b8",
  fk: "#1d4ed8",
  fkInk: "#ffffff",
  draft: "#b45309",
  draftInk: "#92400e",
  status: "#b45309",
  stray: { border: "#a8a29e", ink: "#44403c", count: "#78716c", fill: "#fafaf9" },
};

export const DARK_PALETTE: OntologyPalette = {
  lanes: ["#38bdf8", "#fb923c", "#34d399", "#fb7185", "#60a5fa", "#a3e635", "#2dd4bf", "#f87171"],
  loose: "#64748b",
  link: "#6b97d4",
  linkIdle: "#475569",
  fk: "#2563eb",
  fkInk: "#dbeafe",
  draft: "#f59e0b",
  draftInk: "#fbbf24",
  status: "#fbbf24",
  stray: { border: "#57534e", ink: "#d6d3d1", count: "#a8a29e", fill: "#1c1917" },
};

export function paletteFor(dark: boolean): OntologyPalette {
  return dark ? DARK_PALETTE : LIGHT_PALETTE;
}

/** The accent of the i-th interface, or the loose lane's grey when there is none. */
export function laneColor(p: OntologyPalette, index: number | null): string {
  if (index === null || index < 0) return p.loose;
  return p.lanes[index % p.lanes.length] ?? p.loose;
}

/** Every colour a palette holds, for the test that keeps them out of violet. */
export function paletteColors(p: OntologyPalette): string[] {
  return [...p.lanes, p.loose, p.link, p.linkIdle, p.fk, p.fkInk, p.draft, p.draftInk, p.status, ...Object.values(p.stray)];
}

/** A #rrggbb colour as hue in degrees and HSL saturation and lightness in 0..1. */
export function hexToHsl(hex: string): { h: number; s: number; l: number } {
  const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255) as [number, number, number];
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const l = (max + min) / 2;
  const d = max - min;
  if (d === 0) return { h: 0, s: 0, l };
  const s = d / (1 - Math.abs(2 * l - 1));
  const h = max === r ? ((g - b) / d) % 6 : max === g ? (b - r) / d + 2 : (r - g) / d + 4;
  return { h: (h * 60 + 360) % 360, s, l };
}

/** Violet through magenta, the hues this page never uses: 250° to 335° with any real saturation. */
export function isVioletOrMagenta(hex: string): boolean {
  const { h, s } = hexToHsl(hex);
  return s > 0.25 && h >= 250 && h <= 335;
}
