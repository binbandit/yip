# 0013 - The web client is built on Astryx

**Decision.** At the owner's direction (28 September 2026), the web client's
components come from [astryx-svelte](https://github.com/devrohit06/astryx-svelte)
(`@astryx-svelte/core`), a Svelte 5 port of Meta's Astryx design system,
instead of yip's hand-rolled primitives. Buttons, fields, menus, dialogs,
tabs, badges, tokens, avatars, toasts, code blocks, the app shell and
the side navigation are Astryx components. yip keeps what is specific to its
domain (the state shapes, the diff renderer, the composer's structured
mentions, the right-hand panel shell) and two things Astryx does differently
on purpose:

- **Message text** stays on yip's markdown-lite renderer. Astryx's `Markdown`
  is a full parser, so chat text such as `web/__tests__/x.ts` or `5 * 3 and
  2 * 4` turns into emphasis, and it labels every message a document.
- **Notices and form errors** use yip's small `Notice` rather than Astryx's
  `Banner`: a status shape and a sentence, with no tinted box (the design's
  rule for inline status), and a live region only when it should be. `Banner`
  is always one, so a standing notice would interrupt a screen reader every
  time its view opened.

The look is a yip theme (`web/src/lib/theme.ts`) that extends Astryx's
**neutral** theme: its monochrome accent and status palette, set in the
self-hosted Inter, plus yip's warm-to-cool frame behind one inset work card.
So [0012](0012-visual-direction.md)'s composition still holds (frame, card,
monochrome accent, hue only for status, sidebar navigation, rounded-square
engineers and a circle human); its exact hex values and radii are now the
theme's.

**How.**

- `app.css` imports Astryx's `base.css` (the reset and cascade layer order)
  and its pre-built `astryx.css`, so no StyleX compiler runs in yip's build.
  yip's own global CSS sits in the `product` layer, above Astryx's layers and
  below component-scoped CSS, except a fallback focus ring for yip's own links
  and buttons, which sits in `reset` so every Astryx component keeps its own. Component-scoped CSS reads Astryx tokens only
  (`--color-*`, `--spacing-*`, `--radius-*`, …); yip-specific tokens are
  prefixed `--yip-`.
- The theme is defined at runtime with `defineTheme({ extends: neutralTheme })`
  and injected by `<Theme>`. yip is a client-only SPA and the hub's CSP allows
  inline styles, so there is no build step for it.
- Icons are Lucide (`@lucide/svelte`, the neutral theme's icon set) rendered
  through Astryx's `Icon`.
- Overlays share Astryx's layer stack for Escape and focus. yip's own
  `lib/ui/layers.ts` is gone; the right panel joins the stack through
  `useFocusTrap` when it is modal.

**Why.** The owner asked for the codebase to use the library. A maintained
design system gives consistent, accessible components (focus management,
typeahead, overflow, RTL) that yip was re-implementing piecemeal.

**What still applies.** Everything in the spec about behaviour, content,
truthfulness and accessibility, as for 0012: WCAG 2.2 AA contrast (re-checked
against the theme's actual values by `web/scripts/contrast.mjs`), state never
shown by colour alone, 44px targets on coarse pointers. The neutral theme draws
field and checkbox edges (and a switch's off track) with a 1.5:1 hairline, so
the yip theme raises them to a 3:1 `--yip-control-edge` inside those controls.
