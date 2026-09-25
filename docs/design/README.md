# yip web client · Design system

This describes the design system in `web/`, which implements the behaviour in `docs/spec/02-product-and-design.md`. The visual language follows **Buzz by Block** at the owner's direction ([ADR 0012](../decisions/0012-visual-direction.md)); it replaces the spec's aqua/petrol concept, which has been removed. No Buzz or Block assets, names or exact gradient values are used (see [Borrowed from Buzz](#borrowed-from-buzz-and-what-differs)).

## Principles in the interface

- **The frame and the work.** One tinted gradient (warm at the top, cool at the bottom) is painted behind the window, and the sidebar sits directly on it. One opaque white work card, inset 8px, holds everything you work in. Panes inside the card are split by hairlines. We don't nest cards inside cards, and we don't put every message in a bubble.
- **Monochrome by default.** The accent is the ink colour itself: primary buttons, count pills, the send button, tab underlines and focus rings are near-black by day and near-white by night. Hue is spent only on status — green for confirmed success, red for failure, amber for something waiting on you.
- **People first.** Names and engineering roles lead. Provider, model and billing appear only in a run's details and on Machines.
- **Truthful state.** Every state is shown as a word plus a shape, never by colour alone. The client never implies progress, delivery, approval or a merge that the hub hasn't confirmed.
- **Quiet by default.** Unread rooms are shown in heavier text rather than a different colour. Engineer activity stays out of the timeline: a quiet line sits by the composer, and tool logs are under Activity. Only meaningful events are announced to screen readers.

## Tokens

These are defined on `:root` in `web/src/app.css`. Night values apply under `prefers-color-scheme: dark` unless the owner picks Day, and always under `[data-theme="night"]`.

| Token | Day | Night | Use |
|---|---|---|---|
| `canvas` | `#EBE9D8` | `#33311D` | Frame gradient, top (warm) |
| `navigation` | `#D5DCE3` | `#0E1621` | Frame gradient, bottom (cool) |
| `surface` | `#FFFFFF` | `#1A1A1B` | The work card, panels, dialogs |
| `surface-subtle` | `#F4F4F5` | `#232325` | Hovered rows, code, card headers |
| `ink` | `#1F2328` | `#E6E8EB` | Main text |
| `ink-secondary` | `#59636E` | `#9BA3AD` | Metadata, secondary copy |
| `accent` | `#1F2328` | `#EEF0F2` | Primary controls, count pills, focus ring — the ink colour (monochrome) |
| `accent-ink` | `#FFFFFF` | `#151516` | Text on an accent fill |
| `line` / `line-strong` | `#E4E5E7` / `#D3D6DA` | `#313235` / `#3D3F43` | Hairlines / outline buttons and chips |
| `attention-fill` | `#D4A72C` | `#BB8009` | Amber markers (never text) |
| `attention-ink` | `#7A4F00` | `#E3B341` | Text on amber surfaces |
| `success` | `#1A7F37` | `#3FB950` | Confirmed success |
| `danger` | `#CF222E` | `#F85149` | Failures, destructive actions |
| `info` | `#0969DA` | `#4493F8` | Text selection only |

Derived tokens:

| Token | Day | Night | Use |
|---|---|---|---|
| `accent-subtle` | `#EFF0F2` | `#2A2B2E` | Selected option, active search result |
| `success-subtle` / `danger-subtle` / `attention-subtle` | `#E6F6EA` / `#FDEEEE` / `#FDF6D8` | `#15291D` / `#2F1719` / `#2B2411` | Diff lines, notices, a question asked of you |
| `control-edge` | `#858D97` | `#7A828C` | Borders of text inputs (3:1 against the surface) |
| `hover` / `pressed` / `selected` | black 4% / 8% / 7% | white 6% / 10% / 13% | Row states: alpha washes, never a new hue |
| `frame` | `canvas → navigation` | `canvas → navigation` | The two-stop vertical frame gradient |

Engineers' avatars use `hsl(engineer.hue …)`: day 34% saturation and 91% lightness for the tint with a 27% initial; night a 26% tint with an 88% initial.

### Contrast checks (WCAG 2.2 AA)

`node web/scripts/contrast.mjs` recomputes these. Every text pair we actually use passes 4.5:1.

| Pair | Day | Night |
|---|---|---|
| ink on surface / canvas / navigation | 15.80 / 12.92 / 11.42 | 14.17 / 10.71 / 14.81 |
| ink-secondary on surface / surface-subtle / canvas | 6.11 / 5.56 / 5.00 | 6.82 / 6.15 / 5.15 |
| Sidebar text (ink at 72%) on the frame, top / bottom | 5.69 / **5.28** | 6.37 / 8.07 |
| accent-ink on accent (buttons, count pills) | 15.80 | 15.97 |
| attention-ink on attention-subtle | 6.57 | 7.92 |
| success on surface | 5.08 | 6.85 |
| danger on surface / surface-subtle | 5.36 / 4.87 | 5.19 / 4.68 |
| Avatar initial on its hue tint (worst hue) | 5.38 | 6.62 |

Non-text contrast (3:1): the focus ring is ink (15.8:1 day, 15.2:1 night). Text-input borders measure 3.36 day and 4.47 night. Outline buttons and chips use the lighter `line-strong`, because their label identifies them. Hairlines only separate content that is already grouped.

## Type

- **Inter** (variable, self-hosted via `@fontsource-variable/inter`) for the whole UI, including the wordmark. Contextual ligatures are off and weights are never synthesised.
- The system monospace stack (`ui-monospace, SF Mono, Menlo…`) for commands, SHAs, paths and diffs.
- Scale: 12px metadata and timestamps · 14px/20px messages, navigation and controls · 16px room titles · 24px screen titles. Uppercase labels are 11px with 0.04em tracking and repeat information found elsewhere. On touch devices editable fields are 16px.
- Names and titles use 600 with tight tracking (−0.015 to −0.025em); pills and meta use 500; body text 400.

Font licence: Inter is under the SIL Open Font License 1.1. The licence ships in `node_modules/@fontsource-variable/inter/LICENSE`, and the built woff2 files are the OFL font, unmodified.

## Space, radius, motion

- Spacing is on a 4px base: 4px message row padding, 12px between message groups (6px when compact), 32px between major sections.
- Radii: 8px for sidebar rows and small buttons, 10px for buttons and inputs, 12px for popovers and menus, 14px for artifacts (result cards), 16px for the work card, message hover washes, the composer, code blocks, dialogs and the search palette. Small interactive items (reactions, count pills, chips, the send button, the action bar) are full pills.
- Motion lasts 100–240ms with `cubic-bezier(0.25, 1, 0.5, 1)` and responds to user actions: panels slide in, dialogs pop, the new-messages pill rises, and a linked message flashes. The only looping motion is the room's "working" pill, which pulses gently. `prefers-reduced-motion` disables all motion.

## Layout

| Width | Layout |
|---|---|
| ≥1200px | Sidebar 272px (240px below 1100px), the work card, and an **inline** right panel. The panel defaults to 380px and can be resized with a pointer or the keyboard (a separator with arrow keys, Home and End); the width is remembered. Detail drawers (job, review) are at least 480px wide. The Overview docks its conversation in a 400px column. |
| 760–1199px | The right panel **overlays** the conversation instead of crushing it. It is modal: the rest of the page is inert and focus is trapped. |
| <760px | One primary surface. A "Rooms and navigation" button opens the sidebar as a modal sheet. Threads and drawers are full-screen views with a Back button. The composer respects the bottom safe area, and there is no horizontal page scroll. |

The message column is capped at about 76ch plus the avatar gutter, and content stays left-aligned. There is no top bar on wide screens; pane headers inside the card are 52px. On phones a 52px bar holds the menu button, the mark and search.

## Components and their states

- **Sidebar** (on the frame, no box of its own): the monochrome branching-y mark and wordmark; a "Search everything ⌘K" launcher; Overview, Engineers, Projects and Machines; Rooms and Direct messages with sentence-case labels and a + to create; and a footer with machine health ("1 machine connected · work continues when you close this") and a profile card (name and workspace) whose menu has Settings, Day/Night appearance and Sign out. Room rows show:
  - read rows at reduced opacity, unread rows in bold;
  - mentions as a black count pill;
  - a pencil when a draft is saved;
  - a pulsing elapsed-time pill ("3m", "3m (2)") while engineers are working in the room, with the names in its tooltip;
  - a lock instead of # for private rooms.

  Selection is a grey wash, never a colour. Every marker has a visually hidden text equivalent. The demo notice and any connection problem ("Reconnecting…") sit in a slim line above the card.
- **Room header:** one 52px row — `# name` (a lock for private rooms) with the purpose in muted text, then outline buttons for linked projects, the reply mode ("Mentions only" or "Mira answers", with the full sentence in its tooltip and accessible name), and members (stacked squircles and a count; names and roles in the tooltip and accessible name), and a settings button.
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
  - The first message of a group shows the name, the role in muted text, a middot and the time. Consecutive messages from the same author within 10 minutes compact.
  - A question asked of you sits on a soft amber wash.
  - Hovering or focusing a row shows an action pill (react, reply in thread, copy link, and edit or remove on your own messages).
  - A thread summary shows reply avatars and "N replies · last reply 5m ago".
  - Kinds are rendered distinctly: `question` is an ordinary message with "Pip asked you" or "Answered" and Reply in thread; `approval` is the inline exact-action card; `result` is the result card; `review` shows View review chips; one-line `status` messages are quiet single lines, while longer hub answers show as "yip · From the work ledger".
  - A centred day pill and a monochrome hairline "New" divider (the read position when you opened the room) mark time.
- **Composer:**
  - The mention **combobox** (`role=combobox`, `aria-activedescendant`, arrow keys, Enter/Tab, Escape) lists room members with their role. Engineers outside the room are listed but unavailable ("Not in this room — add them in room settings first").
  - Only choices made from the list become structured mentions. Mentions inside code are ignored, and "Asking Mira, Oren" confirms who will be addressed.
  - A project context picker.
  - Enter or Mod+Enter per the owner's preference, and IME-safe.
  - A draft per room and per thread, saved on this device.
  - An optimistic send that reconciles by `clientKey`. A failed send shows "Not sent · Retry · Edit · Discard", and Retry reuses the same key.
  - An offline notice: "Can't reach your workspace. Your draft is saved on this device."
  - The steering scope ("Adding to: Fix Atlas session expiry · Mira") is shown as a bar at the top of the composer and can be cleared with × or Escape. Its receipt reads, in turn, "Delivering to Mira…" → "Mira received your update" or "Queued for Mira's next step", driven only by `input.delivery`.
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

- Landmarks are the navigation ("Workspace"), `main#main`, and on phones the header, and there is a skip link. The right panel is a `complementary` region when inline and a `dialog` otherwise.
- The **layer stack** (`web/src/lib/ui/layers.ts`) means Escape closes only the topmost layer (a menu, emoji palette, dialog, drawer or sheet) and returns focus to the control that opened it. Focus traps stack, so a confirmation dialog over an overlaid drawer traps correctly.
- Message history is a labelled region. Up and Down (or j and k) move a roving focus between messages. Each message's actions become visible and reachable when it's focused, with no hover needed. "Load earlier messages" is a real button, and scrolling up also triggers it.
- Live regions: a single polite announcer handles new messages from others in the room you're viewing, receipts and confirmations, and a polite toast region reports errors. Streaming previews are **not** live regions, so there's no token-by-token speech.
- The focus ring is 2px of ink with a 2px offset. Controls and rows have 44px effective targets on coarse pointers.
- State is never shown by colour alone. Shapes and words carry it, diff lines have +/− signs, and unread rooms also get visually hidden text.

## Borrowed from Buzz, and what differs

These cues were adopted from our study of the Buzz desktop client:

1. **Tinted chrome framing one opaque card** (`rounded-2xl`, a hairline edge), with panes split by hairlines rather than nested cards. yip uses its own warm-to-cool tint.
2. **A monochrome accent**, with hue only for status, so an agent-heavy screen stays calm.
3. **Search, navigation and the profile in the sidebar**; no top bar.
4. **Unread shown by weight, not colour**, black count pills for mentions, and an elapsed-time "working" pill on room rows.
5. **Squircle avatars for engineers and a circle for the human.** Almost every author here is an engineer, so the one human stands out by shape alone. The role takes the metadata slot after the name.
6. **Message rows** with a soft 16px hover wash, a floating action pill, 10-minute grouping and a thread summary row.
7. **A floating composer** (16px radius, translucent) with ghost toolbar buttons and a round send button.
8. **One right-hand panel shell** for the thread and every detail drawer, resizable and inline or overlaid by width.
9. **Activity kept out of the timeline**: a quiet status line near the composer, with tool logs under a drawer's Activity tab as collapsed rows.
10. **⌘K search** in a top-anchored palette over a blurred veil.

Where yip differs, and why:

- **No Buzz brand material:** no bee, no Buzz names, no exact Buzz gradient values. yip keeps its own branching-y mark, drawn monochrome.
- **Approvals are actionable** inline, with exact-action semantics and version checks. Buzz's are read-only.
- **No "Needs you" inbox.** Questions stay in their rooms as ordinary messages. This is a product principle from the spec.
- **Steering receipts and truthful delivery** have no Buzz equivalent. They come from spec journey D and acceptance test A08.
- **Review truth:** revision-bound verdicts, superseded rounds, and PR facts kept separate (A38–A44).
- **Accessibility first:** sidebar text is 72% ink rather than Buzz's 40% so it passes AA on the frame, and text inputs keep a 3:1 border.
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
