// yip's own theme tokens as plain data ([day, night] pairs), so the theme and
// scripts/contrast.mjs read the same values. Everything else comes from
// Astryx's neutral theme.
export const yipTokens = {
  // The frame gradient behind the sidebar and the work card.
  '--yip-frame-top': ['#ebe9d8', '#33311d'],
  '--yip-frame-bottom': ['#d5dce3', '#0e1621'],
  // Amber markers for something waiting on the owner (never text).
  '--yip-attention-fill': ['#d4a72c', '#bb8009'],
  // The edge of a text field, select or checkbox, and a switch's off track: 3:1 against the surface.
  '--yip-control-edge': ['#858585', '#7a7a7a'],
} as const satisfies Record<`--yip-${string}`, readonly [light: string, dark: string]>;
