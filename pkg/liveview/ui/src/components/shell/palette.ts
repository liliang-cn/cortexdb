// The command palette is opened from anywhere (a term chip on the activity
// page, the top bar button, the keyboard) through one window event, so no page
// needs to hold its state.

export const PALETTE_EVENT = "cortexdb:palette";

/** Open the palette, optionally with a search already typed. */
export function openPalette(query = ""): void {
  window.dispatchEvent(new CustomEvent<string>(PALETTE_EVENT, { detail: query }));
}
