export const OPEN_COMMAND_PALETTE_EVENT = "sky10:command-palette.open";

export function openCommandPalette() {
  window.dispatchEvent(new Event(OPEN_COMMAND_PALETTE_EVENT));
}
