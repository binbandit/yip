# yip web client · Design system

This describes the design system in `web/`, which implements the behaviour in `docs/spec/02-product-and-design.md`. The visual language follows **Buzz by Block** at the owner's direction ([ADR 0012](../decisions/0012-visual-direction.md)); it replaces the spec's aqua/petrol concept, which has been removed. No Buzz or Block assets, names or exact gradient values are used (see [Borrowed from Buzz](#borrowed-from-buzz-and-what-differs)).

## Principles in the interface

- **The frame and the work.** One tinted gradient (warm at the top, cool at the bottom) is painted behind the window, and the sidebar sits directly on it. One opaque white work card, inset 8px, holds everything you work in. Panes inside the card are split by hairlines. We don't nest cards inside cards, and we don't put every message in a bubble.
- **Monochrome by default.** The accent is the ink colour itself: primary buttons, count pills, the send button, tab underlines and focus rings are near-black by day and near-white by night. Hue is spent only on status — green for confirmed success, red for failure, amber for something waiting on you.
- **People first.** Names and engineering roles lead. Provider, model and billing appear only in a run's details and on Machines.
- **Truthful state.** Every state is shown as a word plus a shape, never by colour alone. The client never implies progress, delivery, approval or a merge that the hub hasn't confirmed.
- **Quiet by default.** Unread rooms are shown in heavier text rather than a different colour. Engineer activity stays out of the timeline: a quiet line sits by the composer, and tool logs are under Activity. Only meaningful events are announced to screen readers.
- **A group chat, not a console.** Rooms read like a conversation between colleagues: names and messages, a typing line while someone composes a reply, and a compact card when work finishes. Engineers' intermediate output, tool calls and progress never stream into the conversation; a piece of work or a review is linked once, where it first comes up; and hub notices are single, human sentences with the technical detail kept in the work itself.

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
- Motion lasts 100–240ms with `cubic-bezier(0.25, 1, 0.5, 1)` and responds to user actions: panels slide in, dialogs pop, the new-messages pill rises, and a linked message flashes. The only looping motion is the composer's "typing" dots while someone composes a reply. `prefers-reduced-motion` disables all motion.

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
  - a quiet elapsed-time pill ("3m", "3m (2)") while engineers are working in the room, with the names in its tooltip (it does not animate);
  - a lock instead of # for private rooms.

  Selection is a grey wash, never a colour. Every marker has a visually hidden text equivalent. The demo notice and any connection problem ("Reconnecting…") sit in a slim line above the card.
- **Room header:** one 52px row — `# name` (a lock for private rooms) with the purpose in muted text, then outline buttons for linked projects, the reply mode ("Mentions only" or "Mira answers", with the full sentence in its tooltip and accessible name), and members — stacked squircles with the names of who can answer ("Oren, Mira"), falling back to a count when the room is narrow; roles are in the tooltip and accessible name — and a settings button.
- **Work strip:** one row per logical live or failed assignment (child work stays in its details) (finished work is announced by its result card in the conversation instead). Each row shows:
  - a state word plus shape: hollow circle for queued, bar for running, pause for waiting (the word is the waiting reason), neutral pause for In review, filled check for completed, triangle for failed;
  - the title;
  - the handoff from the actual current review round ("Mira building · Oren review requested", "reviewing", "requested changes" or "approved"); an old-version verdict says it belongs to an earlier revision;
  - the last confirmed activity with a relative time, or the exact blocker text;
  - the machine;
  - the latest attempt's state when it adds something (`WorkRow.runState`). `unknown` replaces the state word with "Not confirmed" and says the machine stopped reporting.

  Clicking a row opens the job drawer. "Add to this" scopes the composer to that job. The strip collapses to three rows plus "Show all N".
- **Message row:**
  - Engineers have **squircle** avatars and the human has a **circle**; shape is the only distinction.
  - The first message of a group shows the name, the engineer's role in quiet text ("Mira · Platform engineer"), and the time. Consecutive messages from the same author within 10 minutes compact, so the role appears once per group rather than on every message.
  - A question asked of you sits on a soft amber wash until it is answered.
  - A job, review, PR, decision or file is linked with a chip only on the first message that mentions it in the view; later messages about the same work stay plain.
  - Full commit hashes in message text show as their short form, with the whole hash on hover.
  - Hovering or focusing a row shows an action pill (react, reply in thread, copy link, and edit or remove on your own messages).
  - A thread summary shows reply avatars and "N replies · last reply 5m ago".
  - Kinds are rendered distinctly: `question` is an ordinary message with "Pip asked you" or "Answered" (and Reply in thread until it has replies); `approval` is the inline exact-action card; `result` is the result card; `review` links the review once; one-line `status` messages are small centred notices, like a group chat's, while longer hub answers show as "yip · From the work ledger".
  - A centred day pill and a monochrome hairline "New" divider (the read position when you opened the room) mark time.
- **Room voice:** short acknowledgments, meaningful developments once, and completion led by the outcome and anything unresolved. Engineers do not recite work IDs, UTC ledger timestamps or scheduling steps. Completion does not imply merge or deployment.
- **Composer:**
  - The mention **combobox** (`role=combobox`, `aria-activedescendant`, arrow keys, Enter/Tab, Escape) lists room members with their role. Engineers outside the room are listed but unavailable ("Not in this room — add them in room settings first").
  - Only choices made from the list become structured mentions. Mentions inside code are ignored, and "Asking Mira, Oren" confirms who will be addressed.
  - With one open question, **Answering Mira's question** targets the answer explicitly. **Not an answer** applies to the next sent message only; the answer target returns afterwards. A mention alone never resolves a question.
  - A project context picker.
  - Enter or Mod+Enter per the owner's preference, and IME-safe.
  - A draft per room and per thread, saved on this device.
  - An optimistic send that reconciles by `clientKey`. A failed send shows "Not sent · Retry · Edit · Discard", and Retry reuses the same key.
  - An offline notice: "Can't reach your workspace. Your draft is saved on this device."
  - The steering scope ("Adding to: Fix Atlas session expiry · Mira") is shown as a bar at the top of the composer and can be cleared with × or Escape. Its receipt reads, in turn, "Delivering to Mira…" → "Mira received your update" or "Queued for Mira's next step", driven only by `input.delivery`.
  - One quiet line under the box, where a group chat shows typing: "Mira is typing…", "Mira and Oren are typing…", "Pip is working on Document Beacon's request flow", or "Pip will reply when possible — No machines are paired yet". Receipts for your updates take the same line. Keyboard hints are for screen readers only. Engineers' streamed text is never shown as it arrives.
- **Result card:** posted like an attachment. It shows the title and state, then one summary line ("3 files +28 −5 · go test ./... passed · approved by Oren") in which every claim opens its evidence: the files open the diff, a check opens its recorded command, exit status and log, a verdict opens the review, and a PR opens its facts. Anything missing is stated plainly, and **Inspect the work** opens the drawer. **Details** expands the full evidence in place: the revision's summary and earlier revisions, every check on the exact current revision (each opening the evidence), each reviewer's verdict on an exact revision beside earlier verdicts ("Oren approved a9002d3 · requested changes on b0e5d19 first"), the revision and branch, the PR's facts kept separate, and the machine.
- **Approval card:** the exact action (command, target, scope, revision, reason, expiry), with Allow this push (or the equivalent) and Reject. The request's `version` is sent, and a 409 is explained. After a decision, a compact Allowed, Rejected, Expired or "Allowed and used" entry replaces the command body; **View request and outcome** reopens the exact action and outcome. Pending requests keep all detail. It states that a chat reply does not grant permission.
- **Job drawer:**
  - A header with the state and waiting reason, the exact blocker, an "outcome not confirmed" notice for unknown runs, missing evidence, the owner, reviewers, machine, revision, last confirmed activity and the source link.
  - **Stop** (confirmed; stops the job tree), **Retry** (for failed, recovery or stalled jobs, or an unknown run), **Accept revision abc1234** or **Accept document abc1234** (only when `requiresHumanReview && review_ready`; sends the exact revision and version, and handles 409), and Add to this work.
  - Tabs:
    - **Evidence:** a revision picker labelled with each revision's file and line counts (from `JobDetail.revisions`), the chosen revision's diff with per-file headers, line numbers and +/− lines, plus checks with inline logs, files, decisions, your updates with their receipts, questions and permissions, and related work.
    - **Review:** rounds in order, each giving the verdict on its exact Git revision or document hash, with a link to the reviewed document; findings with severity, file:line, evidence and author replies; superseded rounds marked; a stale approval explained; the PR shown as peer review / remote reviews / remote checks / merge.
    - **Activity:** the job timeline, plus per-run tool logs in collapsed `<details>` that load on open.
    - **Runs:** each attempt with its state (unknown reads "Outcome not confirmed"), and the provider, model and billing — the only place these appear.
- **Overview:** "Since you were here" summarizes changed assignments from current ledger facts, with one entry per assignment and explicit open questions, permission requests and revision-bound review outcomes. Evidence links open the work, question, review or decision as well as the source conversation. It never marks rooms read or creates another status stream. Unchanged open work remains in the live ledger below, then Needs a look (with the exact blocker), Active, Recently completed and Worth remembering. The visit is recorded (`POST /v1/overview/seen`) only after the page has rendered. A row whose latest attempt's `runState` is `unknown` shows "Not confirmed" rather than a normal state. The Overview conversation is docked at ≥1180px and linked below that.
- **Engineer profile:** role and versioned standing instructions, active and queued work, decisions they recorded, rooms, **projects they can work on** (from the projects' grants, in plain words: "Atlas · can change code · can push"), and provider preference.
- **Review states:** "Changes requested" is an engineering state, not an alert: it uses the pause shape in neutral ink, like any other waiting state, and never red.
- **Machines:** a compact list, then details on demand (see `docs/screenshots/machines-redesign/`).
    - **List:** the 24px title, one purpose sentence and one primary **Add machine**, then aligned rows on the main surface (no nested cards). Each row has a device icon and the machine name (16px), its OS and architecture (12px), and three separate facts: **connection** in words and a shape with the last-confirmed time ("Offline · Last heard 3h ago", "Not responding"), **work** ("Idle", "2 running", linking to the work, or "New work paused"), and **providers and limits**: one line per provider ("Claude Code · Needs sign-in") plus only the limitations that change what the machine can do: low disk, sign-in required, an allowance pause, read-only reviews unavailable, an untested version, a missing execution profile, and a temporary session. A trailing **Details** button (in one aligned column) opens the machine. When the list is wider than 780px the row is a four-column grid under a quiet header; narrower, the facts stack under the name. Rows use 12–16px padding and restrained dividers.
    - **Details** open in the right panel (inline beside the list when there's room, overlaid otherwise), titled with the machine and its connection, and are organized by purpose in four tabs; a tab with something needing attention carries a small dot. Arrow keys move between tabs.
        - **Overview:** "What limits its work" (each limitation links to the tab that explains it), connection with its effect on work (an offline machine is never called asleep; yip says it can't tell why), current work, new work (**Pause new work** / **Resume new work**; the confirmation says current work finishes), capacity (machine slots, kept distinct from the per-account limit under Connections), free disk, and whether it runs as a background service or a temporary session that stops when its terminal closes (with the supported `yip service install runner` command), then the consequential actions: **Stop current work** and **Revoke access**, each its own confirmed action with its loss explained.
        - **Connections:** one concise row per provider (name, sign-in state, account, billing) that expands to its explanation, availability, read-only support, version compatibility and the account's shared concurrency setting. A sign-in command sits next to the provider that needs it, with **Check sign-in again** on a connected machine.
        - **Storage:** workspaces as left-aligned rows with the action in a trailing column: kind in words (working checkout, review snapshot, conversation scratch space), its work, size (measured, "at least" when the count stopped early, or "size unknown", never a false 0), in-use or protected state and unpublished or uncommitted changes. Cleanup lives only here, confirmed, and says what is lost.
        - **Diagnostics:** runner and provider versions, execution profiles with the reason one is unavailable, each adapter's full limitations, toolchains as a name/version list, and the fingerprint with a copy button.
- **Dialogs:** these use native `<dialog>` with `showModal` (so the rest of the page is inert), a stacked focus trap, and Escape through the layer stack. Focus returns to the invoker. On phones they appear as bottom sheets.

## Interaction and accessibility

- Landmarks are the navigation ("Workspace"), `main#main`, and on phones the header, and there is a skip link. The right panel is a `complementary` region when inline and a `dialog` otherwise.
- The **layer stack** (`web/src/lib/ui/layers.ts`) means Escape closes only the topmost layer (a menu, emoji palette, dialog, drawer or sheet) and returns focus to the control that opened it. Focus traps stack, so a confirmation dialog over an overlaid drawer traps correctly.
- Message history is a labelled region. Up and Down (or j and k) move a roving focus between messages. Each message's actions become visible and reachable when it's focused, with no hover needed. "Load earlier messages" is a real button, and scrolling up also triggers it.
- Live regions: a single polite announcer handles new messages from others in the room you're viewing, receipts and confirmations, and a polite toast region reports errors. Engineers' streamed text is never rendered, so there's no token-by-token speech.
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

### Conversation verification (26 September 2026)

The scripted browser journey covers assignment, clarification, a genuine
question, owner answer, peer requested changes, author correction, re-review,
completion and recall from another permitted room, followed by Overview
review evidence opened with Enter. It needs no owner relay or mandatory
acceptance. [Room comparisons](../screenshots/team-conversation/README.md)
cover 390, 900, 1280 and 1440px in both themes. See the
[release checklist](../release-checklist.md) for counts and the separate
real-provider checks still reserved for the owner.
