import { useSyncExternalStore } from "react";
import { paletteFor, type OntologyPalette } from "@/lib/ontologyPalette";

// The diagram's accents are written into SVG attributes, which cannot follow a
// stylesheet's .dark the way a Tailwind class does, so the theme is read off
// the root element and the palette swapped with it. Watching the class rather
// than the stored mode keeps "auto" honest: it is whatever the root says.

function subscribe(cb: () => void) {
  const mo = new MutationObserver(cb);
  mo.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
  return () => mo.disconnect();
}

const isDark = () => document.documentElement.classList.contains("dark");

export function usePalette(): OntologyPalette {
  const dark = useSyncExternalStore(subscribe, isDark, () => false);
  return paletteFor(dark);
}
