# 0012 — Visual direction: Buzz-like, not the concept

> The composition below still holds. The components and exact token values
> are now Astryx's, through a yip theme: see [0013](0013-astryx-design-system.md).

**Decision.** At the owner's direction (25 September 2026), the web client's
visual system follows **Buzz by Block** rather than the product document's
concept. The interactive concept (`docs/spec/design-concept.html`) was
deleted, and section 5 of `02-product-and-design.md` (aqua frame, petrol
accent, golden markers, the "daylight workshop") no longer governs the look.

The system is described in [docs/design/README.md](../design/README.md):

- One tinted gradient painted behind the whole window (warm at the top,
  cool at the bottom), with the sidebar sitting directly on it and **one
  opaque white work card** inset beside it. Panes inside the card are split
  by hairlines.
- A **monochrome accent**: primary buttons, count pills, the send button,
  focus rings and tab underlines use the ink colour (near-black by day,
  near-white by night). Hue is spent only on status: green for confirmed
  success, red for failure, amber for anything waiting on the owner.
- **Inter** at 14/20 for messages, semibold names and titles with tight
  tracking, sentence-case section labels.
- Search, navigation (Engineers, Projects, Machines), rooms,
  direct messages, machine health and the profile menu all live in the
  sidebar; there is no top bar on wide screens. A room's header is a single
  52px row.
- Squircle engineers, circle human; roles as plain muted text after the
  name; a pulsing elapsed-time pill on rooms where an engineer is working.
- The brand mark keeps its branching-y shape but is drawn monochrome in the
  current ink.

**Why.** The owner asked twice for the design to follow Buzz and not the
concept ("Don't use the design concept… use Buzz by Block as good design
inspiration", then "make the UI look more like buzz and less like the
concept ui"). The first implementation borrowed Buzz's hierarchy but kept the
spec's concept palette, which is what made it read as the concept.

**What still applies.** Everything in the spec about behaviour, content,
truthfulness and accessibility: WCAG 2.2 AA contrast (re-verified with
`web/scripts/contrast.mjs`), state never shown by colour alone, 44px targets
on coarse pointers, and the layout breakpoints. No Buzz or Block brand
assets, names, or exact gradient values are used.
