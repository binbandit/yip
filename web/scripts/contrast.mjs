// Prints WCAG 2.2 contrast ratios for the colour pairs the UI actually uses,
// read from the theme itself: Astryx's neutral tokens plus yip's own
// (src/lib/theme-tokens.ts), and exits non-zero if any required pair fails.
// Run: node scripts/contrast.mjs
import { neutralTheme } from '@astryx-svelte/theme-neutral/tokens';
import { yipTokens } from '../src/lib/theme-tokens.ts';

const tokens = { ...neutralTheme.tokens, ...yipTokens };
const modes = [
  ['Day', 0],
  ['Night', 1],
];

const rgb = (hex) => hex.replace('#', '').match(/../g).map((h) => parseInt(h, 16));
const toHex = (c) => '#' + c.map((v) => Math.round(v).toString(16).padStart(2, '0')).join('');
// #RRGGBBAA values are washes: composite them over what they sit on.
const over = (fg, bg) => {
  const [r, g, b, a = 255] = rgb(fg);
  const base = rgb(bg);
  return toHex([r, g, b].map((v, i) => v * (a / 255) + base[i] * (1 - a / 255)));
};
const mix = (fg, bg, alpha) => {
  const [f, b] = [rgb(fg), rgb(bg)];
  return toHex(f.slice(0, 3).map((v, i) => v * alpha + b[i] * (1 - alpha)));
};
const lum = (hex) => {
  const c = rgb(hex)
    .slice(0, 3)
    .map((v) => v / 255)
    .map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
};
const ratio = (a, b) => {
  const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p);
  return (x + 0.05) / (y + 0.05);
};

let failed = false;
const check = (r, min, label) => {
  if (r < min) failed = true;
  console.log(`${r >= min ? 'pass' : 'FAIL'}  ${r.toFixed(2)}:1  ${label}`);
};

for (const [name, i] of modes) {
  const t = (key) => {
    const v = tokens[`--${key}`];
    return Array.isArray(v) ? v[i] : v;
  };
  const surface = t('color-background-surface');
  const c = {
    surface,
    muted: over(t('color-background-muted'), surface),
    'frame-top': t('yip-frame-top'),
    'frame-bottom': t('yip-frame-bottom'),
    text: t('color-text-primary'),
    'text-secondary': t('color-text-secondary'),
    accent: t('color-accent'),
    'on-accent': t('color-on-accent'),
    success: t('color-success'),
    error: t('color-error'),
    warning: t('color-warning'),
    'success-muted': over(t('color-success-muted'), surface),
    'error-muted': over(t('color-error-muted'), surface),
    'warning-muted': over(t('color-warning-muted'), surface),
    'border-emphasized': t('color-border-emphasized'),
    'control-edge': t('yip-control-edge'),
    'attention-fill': t('yip-attention-fill'),
  };
  // Read room names in the sidebar are ink at 82% over the frame; check both ends.
  c['room@frame-top'] = mix(c.text, c['frame-top'], 0.82);
  c['room@frame-bottom'] = mix(c.text, c['frame-bottom'], 0.82);

  const text = [
    ['text', 'surface'], ['text', 'muted'], ['text', 'frame-top'], ['text', 'frame-bottom'],
    ['text-secondary', 'surface'], ['text-secondary', 'muted'], ['text-secondary', 'frame-top'], ['text-secondary', 'frame-bottom'],
    ['room@frame-top', 'frame-top'], ['room@frame-bottom', 'frame-bottom'],
    ['accent', 'surface'], ['on-accent', 'accent'],
    ['success', 'surface'], ['error', 'surface'], ['warning', 'surface'],
    ['success', 'success-muted'], ['error', 'error-muted'], ['warning', 'warning-muted'], ['text', 'warning-muted'],
  ];
  // Required: controls and the edges that let you find them. Informational:
  // hairlines and markers that always sit beside text saying the same thing.
  const nontext = [['accent', 'surface'], ['control-edge', 'surface'], ['control-edge', 'muted']];
  const decorative = [['border-emphasized', 'surface'], ['attention-fill', 'surface']];

  console.log(`\n${name} — text (AA needs 4.5:1)`);
  for (const [f, b] of text) check(ratio(c[f], c[b]), 4.5, `${f} on ${b}`);
  console.log(`${name} — non-text UI (needs 3:1 where it conveys state or is a control boundary)`);
  for (const [f, b] of nontext) check(ratio(c[f], c[b]), 3, `${f} vs ${b}`);
  for (const [f, b] of decorative) {
    const r = ratio(c[f], c[b]);
    console.log(`info  ${r.toFixed(2)}:1  ${f} vs ${b}`);
  }
}

// Engineer avatars: initial letter on the hue tint (decorative; the name is always shown).
const hsl = (h, s, l) => {
  s /= 100;
  l /= 100;
  const k = (n) => (n + h / 30) % 12;
  const a = s * Math.min(l, 1 - l);
  const f = (n) => l - a * Math.max(-1, Math.min(k(n) - 3, Math.min(9 - k(n), 1)));
  return toHex([f(0), f(8), f(4)].map((x) => x * 255));
};
let minDay = 99;
let minNight = 99;
for (let h = 0; h < 360; h += 5) {
  minDay = Math.min(minDay, ratio(hsl(h, 45, 27), hsl(h, 34, 91)));
  minNight = Math.min(minNight, ratio(hsl(h, 45, 88), hsl(h, 26, 26)));
}
console.log(`\nAvatar initials across all hues: day min ${minDay.toFixed(2)}:1, night min ${minNight.toFixed(2)}:1`);
if (failed) process.exitCode = 1;
