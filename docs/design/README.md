# yip web client · Design system

This describes the design system in `web/`, which implements the behaviour in `docs/spec/02-product-and-design.md`. Components come from **Astryx** through [astryx-svelte](https://github.com/devrohit06/astryx-svelte), a Svelte 5 port of Meta's design system ([ADR 0013](../decisions/0013-astryx-design-system.md)). The look is a yip theme over Astryx's neutral theme. Its composition follows **Buzz by Block** at the owner's direction ([ADR 0012](../decisions/0012-visual-direction.md)); no Buzz or Block assets, names or exact gradient values are used (see [Borrowed from Buzz](#borrowed-from-buzz-and-what-differs)).

## Principles in the interface

- **The frame and the work.** One tinted gradient (warm at the top, cool at the bottom) is painted behind the window, and the sidebar sits directly on it. One opaque white work card, inset 8px, holds everything you work in. Panes inside the card are split by hairlines. We don't nest cards inside cards, and we don't put every message in a bubble.
- **Monochrome by default.** The accent is the ink colour itself: primary buttons, count pills, the send button, tab underlines and focus rings are near-black by day and near-white by night. Hue is spent only on status — green for confirmed success, red for failure, amber for something waiting on you.
- **People first.** Names and engineering roles lead. Provider, model and billing appear only in a run's details and on Machines.
- **Truthful state.** Every state is shown as a word plus a shape, never by colour alone. The client never implies progress, delivery, approval or a merge that the hub hasn't confirmed.
- **Quiet by default.** Unread rooms are shown in heavier text rather than a different colour. Engineer activity stays out of the timeline: a quiet line sits by the composer, and tool logs are under Activity. Only meaningful events are announced to screen readers.
- **A group chat, not a console.** Rooms read like a conversation between colleagues: names and messages, a typing line while someone composes a reply, and a compact card when work finishes. Engineers' intermediate output, tool calls and progress never stream into the conversation; a piece of work or a review is linked once, where it first comes up; and hub notices are single, human sentences with the technical detail kept in the work itself.

## Theme and tokens

The theme is `web/src/lib/theme.ts`: `defineTheme({ extends: neutralTheme })` with Inter and yip's own tokens (`web/src/lib/theme-tokens.ts`). `<Theme>` in `App.svelte` applies it and the owner's appearance (Match this device, Day or Night) as Astryx's `system`, `light` or `dark` mode. Every colour is a `[day, night]` pair resolved with `light-dark()`.

Components and app CSS use Astryx's tokens only (`cd web && npx @astryx-svelte/cli@0.5.2 docs tokens` lists them all). The ones yip leans on:

| Token | Day | Night | Use |
|---|---|---|---|
| `--yip-frame-top` / `--yip-frame-bottom` | `#EBE9D8` / `#D5DCE3` | `#33311D` / `#0E1621` | The frame gradient behind the sidebar and the card (yip's) |
| `--color-background-surface` | `#FFFFFF` | `#262626` | The work card, panels, dialogs |
| `--color-background-muted` | `#F1F1F1` | `#1B1B1B` | Code, quiet fills |
| `--color-text-primary` / `--color-text-secondary` | `#171717` / `#525252` | `#FAFAFA` / `#A3A3A3` | Main text / metadata |
| `--color-accent` / `--color-on-accent` | `#262626` / `#FFFFFF` | `#EBEBEB` / `#171717` | Primary controls, count pills: the ink colour |
| `--color-border` / `--color-border-emphasized` | black 8% / `#D4D4D4` | white 10% / `#525252` | Hairlines / stronger separators |
| `--yip-control-edge` | `#858585` | `#7A7A7A` | The edge of text fields, selects, file inputs, checkboxes and radios, and a switch's off track (yip's; the theme sets it as `--color-border-emphasized` inside those controls) |
| `--color-success`, `--color-error`, `--color-warning` | `#007004`, `#A50C25`, `#745B00` | `#9FE59B`, `#FFC6C1`, `#FDCF4F` | Status text and shapes |
| `--color-*-muted` | pastel washes | tinted washes | Diff lines, search matches, a question asked of you, a message that failed to send |
| `--yip-attention-fill` | `#D4A72C` | `#BB8009` | Amber markers (never text) (yip's) |
| `--color-overlay-hover` / `--color-overlay-pressed` | black 5% / 10% | white 5% / 10% | Row states: alpha washes, never a new hue |

Engineers' avatars tint Astryx's avatar fallback with `hsl(engineer.hue …)`: day 34% saturation and 91% lightness for the tint with a 27% initial; night a 26% tint with an 88% initial.

A profile picture (the owner's from Settings, an engineer's from Edit profile) replaces the initial and keeps the avatar's shape, cropped to fill it. Animated GIFs play; when reduced motion is preferred, every picture shows its first frame instead (`lib/util/avatars.ts` draws it to a canvas). See [ADR 0014](../decisions/0014-profile-pictures.md).

### Contrast checks (WCAG 2.2 AA)

`node web/scripts/contrast.mjs` recomputes these from the theme's actual values. Every text pair we use passes 4.5:1.

| Pair | Day | Night |
|---|---|---|
| Primary text on surface / muted / frame top / frame bottom | 17.93 / 15.87 / 14.67 / 12.96 | 14.50 / 16.50 / 12.59 / 17.41 |
| Secondary text on surface / muted / frame top / frame bottom | 7.81 / 6.92 / 6.39 / **5.65** | 6.00 / 6.83 / **5.21** / 7.21 |
| Read room names (ink at 82%) on the frame, top / bottom | 8.92 / 8.23 | 9.02 / 11.89 |
| On-accent on accent (buttons, count pills) | 15.13 | 15.04 |
| Success / error / warning on surface | 6.33 / 7.84 / 6.48 | 10.20 / 10.16 / 10.24 |
| Status text on its own wash (success / error / warning) | **4.62** / 5.51 / 4.79 | 6.10 / 6.09 / 6.13 |
| Avatar initial on its hue tint (worst hue) | 5.38 | 6.62 |

Non-text contrast (3:1): the accent (focus rings, primary controls) is 15.1:1 by day and 12.7:1 by night. Control edges are 3.69:1 on the surface by day (3.33:1 on muted) and 3.53:1 by night, so a text field or checkbox can be found by its outline. Hairlines only separate content that is already grouped. The script exits non-zero if a required pair drops below its minimum.

## Type

- **Inter** (variable, self-hosted via `@fontsource-variable/inter`) for the whole UI, including the wordmark, set through the theme's `typography`. Contextual ligatures are off and weights are never synthesised.
- The system monospace stack (`ui-monospace, SF Mono, Menlo…`) for commands, SHAs, paths and diffs.
- Astryx's scale (base 14, ratio 1.2): 12px supporting text and timestamps · 14px/20px messages, navigation and controls · 17px room, panel and section titles · 24px screen titles. On touch devices editable fields are 16px.
- Names and titles use 600; pills and meta use 500; body text 400.

Font licence: Inter is under the SIL Open Font License 1.1. The licence ships in `node_modules/@fontsource-variable/inter/LICENSE`, and the built woff2 files are the OFL font, unmodified.

## Space, radius, motion

- Spacing is Astryx's 4px scale (`--spacing-*`): 4px message row padding, 12px between message groups (6px when compact, `--yip-group-gap`), 32px between major sections.
- Radii are Astryx's: 10px (`--radius-element`) for buttons, inputs and sidebar rows, 12px (`--radius-container`) for the work card, cards, popovers and dialogs, and full pills for chips, counts, reactions and the send button.
- Motion uses Astryx's durations and easing and responds to user actions: panels slide in, dialogs and menus open, the new-messages pill rises, and a linked message flashes. The only looping motion is the composer's "typing" dots while someone composes a reply. `prefers-reduced-motion` disables all motion.

## Layout

The shell is Astryx's `AppShell` with a `SideNav`. yip draws the frame gradient behind it and insets the work card.

| Width | Layout |
|---|---|
| ≥1200px | The sidebar, the work card, and an **inline** right panel. The panel defaults to 336px and can be resized with a pointer or the keyboard (Astryx's resize handle); the width is remembered. Detail drawers (job, review) are at least 480px wide. |
| 769–1199px | The right panel **overlays** the conversation instead of crushing it. It is modal: the rest of the page is inert and focus is trapped. |
| ≤768px | One primary surface (AppShell's `md` breakpoint). A top bar holds the mark, search and a "Rooms and navigation" button that opens the sidebar in AppShell's drawer, which slides in from that button's side and opens on the current page. Threads and drawers are full-screen views with a Back button; while one is open the top bar steps aside. The chrome, drawer and composer respect the safe areas, and there is no horizontal page scroll. |

The message column is capped at about 76ch plus the avatar gutter, and content stays left-aligned. There is no top bar on wide screens; pane headers inside the card are 52px. Controls and rows have 44px targets on coarse pointers.

## Components and their states

- **Sidebar** (on the frame, no box of its own): the monochrome branching-y mark and wordmark; a "Search everything ⌘K" launcher; Engineers, Projects and Machines; Rooms and Direct messages with sentence-case labels and a + to create; and a footer with machine health ("studio-mini connected", or "3 machines connected") and a profile card (name and workspace) whose menu has Settings, Getting started, Day/Night appearance and Sign out. Room rows show:
  - read rows in slightly quieter ink, unread rows in semibold;
  - mentions as an ink count pill (an Astryx `Badge`);
  - a pencil when a draft is saved;
  - a quiet elapsed-time pill ("3m", "3m (2)") while engineers are working in the room, with the names in its tooltip (it does not animate);
  - a lock instead of # for private rooms.

  The sidebar is an Astryx `SideNav`. Selection is a grey wash, never a colour. Every marker has a visually hidden text equivalent. Any connection problem ("Reconnecting…") sits in a slim line across the top of the window, above the sidebar and the card (AppShell's banner slot).
- **Room header:** one 52px row: `# name` (a lock for private rooms) with the purpose in muted text, then outline buttons for linked projects, the reply mode ("Mentions only" or "Mira answers", with the full sentence in its tooltip and accessible name), and members (stacked avatars in an Astryx `AvatarGroup`, with the names of who can answer, "Oren, Mira", falling back to a count when the room is narrow; roles are in the tooltip and accessible name), and a settings button.
- **Work strip:** one row per logical live or failed assignment (child work stays in its details) (finished work is announced by its result card in the conversation instead). Each row shows:
  - a state word plus shape: hollow circle for queued, bar for running, pause for waiting (the word is the waiting reason), neutral pause for In review, filled check for completed, triangle for failed;
  - the title;
  - the handoff from the actual current review round ("Mira building · Oren review requested", "reviewing", "requested changes" or "approved"); an old-version verdict says it belongs to an earlier revision;
  - the last confirmed activity with a relative time, or the exact blocker text;
  - the machine;
  - the latest attempt's state when it adds something (`WorkRow.runState`). `unknown` replaces the state word with "Not confirmed" and says the machine stopped reporting.

  Clicking a row opens the job drawer. "Add to this" scopes the composer to that job. The strip collapses to three rows plus "Show all N".
- **Message row:**
  - Engineers have **rounded-square** avatars and the human has a **circle**; shape is the only distinction, with or without a picture.
  - The first message of a group shows the name, the engineer's role in quiet text ("Mira · Platform engineer"), and the time. Consecutive messages from the same author within 10 minutes compact, so the role appears once per group rather than on every message.
  - A question asked of you sits on a soft amber wash until it is answered.
  - A job, review, PR, decision or file is linked with a chip only on the first message that mentions it in the view; later messages about the same work stay plain.
  - Message text renders through yip's own markdown-lite (`lib/util/markdown.ts`, which escapes all HTML), not Astryx `Markdown`: chat text needs `__tests__` in a path and `5 * 3` in a sentence to stay literal, which a full Markdown parser turns into emphasis. Only structured mentions are highlighted, full commit hashes show as their short form with the whole hash on hover, `#` headings are bold lines rather than page headings, links (http, https and mailto only) open in a new tab, and images are linked, never loaded.
  - Hovering or focusing a row shows an action pill (react, reply in thread, copy link, and edit or remove on your own messages).
  - A thread summary shows reply avatars and "N replies · last reply 5m ago".
  - Kinds are rendered distinctly: `question` is an ordinary message with "Pip asked you" or "Answered" (and Reply in thread until it has replies); `approval` is the inline exact-action card; `result` is the result card; `review` links the review once; one-line `status` messages are small centred notices, like a group chat's, while longer hub answers show as "yip · From the work ledger".
  - A centred day pill and a monochrome hairline "New" divider (the read position when you opened the room) mark time.
- **Room voice:** short acknowledgments, meaningful developments once, and completion led by the outcome and anything unresolved. Engineers do not recite work IDs, UTC ledger timestamps or scheduling steps. Completion does not imply merge or deployment.
- **Composer:**
  - The mention **combobox** (`role=combobox`, `aria-activedescendant`, arrow keys, Enter/Tab, Escape) lists room members with their role. Engineers outside the room are listed but unavailable ("Not in this room — add them in room settings first").
  - Only choices made from the list become structured mentions. Mentions inside code are ignored, and "Asking Mira, Oren" confirms who will be addressed.
  - Keyboard selection keeps the active engineer visible in long menus. Project choices stay inside the available viewport, and long project names wrap in list and detail headings.
  - With one open question, **Answering Mira's question** targets the answer explicitly. **Not an answer** applies to the next sent message only; the answer target returns afterwards. A mention alone never resolves a question.
  - A project context picker.
  - Enter or Mod+Enter per the owner's preference, and IME-safe.
  - A draft per room and per thread, saved on this device.
  - An optimistic send that reconciles by `clientKey`. A failed send shows "Not sent · Retry · Edit · Discard", and Retry reuses the same key.
  - Editing an unsent message preserves its selected project and structured mentions in rooms and threads. A canonical event or fetched message confirms delivery even when the HTTP response was lost, clearing the saved unsent copy.
  - An offline notice: "Can't reach your workspace. Your draft is saved on this device."
  - The steering scope ("Adding to: Fix Atlas session expiry · Mira") is shown as a bar at the top of the composer and can be cleared with × or Escape. Its receipt reads, in turn, "Delivering to Mira…" → "Mira received your update" or "Queued for Mira's next step", driven only by `input.delivery`.
  - If selected work finishes while a draft is being written, the draft keeps its context. The scope bar names the finished work and says the message will follow up with its owner in the original thread. Reloading restores that target; sending waits while it is loaded. Clearing the target is an explicit choice to return to a room message. This keeps a follow-up out of unrelated work the same engineer has since started.
  - One quiet line under the box, where a group chat shows typing: "Mira is typing…", "Mira and Oren are typing…", "Pip is working on Document Beacon's request flow", or "Pip will reply when possible — No machines are paired yet". Receipts for your updates take the same line. Keyboard hints are for screen readers only. Engineers' streamed text is never shown as it arrives.
- **Result card:** posted like an attachment. It shows the title and state, then one summary line ("3 files +28 −5 · go test ./... passed · approved by Oren") in which every claim opens its evidence: the files open the diff, a check opens its recorded command, exit status and log, a verdict opens the review, and a PR opens its facts. Anything missing is stated plainly, and **Inspect the work** opens the drawer. **Details** expands the full evidence in place: the revision's summary and earlier revisions, every check on the exact current revision (each opening the evidence), each reviewer's verdict on an exact revision beside earlier verdicts ("Oren approved a9002d3 · requested changes on b0e5d19 first"), the revision and branch, the PR's facts kept separate, and the machine.
- **Approval card:** the exact action (command, target, scope, revision, reason, expiry), with Allow this push (or the equivalent) and Reject. The request's `version` is sent. A stale decision reloads the current permission: another tab may already have allowed it or delivered it to the machine, so a conflict never claims that nothing ran. If the reload fails, the card says the current status is unconfirmed and disables decisions until **Check current request** can retrieve it. After a decision, a compact Allowed, Rejected, Expired or "Allowed · delivered to the machine" entry replaces the command body; **View request and outcome** reopens the exact action. Permission delivery does not prove the action succeeded. Pending requests keep all detail. A chat reply does not grant permission.
- **Job drawer:**
  - A header with the state and waiting reason, the exact blocker, an "outcome not confirmed" notice for unknown runs, missing evidence, the owner, reviewers, machine, revision, last confirmed activity and the source link.
  - **Stop** (confirmed; stops the job tree), **Retry** (for failed, recovery or stalled jobs, or an unknown run), **Accept revision abc1234** or **Accept document abc1234** (only when `requiresHumanReview && review_ready`; sends the exact revision and version, and handles 409), and Add to this work.
  - Tabs:
    - **Evidence:** a revision picker labelled with each revision's file and line counts (from `JobDetail.revisions`), the chosen revision's diff with per-file headers, line numbers and +/− lines, plus checks with inline logs, files, decisions, your updates with their receipts, questions and permissions, and related work.
    - **Review:** rounds in order, each giving the verdict on its exact Git revision or document hash, with a link to the reviewed document; findings with severity, file:line, evidence and author replies; superseded rounds marked; a stale approval explained; the PR shown as peer review / remote reviews / remote checks / merge.
    - **Activity:** the job timeline, plus per-run tool logs in collapsed sections (Astryx `Collapsible`) that load on open.
    - **Runs:** each attempt with its state (unknown reads "Outcome not confirmed"), and the provider, model and billing — the only place these appear.
  - The tab bar keeps its height when the drawer contains a long diff, so Evidence, Review, Activity and Runs remain reachable.
- **Getting started** (`/start`) is where a new workspace opens, until it has a room and an engineer who can work on a project there with a reviewer, or the owner chooses **Skip for now**; after that `/` opens the room you were last in, and the checklist stays in the profile menu. It follows one connected engineer, room and project, including repository access and a distinct reviewer where required. Unrelated setup objects do not complete each other's steps. Saved setup and finished work remain checked when a machine goes offline; current availability has its own explanation. The guide names the missing connection and links to its room or project settings.
- **Engineer profile:** role and versioned standing instructions, active and queued work, decisions they recorded, rooms, **projects they can work on** (from the projects' grants, in plain words: "Atlas · can change code · can push"), and provider preference. Empty room and access sections link to the relevant setup choices. Provider availability follows live machine reports, the selected account, allowed billing and required read-only capability; a previously signed-in provider on an offline or drained machine is not described as ready for new work.
- **Review states:** "Changes requested" is an engineering state, not an alert: it uses the pause shape in neutral ink, like any other waiting state, and never red.
- **Notices and form errors:** a sentence led by a 16px status shape (a circled i for information, a triangle for attention, a circled ! for danger), with no box, tint or edge stripe (yip's `Notice`, used in place of Astryx's `Banner`). The shape carries the hue, and danger text is red as well. Toasts and blocking review findings use the same shapes. Why work is in its state (why it failed, what it waits on) is the state's second line, not a separate notice, and missing evidence it already names is not repeated.
- **Machines:** a compact list, then details on demand.
    - **List:** the 24px title, one purpose sentence and one primary **Add machine**, then aligned rows on the main surface (no nested cards). Each row has a device icon and the machine name (17px), its OS and architecture (12px), and three separate facts: **connection** in words and a shape with the last-confirmed time ("Offline · Last heard 3h ago", "Not responding"), **work** ("Idle", "2 running", linking to the work, or "New work paused"), and **providers and limits**: one line per provider ("Claude Code · Needs sign-in") plus only the limitations that change what the machine can do: low disk, sign-in required, an allowance pause, read-only reviews unavailable, a missing execution profile, and a temporary session. A trailing **Details** button (in one aligned column) opens the machine. When the list is wider than 780px the row is a four-column grid under a quiet header; narrower, the facts stack under the name. Rows use 12–16px padding and restrained dividers.
    - **Details** open in the right panel (inline beside the list when there's room, overlaid otherwise), titled with the machine and its connection, and are organized by purpose in four tabs; a tab with something needing attention carries a small dot. Arrow keys move between tabs.
        - **Overview:** "What limits its work" (each limitation links to the tab that explains it), connection with its effect on work (an offline machine is never called asleep; yip says it can't tell why), current work, new work (**Pause new work** / **Resume new work**; the confirmation says current work finishes), capacity (machine slots, kept distinct from the per-account limit under Connections), free disk, and whether it runs as a background service or a temporary session that stops when its terminal closes (with the supported `yip service install runner` command), then the consequential actions: **Stop current work** and **Revoke access**, each its own confirmed action with its loss explained.
        - **Connections:** one concise row per provider (name, sign-in state, account, billing) that expands to its explanation, availability, read-only support, version compatibility and the account's shared concurrency setting. A sign-in command sits next to the provider that needs it, with **Check sign-in again** on a connected machine.
        - **Storage:** workspaces as left-aligned rows with the action in a trailing column: kind in words (working checkout, review snapshot, conversation scratch space), its work, size (measured, "at least" when the count stopped early, or "size unknown", never a false 0), in-use or protected state and unpublished or uncommitted changes. Cleanup lives only here, confirmed, and says what is lost.
        - **Diagnostics:** runner and provider versions, execution profiles with the reason one is unavailable, each adapter's full limitations, toolchains as a name/version list, and the fingerprint with a copy button.
- **Dialogs:** Astryx `Dialog` (yip's `Dialog.svelte` adds the title, description and footer shape) and `AlertDialog` for confirmations, on native `<dialog>` with `showModal`, so the rest of the page is inert. Dialogs with input don't close on a backdrop click. Focus returns to the invoker.
- **Toasts:** the store's toasts show through Astryx's toast viewport (`Toasts.svelte`).

## Interaction and accessibility

- Landmarks are the navigation ("Workspace"), AppShell's main region, and on phones its top bar, and AppShell provides the skip link. The right panel is a `complementary` region when inline and a `dialog` otherwise.
- **Astryx's layer stack** means Escape closes only the topmost layer (a menu, emoji palette, dialog, drawer or the phone navigation) and returns focus to the control that opened it. A modal right panel joins it through `useFocusTrap`; an inline panel takes an Escape no layer claimed. Focus traps stack, so a confirmation dialog over an overlaid drawer traps correctly.
- Message history is a labelled region. Up and Down (or j and k) move a roving focus between messages. Each message's actions become visible and reachable when it's focused, with no hover needed. "Load earlier messages" is a real button, and scrolling up also triggers it.
- Live regions: a single polite announcer handles new messages from others in the room you're viewing, receipts and confirmations. Errors show as Astryx toasts, which Astryx announces assertively; a failure inside a dialog is shown in the dialog instead, since a toast would sit behind it. A `Notice` is a live region only when it should be: an error from an action is an alert and a notice that appears in response to one is a status, while a standing notice ("an earlier revision", "low on disk") is not announced each time a view opens. Engineers' streamed text is never rendered, so there's no token-by-token speech.
- Focus rings come from Astryx and use the ink accent; yip's own links and buttons take the same ring (`app.css`). Controls and rows have 44px effective targets on coarse pointers (`app.css`).
- State is never shown by colour alone. Shapes and words carry it, diff lines have +/− signs, and unread rooms also get visually hidden text.

## Borrowed from Buzz, and what differs

These cues were adopted from our study of the Buzz desktop client:

1. **Tinted chrome framing one opaque card** (`rounded-2xl`, a hairline edge), with panes split by hairlines rather than nested cards. yip uses its own warm-to-cool tint.
2. **A monochrome accent**, with hue only for status, so an agent-heavy screen stays calm.
3. **Search, navigation and the profile in the sidebar**; no top bar.
4. **Unread shown by weight, not colour**, black count pills for mentions, and an elapsed-time "working" pill on room rows.
5. **Rounded-square avatars for engineers and a circle for the human.** Almost every author here is an engineer, so the one human stands out by shape alone. The role takes the metadata slot after the name.
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
- **Accessibility first:** sidebar text is full or 82% ink rather than Buzz's 40% so it passes AA on the frame.
- **No community rail, presence dots or theme gallery.** yip is one workspace with one human, and presence would imply a human is online.

## Files

- `web/src/lib/theme.ts`, `theme-tokens.ts`: the yip theme over Astryx's neutral theme.
- `web/src/app.css`: loads Astryx's stylesheets and holds yip's few globals (the frame, layout measures, touch targets), all in the `product` cascade layer.
- `web/src/components/`: yip's compositions of Astryx components: the sidebar, screen and panel chrome (`Screen`, `ScreenSection`, `RightPanel`), message rendering, the composer, cards and dialogs. `panels/` holds the right-panel views. Icons are Lucide (`@lucide/svelte`) through Astryx's `Icon`.
- `web/src/screens/`: one component per route.
- `web/src/lib/state/`: the pure event reducer (`data.ts`), the runes store (`app.svelte.ts`), the SSE client (`events.ts`), drafts (`drafts.ts`) and the detail cache.
  - On load the client reads `/v1/bootstrap`, then `GET /v1/runs` for active attempts. It opens the event stream at the bootstrap cursor, so there is no history replay.
  - A room loads `GET /v1/rooms/{id}/work?include=replies`. Live conversational replies feed the composer's activity line, and `runState` feeds the strip.
  - REST snapshots never turn a run that events have already finished back into a live one.
  - `message_delta` stream chunks are appended to the preview, which is replaced when the canonical `message.created` arrives.
- `web/src/lib/util/`: message-text helpers, mentions, diff parsing, labels and time.
- `web/scripts/contrast.mjs`: the contrast checks above.

## Running and verifying

- `cd web && npm ci`, then `npm run dev`. This starts Vite on :5173 and proxies `/v1`, including the SSE stream, to `YIP_HUB` (default `http://127.0.0.1:7521`). Start the hub with `--allowed-origin http://localhost:5173` so state-changing requests from the dev origin pass the Origin check.
- `npm run check` runs svelte-check over the app and the tests, and fails on warnings. `npm run build` writes `web/dist` (keeping `dist/.gitkeep`) for the hub to embed.
- `npm test` runs the unit tests (reducer, mentions, message rendering and escaping, drafts, router, diff parsing) and the jsdom smoke suites. The smoke suites mount the real `App` against recorded hub payloads (`tests/unit/fixtures`), including a full SSE stream. The engineers in those recordings followed a script, not a real provider; see the [fixtures README](../../web/tests/unit/fixtures/README.md).
- `just e2e` (or `npm run e2e` after `just all`) starts a real hub on a new, empty data directory and runs the browser smoke journeys in Chromium or an installed Chrome: owner setup with the printed code, sign-in, keyboard use of dialogs, mentions and search, machine pairing, creating and switching workspaces, and layouts at 390, 1024 and 1440px. No machine is paired and no provider runs.
- `node scripts/contrast.mjs` recomputes the contrast table.

### Conversation verification (26 September 2026)

The scripted browser journey covers assignment, clarification, a genuine
question, owner answer, peer requested changes, author correction, re-review,
completion and recall from another permitted room, followed by Overview
review evidence opened with Enter. It needs no owner relay or mandatory
acceptance. Room captures covered 390, 900, 1280 and 1440px in both themes;
the journey and capture scripts ran against the removed demo (ADR 0004). See
the
[release checklist](../release-checklist.md) for current counts and the
[adversarial campaign](../simulations/2026-09-28.md) for the separately
authorized real-provider and GitHub checks.

### Conversation and catch-up verification (28 September 2026)

The follow-up [UX campaign](../simulations/2026-09-28-ux.md) compares the
implementation with the owner's original conversation and the product spec.
It covers live Overview updates during one visit, threaded source navigation,
drafted follow-ups after work ends, connected setup, drawer navigation and
stale permission decisions. The report records UI checks separately from the
real-provider recall check.
The Overview it covers was later removed ([0016](../decisions/0016-no-overview.md)).
