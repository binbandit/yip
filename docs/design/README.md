# yip web client · Design system

This describes the design system in `web/`, which implements `docs/spec/02-product-and-design.md`. The spec is authoritative; this file records how the client meets it. The visual direction is new. It borrows hierarchy and interaction ideas from **Buzz by Block** (see [Borrowed from Buzz](#borrowed-from-buzz-and-what-differs)), but no Buzz assets, colours, gradients or branding.

## Principles in the interface

- **The frame and the work.** A pale aqua frame holds the top bar and the sidebar. The one bright work card holds everything you work in. Panes inside the card are split by hairlines. We don't nest cards inside cards, and we don't put every message in a bubble.
- **People first.** Names and engineering roles lead. Provider, model and billing appear only in a run's details and on Machines.
- **Truthful state.** Every state is shown as a word plus a shape, never by colour alone. The client never implies progress, delivery, approval or a merge that the hub hasn't confirmed.
- **Quiet by default.** Unread rooms are shown in heavier text rather than a different colour. Engineer activity stays out of the timeline: a quiet line sits by the composer, and tool logs are under Activity. Only meaningful events are announced to screen readers.

## Tokens

These are defined on `:root` in `web/src/app.css`. Night values apply under `prefers-color-scheme: dark` unless the owner picks Day, and always under `[data-theme="night"]`.

| Token | Day | Night | Use |
|---|---|---|---|
| `canvas` | `#E9F2F3` | `#172C32` | Outer frame (top of the frame gradient) |
| `navigation` | `#D7E9EB` | `#203B43` | Sidebar (bottom of the frame gradient) |
| `surface` | `#FFFFFF` | `#21343B` | The work card, panels, dialogs |
| `surface-subtle` | `#F3F7F7` | `#29424A` | Hovered rows, code, supporting content |
| `ink` | `#183A43` | `#EDF5F5` | Main text |
| `ink-secondary` | `#526B72` | `#AFC6CC` | Metadata, secondary copy |
| `accent` | `#176274` | `#89CFDE` | Links, selection, primary controls, focus ring |
| `accent-ink` | `#FFFFFF` | `#173740` | Text on an accent fill |
| `line` | `#D7E2E4` | `#43606A` | Hairlines (decorative separators only) |
| `attention-fill` | `#F6DB7C` | `#705C24` | Small attention markers and fills. **Never used as text.** |
| `attention-ink` | `#57420D` | `#FFF0B8` | Text on attention surfaces |
| `success` | `#236A51` | `#8FD4B5` | Confirmed success |
| `danger` | `#AA3944` | `#FFABB1` | Failures, destructive actions |

These tokens are derived for this client:

| Token | Day | Night | Use |
|---|---|---|---|
| `accent-subtle` | `#E3EFF1` | `#2A4C55` | Selected option, steering scope, mention chips |
| `success-subtle` | `#EAF4EF` | `#26463D` | Added diff lines, success notices |
| `danger-subtle` | `#FBEFF0` | `#4A3238` | Removed diff lines, failed sends |
| `attention-subtle` | `#FDF6DC` | `#3C3A28` | Blockers, mentions of you, search matches |
| `control-edge` | `#748E95` | `#7B9BA4` | Borders of inputs and buttons (3:1 against surface) |
| `hover` / `pressed` / `selected` | 5% / 9% / 8% ink | 6% / 10% / 11% white | Row states, tinted with alpha rather than new hues |
| `frame` | `canvas → navigation` | `canvas → navigation` | The two-stop vertical frame gradient |

Engineers' avatars use `hsl(engineer.hue …)`. Day uses 42% saturation and 90% lightness for the tint and 26% lightness for the initial. Night uses a 30% tint and 90% for the initial.

### Contrast checks (WCAG 2.2 AA)

`node web/scripts/contrast.mjs` recomputes these. Every text pair we actually use passes 4.5:1.

| Pair | Day | Night |
|---|---|---|
| ink on surface / canvas / navigation | 12.17 / 10.70 / 9.71 | 11.72 / 13.16 / 10.74 |
| ink-secondary on surface / surface-subtle | 5.67 / 5.25 | 7.27 / 5.97 |
| ink-secondary on canvas / navigation (sidebar) | 4.98 / **4.52** | 8.16 / 6.66 |
| accent on surface / navigation / accent-subtle | 6.92 / 5.52 / 5.89 | 7.44 / 6.81 / 5.32 |
| accent-ink on accent (buttons, count pills) | 6.92 | 7.27 |
| attention-ink on attention-fill ("New" divider) | 7.00 | 5.67 |
| attention-ink on attention-subtle (mentions of you) | 8.85 | 10.07 |
| success on surface / success-subtle | 6.46 / 5.75 | 7.57 / 6.05 |
| danger on surface / danger-subtle | 6.22 / 5.54 | 7.23 / 6.49 |
| Avatar initial on its hue tint (worst hue) | 5.69 | 5.52 |

Non-text contrast, which needs 3:1: the accent focus ring measures 6.92 day and 7.44 night against the surface. Control edges measure 3.47 day and 4.36 night against the surface, and 3.05 day against the canvas. The `line` hairlines (1.3:1) only separate content that is already grouped. They never define a control. The yellow `attention-fill` is used only as a marker next to text (the "New" divider, the mention stripe, the left edge of a blocker), never as text.

The closest pair is ink-secondary on the day navigation colour, at 4.52:1. Sidebar rows switch to `ink` on hover and when selected.

## Type

- **Hanken Grotesk** (variable, self-hosted via `@fontsource-variable/hanken-grotesk`) for the whole UI.
- **Bricolage Grotesque** 600 (self-hosted via `@fontsource/bricolage-grotesque`, Latin subset only) for the wordmark and the setup title only.
- The system monospace stack (`ui-monospace, SF Mono, Menlo…`) for commands, SHAs, paths and diffs.
- Scale: 13px metadata · 14px navigation and controls · 15px/1.55 messages · 18px room title · 26px screen titles (22px on phones). No essential text is smaller than 13px, except the 12–12.5px uppercase section labels and timestamps that repeat information found elsewhere. On touch devices editable fields are 16px.
- Names use a weight of 600–680. Message text uses 400.

Font licences: both families are under the SIL Open Font License 1.1. Their licence files ship in the npm packages (`node_modules/@fontsource-variable/hanken-grotesk/LICENSE`, `node_modules/@fontsource/bricolage-grotesque/LICENSE`), and the built woff2 files are the OFL fonts, unmodified.

## Space, radius, motion

- Spacing is on a 4px base: 8px inside small groups, 12–16px of row padding, 20px between message groups (12px when compact), and 32px between major sections.
- Radii: 8px for controls, 10px for artifacts (diffs, checks, result cards), 12px for popovers and message hover washes, and 16px for the work card, dialogs and the search palette. Small interactive items (reactions, count pills, chips) are full pills.
- Motion lasts 120–180ms with `cubic-bezier(0.25, 1, 0.5, 1)` and only responds to user actions: panels slide in, dialogs pop, the new-messages pill rises, and a linked message flashes when highlighted. The only looping motion is the small "running" bar, and `prefers-reduced-motion` disables all motion.

## Layout

| Width | Layout |
|---|---|
| ≥1200px | Sidebar 232px (200px below 1100px), the work card, and an **inline** right panel. The panel defaults to 336px and can be resized with a pointer or the keyboard (a separator with arrow keys, Home and End); the width is remembered. Detail drawers (job, review) are at least 480px wide. The Overview docks its conversation in a 400px column. |
| 760–1199px | The right panel **overlays** the conversation instead of crushing it. It is modal: the rest of the page is inert and focus is trapped. |
| <760px | One primary surface. A "Rooms and navigation" button opens the sidebar as a modal sheet. Threads and drawers are full-screen views with a Back button. The composer respects the bottom safe area, and there is no horizontal page scroll. |

The message column is capped at about 76ch plus the avatar gutter, and content stays left-aligned. The top bar is a fixed 52px.

## Components and their states

- **Top bar:** the branching-y mark with the lowercase wordmark, a search launcher (⌘K or Ctrl+K), a connection line shown only when it isn't live ("Reconnecting…", "Can't reach your workspace"), a day/night toggle, and a profile menu with Settings and Sign out.
- **Sidebar:** Overview; Rooms; Direct messages; Engineers, Projects, Machines; and a machine-health footer such as "1 machine connected · work continues when you close this". Room rows show:
  - unread as bold text;
  - mentions as a count pill;
  - a pencil when a draft is saved;
  - an animated bar with "Mira working" while a run is active in the room;
  - a lock for private rooms.

  Every marker has a visually hidden text equivalent.
- **Room header:** the room name, a private marker and the room's purpose, followed by a members pill (squircle avatars, names and roles) that opens settings, project chips, the reply mode in words ("Quiet — only mentioned engineers reply" or "Mira answers unaddressed messages"), and a settings button.
- **Work strip:** one row per live job, plus failed jobs and jobs completed within 24h. Each row shows:
  - a state word plus shape: hollow circle for queued, bar for running, pause for waiting (the word is the waiting reason), check for ready, filled check for completed, triangle for failed;
  - the title;
  - the handoff ("Mira building · Oren reviewing next");
  - the last confirmed activity with a relative time, or the exact blocker text;
  - the machine;
  - the latest attempt's state when it adds something (`WorkRow.runState`). `unknown` replaces the state word with "Not confirmed" and says the machine stopped reporting.

  Clicking a row opens the job drawer. "Add to this" scopes the composer to that job. The strip collapses to three rows plus "Show all N".
- **Message row:**
  - Engineers have **squircle** avatars and the human has a **circle**; shape is the only distinction.
  - The first message of a group shows the name, the role badge and the time. Consecutive messages from the same author within 10 minutes compact.
  - Messages that mention you get a yellow left stripe.
  - Hovering or focusing a row shows an action pill (react, reply in thread, copy link, and edit or remove on your own messages).
  - A thread summary shows reply avatars and "N replies · last reply 5m ago".
  - Kinds are rendered distinctly: `question` is an ordinary message with "Pip asked you" or "Answered" and Reply in thread; `approval` is the inline exact-action card; `result` is the result card; `review` shows View review chips; one-line `status` messages are quiet single lines, while longer hub answers show as "yip · From the work ledger".
  - Day dividers and a yellow "New" divider (the read position at the moment you opened the room) mark time.
- **Composer:**
  - The mention **combobox** (`role=combobox`, `aria-activedescendant`, arrow keys, Enter/Tab, Escape) lists room members with their role. Engineers outside the room are listed but unavailable ("Not in this room — add them in room settings first").
  - Only choices made from the list become structured mentions. Mentions inside code are ignored, and "Asking Mira, Oren" confirms who will be addressed.
  - A project context picker.
  - Enter or Mod+Enter per the owner's preference, and IME-safe.
  - A draft per room and per thread, saved on this device.
  - An optimistic send that reconciles by `clientKey`. A failed send shows "Not sent · Retry · Edit · Discard", and Retry reuses the same key.
  - An offline notice: "Can't reach your workspace. Your draft is saved on this device."
  - The steering scope ("Adding to: Fix Atlas session expiry · Mira") is highlighted with the accent edge and can be cleared with × or Escape. Its receipt reads, in turn, "Delivering to Mira…" → "Mira received your update" or "Queued for Mira's next step", driven only by `input.delivery`.
  - An activity line above the box ("Mira is replying…", or "Pip will reply when possible — No machines are paired yet").
- **Result card:** the state and title; missing evidence stated plainly; the number of changed files with +/− line counts and the revision's summary, from `JobDetail.revisions`; checks on the exact current revision (command, pass or fail, exit code), each opening the evidence; reviewers with their verdict on an exact revision beside earlier verdicts ("Oren approved a9002d3 · requested changes on b0e5d19 first"); the revision and branch; the PR's facts, kept separate; the machine; and **Inspect the work**.
- **Approval card:** the exact action (command, target, scope, revision, reason, expiry), with Allow this push (or the equivalent) and Reject. The request's `version` is sent, and a 409 is explained. The card shows Allowed, Rejected, Expired or "Allowed and used" truthfully. It states that a chat reply does not grant permission.
- **Job drawer:**
  - A header with the state and waiting reason, the exact blocker, an "outcome not confirmed" notice for unknown runs, missing evidence, the owner, reviewers, machine, revision, last confirmed activity and the source link.
  - **Stop** (confirmed; stops the job tree), **Retry** (for failed, recovery or stalled jobs, or an unknown run), **Accept revision abc1234** (only when `requiresHumanReview && review_ready`; sends the exact revision and version, and handles 409), and Add to this work.
  - Tabs:
    - **Evidence:** a revision picker labelled with each revision's file and line counts (from `JobDetail.revisions`), the chosen revision's diff with per-file headers, line numbers and +/− lines, plus checks with inline logs, files, decisions, your updates with their receipts, questions and permissions, and related work.
    - **Review:** rounds in order, each giving the verdict on its exact revision; findings with severity, file:line, evidence and author replies; superseded rounds marked; a stale approval explained; the PR shown as peer review / remote reviews / remote checks / merge.
    - **Activity:** the job timeline, plus per-run tool logs in collapsed `<details>` that load on open.
    - **Runs:** each attempt with its state (unknown reads "Outcome not confirmed"), and the provider, model and billing — the only place these appear.
- **Overview:** "Since you were here" catch-up with links to source conversations (it never marks rooms read), then Needs a look (with the exact blocker), Active, Recently completed and Worth remembering. The visit is recorded (`POST /v1/overview/seen`) only after the page has rendered. A row whose latest attempt's `runState` is `unknown` shows "Not confirmed" rather than a normal state. The Overview conversation is docked at ≥1180px and linked below that.
- **Machines:** connection state and provider state are kept separate. Asleep or offline machines are described with their last-seen time ("Offline since 10:42 — asleep, shut down or off the network"). Each machine shows its slots, disk pressure, execution profiles (with the reason one is unavailable), providers with sign-in state, billing, tested or untested version and limitations, the toolchains, the runner version and the fingerprint. **Drain**, **Stop its work** and **Revoke** are three distinct, confirmed actions.
- **Dialogs:** these use native `<dialog>` with `showModal` (so the rest of the page is inert), a stacked focus trap, and Escape through the layer stack. Focus returns to the invoker. On phones they appear as bottom sheets.

## Interaction and accessibility

- Landmarks are the header, the navigation ("Workspace") and `main#main`, and there is a skip link. The right panel is a `complementary` region when inline and a `dialog` otherwise.
- The **layer stack** (`web/src/lib/ui/layers.ts`) means Escape closes only the topmost layer (a menu, emoji palette, dialog, drawer or sheet) and returns focus to the control that opened it. Focus traps stack, so a confirmation dialog over an overlaid drawer traps correctly.
- Message history is a labelled region. Up and Down (or j and k) move a roving focus between messages. Each message's actions become visible and reachable when it's focused, with no hover needed. "Load earlier messages" is a real button, and scrolling up also triggers it.
- Live regions: a single polite announcer handles new messages from others in the room you're viewing, receipts and confirmations, and a polite toast region reports errors. Streaming previews are **not** live regions, so there's no token-by-token speech.
- The focus ring is 2px of accent with a 2px offset. Controls and rows have 44px effective targets on coarse pointers.
- State is never shown by colour alone. Shapes and words carry it, diff lines have +/− signs, and unread rooms also get visually hidden text.

## Borrowed from Buzz, and what differs

These cues were adopted from our study of the Buzz desktop client (hierarchy and interaction only):

1. **Tinted chrome framing one bright card** (`rounded-2xl`, a hairline edge), with panes split by hairlines rather than nested cards. We use it because it gives the "you work here" surface a clear edge while navigation recedes. The tint here is the spec's aqua canvas → navigation gradient.
2. **Unread shown by weight, not colour**, with a count pill for mentions and a quiet "working" indicator on room rows. These keep an agent-heavy sidebar calm.
3. **Squircle avatars for engineers and a circle for the human.** Almost every author here is an engineer, so the one human stands out by shape alone. The role badge takes the metadata slot, so no "Agent" label is repeated.
4. **A hover action pill** on messages, extended here to keyboard focus and touch.
5. **One right-hand panel shell** for the thread and every detail drawer, resizable and inline or overlaid by width.
6. **Activity kept out of the timeline**: a quiet status line near the composer, with tool logs under a drawer's Activity tab as collapsed rows.
7. **⌘K search** with grouped results and a top-anchored palette.

Where yip differs, and why:

- **No Buzz brand material:** no bee, no olive→steel gradient, and not Buzz's monochrome-black accent. yip's accent is petrol, and colour is used for status plus the one aqua frame.
- **Approvals are actionable** inline, with exact-action semantics and version checks. Buzz's are read-only.
- **No "Needs you" inbox.** Questions stay in their rooms as ordinary messages. This is a product principle from the spec.
- **Steering receipts and truthful delivery** have no Buzz equivalent. They come from spec journey D and acceptance test A08.
- **Review truth:** revision-bound verdicts, superseded rounds, and PR facts kept separate (A38–A44).
- **Time-grouped messages without bubbles.** We use Buzz's 10-minute grouping but not its 500ms blur-in arrival, to keep motion within 180ms.
- **No community rail, presence dots or theme gallery.** yip is one workspace with one human, and presence would imply a human is online.

## Files

- `web/src/app.css`: tokens, base styles and shared primitives (buttons, fields, chips, notices, tabs, prose).
- `web/src/components/`: the shell pieces, message rendering, the composer, cards and dialogs. `panels/` holds the right-panel views.
- `web/src/screens/`: one component per route.
- `web/src/lib/state/`: the pure event reducer (`data.ts`), the runes store (`app.svelte.ts`), the SSE client (`events.ts`), drafts (`drafts.ts`) and the detail cache.
  - On load the client reads `/v1/bootstrap`, then `GET /v1/runs` for active attempts. It opens the event stream at the bootstrap cursor, so there is no history replay.
  - A room loads `GET /v1/rooms/{id}/work?include=replies`. Live conversational replies feed the composer's activity line, and `runState` feeds the strip.
  - REST snapshots never turn a run that events have already finished back into a live one.
  - `message_delta` stream chunks are appended to the preview, which is replaced when the canonical `message.created` arrives.
- `web/src/lib/util/`: markdown-lite (which escapes all HTML), mentions, diff parsing, labels and time.
- `web/scripts/contrast.mjs`: the contrast checks above.

## Running and verifying

- `cd web && npm ci`, then `npm run dev`. This starts Vite on :5173 and proxies `/v1`, including the SSE stream, to `YIP_HUB` (default `http://127.0.0.1:7521`). Start the hub with `--allowed-origin http://localhost:5173` so state-changing requests from the dev origin pass the Origin check.
- `npm run check` runs svelte-check over the app and the tests, and fails on warnings. `npm run build` writes `web/dist` (keeping `dist/.gitkeep`) for the hub to embed.
- `npm test` runs the unit tests (reducer, mentions, markdown escaping, drafts, router, diff parsing) and the jsdom smoke suites. The smoke suites mount the real `App` against payloads captured from a demo hub (`tests/unit/fixtures`), including a full captured SSE stream.
- `npm run e2e` runs the Playwright journeys in `tests/e2e` against `bin/yip demo` (build it first with `make all`). They run only if a Playwright Chromium or a system Chrome or Edge is installed; the command never downloads a browser.
- `node scripts/contrast.mjs` recomputes the contrast table.
