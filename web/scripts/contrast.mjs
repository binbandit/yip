// Prints WCAG 2.2 contrast ratios for the token pairs the UI actually uses.
// Run: node scripts/contrast.mjs
const day = {
  canvas: '#E9F2F3', navigation: '#D7E9EB', surface: '#FFFFFF', 'surface-subtle': '#F3F7F7',
  ink: '#183A43', 'ink-secondary': '#526B72', accent: '#176274', 'accent-ink': '#FFFFFF', line: '#D7E2E4',
  'attention-fill': '#F6DB7C', 'attention-ink': '#57420D', success: '#236A51', danger: '#AA3944',
  'accent-subtle': '#E3EFF1', 'danger-subtle': '#FBEFF0', 'success-subtle': '#EAF4EF', 'attention-subtle': '#FDF6DC', 'control-edge': '#748E95',
};
const night = {
  canvas: '#172C32', navigation: '#203B43', surface: '#21343B', 'surface-subtle': '#29424A',
  ink: '#EDF5F5', 'ink-secondary': '#AFC6CC', accent: '#89CFDE', 'accent-ink': '#173740', line: '#43606A',
  'attention-fill': '#705C24', 'attention-ink': '#FFF0B8', success: '#8FD4B5', danger: '#FFABB1',
  'accent-subtle': '#2A4C55', 'danger-subtle': '#4A3238', 'success-subtle': '#26463D', 'attention-subtle': '#3C3A28', 'control-edge': '#7B9BA4',
};
const lum = (hex) => {
  const c = hex.replace('#', '').match(/../g).map((h) => parseInt(h, 16) / 255)
    .map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
};
const ratio = (a, b) => { const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p); return (x + 0.05) / (y + 0.05); };
const text = [
  ['ink', 'surface'], ['ink', 'surface-subtle'], ['ink', 'canvas'], ['ink', 'navigation'], ['ink', 'accent-subtle'],
  ['ink-secondary', 'surface'], ['ink-secondary', 'surface-subtle'], ['ink-secondary', 'canvas'], ['ink-secondary', 'navigation'], ['ink-secondary', 'accent-subtle'],
  ['accent', 'surface'], ['accent', 'surface-subtle'], ['accent', 'canvas'], ['accent', 'navigation'], ['accent', 'accent-subtle'],
  ['accent-ink', 'accent'], ['attention-ink', 'attention-fill'], ['attention-ink', 'surface'],
  ['success', 'surface'], ['success', 'success-subtle'], ['danger', 'surface'], ['danger', 'danger-subtle'], ['danger', 'surface-subtle'],
  ['attention-ink', 'attention-subtle'], ['ink', 'attention-subtle'], ['ink', 'success-subtle'], ['ink', 'danger-subtle'],
];
const nontext = [['accent', 'surface'], ['accent', 'canvas'], ['control-edge', 'surface'], ['control-edge', 'canvas'], ['attention-fill', 'surface'], ['line', 'surface']];
for (const [name, t] of [['Day', day], ['Night', night]]) {
  console.log(`\n${name} — text (AA needs 4.5:1)`);
  for (const [f, b] of text) { const r = ratio(t[f], t[b]); console.log(`${r >= 4.5 ? 'pass' : 'FAIL'}  ${r.toFixed(2)}:1  ${f} on ${b}`); }
  console.log(`${name} — non-text UI (needs 3:1 where it conveys state or is a control boundary)`);
  for (const [f, b] of nontext) { const r = ratio(t[f], t[b]); console.log(`${r >= 3 ? 'pass' : 'info'}  ${r.toFixed(2)}:1  ${f} vs ${b}`); }
}

// Engineer avatars: initial letter on the hue tint (decorative; the name is always shown).
const hsl = (h, s, l) => {
  s /= 100; l /= 100;
  const k = (n) => (n + h / 30) % 12, a = s * Math.min(l, 1 - l);
  const f = (n) => l - a * Math.max(-1, Math.min(k(n) - 3, Math.min(9 - k(n), 1)));
  return '#' + [f(0), f(8), f(4)].map((x) => Math.round(x * 255).toString(16).padStart(2, '0')).join('');
};
let minDay = 99, minNight = 99;
for (let h = 0; h < 360; h += 5) {
  minDay = Math.min(minDay, ratio(hsl(h, 45, 26), hsl(h, 42, 90)));
  minNight = Math.min(minNight, ratio(hsl(h, 45, 90), hsl(h, 28, 30)));
}
console.log(`\nAvatar initials across all hues: day min ${minDay.toFixed(2)}:1, night min ${minNight.toFixed(2)}:1`);
