import { defineTheme } from '@astryx-svelte/core/theme';
import { neutralTheme } from '@astryx-svelte/theme-neutral';
import { yipTokens } from './theme-tokens';

const sans = "ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif";

// Astryx draws control edges (and a switch's off track) with its hairline
// token, which is too faint to find a control by (WCAG 1.4.11 wants 3:1), so
// these controls get yip's edge instead.
const controlEdge = { base: { '--color-border-emphasized': 'var(--yip-control-edge)' } };
const controls = ['text-input', 'text-area', 'selector', 'file-input', 'checkbox-indicator', 'radio-indicator', 'switch'];

/**
 * yip on Astryx: the neutral theme's monochrome accent (ink-coloured primary
 * controls, hue spent only on status), set in self-hosted Inter, plus the
 * warm-to-cool frame painted behind the work card (ADR 0012).
 *
 * `--yip-*` tokens are yip's own; everything else is an Astryx token, so
 * components and app CSS read one vocabulary that follows day and night.
 */
export const yipTheme = defineTheme({
  name: 'yip',
  extends: neutralTheme,
  typography: {
    scale: { base: 14, ratio: 1.2 },
    body: { family: 'Inter Variable', fallbacks: `Inter, ${sans}` },
    heading: { family: 'Inter Variable', fallbacks: `Inter, ${sans}`, weights: { 3: 'semibold', 4: 'semibold' } },
    code: { family: 'ui-monospace', fallbacks: "'SF Mono', SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace" },
  },
  tokens: yipTokens,
  components: Object.fromEntries(controls.map((name) => [name, controlEdge])),
});
