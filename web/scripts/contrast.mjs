// Prints WCAG 2.2 contrast ratios for the token pairs the UI actually uses.
// Run: node scripts/contrast.mjs
const day = {
  canvas: '#EBE9D8', navigation: '#D5DCE3', surface: '#FFFFFF', 'surface-subtle': '#F4F4F5',
  ink: '#1F2328', 'ink-secondary': '#59636E', accent: '#1F2328', 'accent-ink': '#FFFFFF', line: '#E4E5E7',
  'attention-fill': '#D4A72C', 'attention-ink': '#7A4F00', success: '#1A7F37', danger: '#CF222E',
  'accent-subtle': '#EFF0F2', 'danger-subtle': '#FDEEEE', 'success-subtle': '#E6F6EA', 'attention-subtle': '#FDF6D8', 'control-edge': '#858D97',
};
const night = {
  canvas: '#33311D', navigation: '#0E1621', surface: '#1A1A1B', 'surface-subtle': '#232325',
  ink: '#E6E8EB', 'ink-secondary': '#9BA3AD', accent: '#EEF0F2', 'accent-ink': '#151516', line: '#313235',
  'attention-fill': '#BB8009', 'attention-ink': '#E3B341', success: '#3FB950', danger: '#F85149',
  'accent-subtle': '#2A2B2E', 'danger-subtle': '#2F1719', 'success-subtle': '#15291D', 'attention-subtle': '#2B2411', 'control-edge': '#7A828C',
};
// Sidebar labels are ink at 72% over the frame gradient; check both ends.
const mix = (fg, bg, a) => {
  const p = (h) => h.replace('#', '').match(/../g).map((x) => parseInt(x, 16));
  const [f, b] = [p(fg), p(bg)];
  return '#' + f.map((v, i) => Math.round(v * a + b[i] * (1 - a)).toString(16).padStart(2, '0')).join('');
};
for (const t of [day, night]) {
  t['sidebar-muted@canvas'] = mix(t.ink, t.canvas, 0.72);
  t['sidebar-muted@navigation'] = mix(t.ink, t.navigation, 0.72);
}
const lum = (hex) => {
  const c = hex.replace('#', '').match(/../g).map((h) => parseInt(h, 16) / 255)
    .map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
};
const ratio = (a, b) => { const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p); return (x + 0.05) / (y + 0.05); };
const text = [
  ['ink', 'surface'], ['ink', 'surface-subtle'], ['ink', 'canvas'], ['ink', 'navigation'], ['ink', 'accent-subtle'],
  ['ink-secondary', 'surface'], ['ink-secondary', 'surface-subtle'], ['ink-secondary', 'canvas'], ['ink-secondary', 'accent-subtle'],
  ['accent', 'surface'], ['accent', 'surface-subtle'], ['accent', 'canvas'], ['accent', 'navigation'], ['accent', 'accent-subtle'],
  ['accent-ink', 'accent'], ['attention-ink', 'surface'],
  ['success', 'surface'], ['success', 'success-subtle'], ['danger', 'surface'], ['danger', 'danger-subtle'], ['danger', 'surface-subtle'],
  ['attention-ink', 'attention-subtle'], ['ink', 'attention-subtle'], ['ink', 'success-subtle'], ['ink', 'danger-subtle'],
  ['sidebar-muted@canvas', 'canvas'], ['sidebar-muted@navigation', 'navigation'],
];
const nontext = [['accent', 'surface'], ['accent', 'canvas'], ['control-edge', 'surface'], ['attention-fill', 'surface'], ['line', 'surface']];
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
  minDay = Math.min(minDay, ratio(hsl(h, 45, 27), hsl(h, 34, 91)));
  minNight = Math.min(minNight, ratio(hsl(h, 45, 88), hsl(h, 26, 26)));
}
console.log(`\nAvatar initials across all hues: day min ${minDay.toFixed(2)}:1, night min ${minNight.toFixed(2)}:1`);
