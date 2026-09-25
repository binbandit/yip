# yip · Product and design

Version 1.2 · 25 September 2026 · Product direction and interaction specification

Working name: **yip**. The possible domain is `getyip.dev`; see the dated naming checks in the research appendix.

## 1. The idea, sharpened

**A familiar team that carries the work with you.**

yip is a home for the AI engineers you work with. You can put Mira into both Engineering and Security, ask Pip to reverse-engineer an unfamiliar repository, and bring Oren into the conversation for a second opinion. They remain the same engineers as the rooms and projects change. Work runs on your machines, and its progress comes back to the conversation where it belongs.

The product should remove the need to act as the agents' memory, dispatcher, and messenger. Its value is not the number of agents visible on screen. Its value is how naturally you can move from a broad conversation to useful, inspectable work and back again.

### The “almost there” problem

The user's concern is central: tools can have agents, channels, remote execution, and attractive screenshots and still feel incomplete. Axel's [20 September X post](https://x.com/itsmraxel/status/2101582902727147786), read in the user's authenticated browser session, describes that feeling after trying Buzz and seeing Nebula. This is qualitative evidence, not proof that an entire market agrees or that those products lack adoption.

Our working hypothesis is **a gap in continuity and coordination**, rather than a missing dashboard feature:

| When the experience breaks | What the user feels | yip's intended response |
|---|---|---|
| “Who did I tell about this?” | My work is scattered across chats | Engineers and jobs remain addressable across rooms; source conversations stay linked. |
| “Didn't we already decide that?” | I am repeating myself | Scoped decisions with provenance are available wherever sharing is permitted. |
| “Can you ask the other agent?” | I am the integration layer | Engineers can make a visible, bounded request to a colleague and continue when answered. |
| “Is it actually doing anything?” | The animation is not trustworthy | Show the last confirmed action, owner, machine, blocker, and evidence. |
| “I just want to change one thing.” | I must restart or derail the job | Follow-up, queue, steer, and cancel have distinct behaviour. |
| “I came back and there are 80 messages.” | Collaboration has become noise | A useful catch-up, clear outcomes, and optional expanded collaboration detail. |
| “Why does every question need a new project?” | The tool's structure is driving my thinking | Rooms can range across projects; jobs make their execution scope explicit. |

These are hypotheses to validate through realistic use. We cannot infer product quality or adoption from a few social posts, and there is no defensible claim that nobody else is addressing these problems. The opportunity is to execute this combination exceptionally well.

## 2. Product principles

**People first, work second, infrastructure third.** The daily interface shows familiar names and conversations. Job state is nearby. Machines and provider plumbing appear when they explain a choice or failure.

**Continuity without pretending omniscience.** Engineers remember approved, relevant facts and cite their sources. They do not magically remember everything or disclose private-room material elsewhere.

**Work should leave evidence.** “Done” points to an output. Code work comes with a diff and checks; research comes with sources; a decision links to its discussion. A confident paragraph is not completion.

**One request should not produce a chorus.** The owner answers, collaborators contribute when useful, and routine status updates stay compact. We want the feel of capable colleagues, not role-play about being busy.

**Engineers own the work.** They investigate, make reasonable decisions, consult colleagues, verify results, and finish within granted scope. The user should not become their dispatcher or routine decision-maker. They ask for help only when they genuinely cannot resolve something, in the group chat like a colleague. The user can inspect progress, change scope, and stop work at any time.

**Colleagues have judgment and a shared history.** Engineers volunteer relevant concerns, ask one another for help, choose reviewers, disagree constructively, make revisions, and remember the resulting decisions. Their personality comes through clear, consistent communication and engineering choices. Names and engineering roles lead the conversation; AI identity remains clear in profiles and accessible labels, without repeating provider metadata beside every message. Avoid invented biographies, fake human availability, or theatrical office chatter.

**The laptop is a window.** Remote work continues when it closes. The interface names the executing machine so this is a visible fact rather than a marketing promise.

## 3. Positioning and what to leave out

Primary audience: developers who already use coding agents, work across several projects, and own an always-on machine or dev server. A single-human organisation is a complete use case. Later teams can use the same work and identity model.

Category description: **a self-hosted workspace for your AI engineering team**.

Do not lead with “multi-agent orchestration platform,” a virtual office, an org chart, token graphs, or a simulated company. Avoid requiring a boss agent, a workflow canvas, or an issue form before the user can talk. Do not compete with Slack's entire feature set or build a GitHub replacement.

The conversational experience is the product. The structured job model exists to make that experience dependable, not to turn every discussion into ticket administration.

## 4. Brand and name

### yip

Use lowercase **yip**, pronounced as written, with no acronym expansion or explanatory suffix. The name is short, conversational, and easy to use: “Put it in yip.” “Ask Mira in yip.”

The user chose yip over yap after reviewing existing uses. `getyip.dev` is a possible domain: the registry returned no registration record during the check, but registrar availability and price are unconfirmed. Existing uses mean the name is not globally unique or cleared. The npm name is taken. See the dated evidence in the research appendix; keep public distribution configuration separate from the wordmark.

### Identity: a branching y

The new mark uses two rounded strokes meeting as a **y**, with an aqua upper arm, a white main stroke, and a yellow terminal. It keeps the established petrol background and reads at small sizes. Use the supplied monochrome version when colour is unavailable. The mark accompanies the lowercase wordmark without bird terminology, robot faces, or status animation.

Names and identities of the engineers stay independent of the product brand. Ordinary objects remain engineers, rooms, projects, work, and machines.

## 5. Visual direction

> Superseded for the web client by [ADR 0012](../decisions/0012-visual-direction.md): the owner chose a neutral, Buzz-like visual system over the aqua and petrol palette below. The accessibility and truthfulness rules still apply.

### A daylight workshop

Use a pale aqua navigation surface, a clean paper-white conversation area, deep petrol text/accent, and small golden markers for attention and handoffs. The feeling should be relaxed and purposeful: something you keep open all day, with enough character to recognise in a screenshot.

The published [Buzz Projects examples](https://engineering.block.xyz/blog/projects-in-buzz) show a useful separation between soft outer chrome and a clear inner work surface, with compact navigation and adjacent context. Borrow that hierarchy, not its assets, colour system, or layout pixel for pixel. Retain Slack's familiar spatial model of rooms and threads while creating a different visual identity.

The memorable element is the **aqua frame around a bright conversation surface**, combined with the branching y mark. Everything inside should stay quieter. A restrained handoff line connects named collaborators and the current result. It conveys work, not decoration.

### Design-plan review

An early direction could easily become another dark developer console with neon status dots, or a cream-and-serif AI landing page. Neither fits this brief. The chosen system uses daylight surfaces and a compact humanist sans; the contrast comes from the aqua frame and distinctive mark. It is an application, so conversation and useful controls occupy the first screen rather than a marketing hero.

### Tokens

| Token | Day | Night | Use |
|---|---|---|---|
| `canvas` | `#E9F2F3` | `#172C32` | Outer application background |
| `navigation` | `#D7E9EB` | `#203B43` | Sidebar and organisation identity |
| `surface` | `#FFFFFF` | `#21343B` | Conversation and raised panels |
| `surface-subtle` | `#F3F7F7` | `#29424A` | Supporting content, hovered rows |
| `ink` | `#183A43` | `#EDF5F5` | Main text |
| `ink-secondary` | `#526B72` | `#AFC6CC` | Metadata and secondary copy |
| `accent` | `#176274` | `#89CFDE` | Selected items, links, main controls |
| `accent-ink` | `#FFFFFF` | `#173740` | Text on accent fill |
| `line` | `#D7E2E4` | `#43606A` | Necessary separators |
| `attention-fill` | `#F6DB7C` | `#705C24` | Small attention surfaces |
| `attention-ink` | `#57420D` | `#FFF0B8` | Attention labels |
| `success` | `#236A51` | `#8FD4B5` | Confirmed success |
| `danger` | `#AA3944` | `#FFABB1` | Failed/destructive actions |

Validate actual text/surface combinations against WCAG 2.2 AA, not just isolated tokens. Yellow is a fill or marker, not body text on white. Never use colour alone for status.

Typography: Hanken Grotesk for production UI; Bricolage Grotesque only for the wordmark and occasional onboarding title. Self-host appropriately licensed font files and retain their licences. The offline concept uses an installed native sans fallback so it opens without network dependencies. Code uses the system monospace stack, only where code benefits from it.

Type scale: 13px metadata, 14px navigation, 15px/1.55 messages, 18px room title, 26px overview title. Essential text never below 13px. Editable fields use at least 16px on touch devices. Names use medium/semibold weight; message content remains regular.

Spacing uses a 4px base: 8px within small groups, 12–16px for row padding, 24px between message groups, 32px for major sections. Control radius 8px, artifact radius 10px, large work-surface corner 16px. Do not put every message in a rounded bubble or every fact in a card.

### Layout

At 1440px, use a 232px sidebar, flexible conversation, and an optional 336px context drawer. The conversation text column is capped around 76 characters. Keep content left aligned. At 1024px, the sidebar becomes 200px and the context drawer overlays instead of crushing the conversation. Below 760px, use one primary surface with a Rooms button, a separate thread screen, and a bottom-safe-area composer. No permanent four-column layout.

```text
┌────────────────────────────────────────────────────────────────────┐
│ yip             Search rooms, people and work              Profile  │
├────────────────┬───────────────────────────────┬───────────────────┤
│ Your workspace │ Security       Mira Oren Pip  │ Work / Evidence   │
│ Overview       │ Atlas, Beacon                 │ Fix token expiry  │
│                │                               │ Owner  Mira       │
│                │ Brayden: Can you investigate… │ Review Oren       │
│ Rooms          │ Mira: I found…                │                   │
│  Engineering   │ Oren: Here is the risk…       │ Diff              │
│  Security      │                               │ Checks            │
│  Reverse eng.  │ [Work result, linked evidence]│ Decisions         │
│                │                               │ Activity          │
│ Engineers      │ Message Security…             │                   │
│ Projects       │ Context: Atlas   Send         │                   │
│ Machines       │                               │                   │
└────────────────┴───────────────────────────────┴───────────────────┘
```

The drawer is contextual, normally closed until selected or until a user chooses to keep it open. The room header always identifies who can answer. Machine health is a small sidebar footer except on the Machines screen.

## 6. Information architecture

**Overview:** cross-project work and changes since the owner last visited. Lead with completed outcomes, active work, and useful context; link genuine blockers to their source conversations. Each row has an owner, project, state, last confirmed update, and an origin link. Avoid charts unless they answer a real operating question.

**Conversation is the place for questions:** use ordinary room mentions, replies, and unread indicators. There is no “Needs you” inbox, input queue, or engineer-wide attention badge. A question belongs with the work and the colleagues who can help, not in a separate manager dashboard.

**Rooms:** Engineering, Security, Reverse engineering, and user-created groups. A room is a group of participants with a purpose; it is not constrained to one task or repository. Allow private rooms and DMs. One-person conversations with an engineer still use the same underlying model.

**Engineers:** enduring profiles with skills, instructions, provider preference, memberships, current work, and source-backed knowledge. Show “Mira · Platform engineer” as the identity; provider/model is secondary configuration, visible in run details.

**Projects:** linked repositories, instructions, permissions, decisions, open work, and related conversations. Do not force the user to enter a project before starting a conversation.

**Machines:** where execution happens, which provider profiles are ready, and why a machine can or cannot run a job. Use ordinary labels such as “Studio mini” and “Build mini.”

Global search understands names, rooms, project filters, messages, work IDs, and decisions. A result always opens its actual source. Search access matches conversation access.

## 7. The key interaction journeys

### A. Build your first small team

1. Create a local organisation and choose where its hub will run. Explain the practical consequence: if the hub runs on the laptop, it stops being available when the laptop sleeps.
2. Add an always-on machine through a verified installation and pairing flow. Show its name, fingerprint, operating system, and reachable providers.
3. Connect the user's provider through its own supported sign-in. Show the billing mode and the machine holding the session. yip never asks the user to paste subscription tokens.
4. Create an engineer with name, role, and access. Offer a few editable role starting points; no fictitious biography or claimed years of experience.
5. Create a room and add existing engineers. Add a project by remote repository or explicit local import.
6. Run a small real investigation and show its result, evidence, and machine. Do not declare setup successful based on a green connection dot alone.

The user may skip remote setup and begin on one machine. The interface should explain the resulting execution location rather than obstruct them.

### B. Ask for work in an ordinary sentence

Example: “@Mira fix Atlas’s session expiry bug.”

The composer resolves the names to engineer IDs and Atlas to a project chip. Sending creates one source message. Mira acknowledges the objective, starts the implementation job, and requests Oren's review through an explicit dependency. The room shows a compact work strip: **Session expiry · Mira building · Oren reviewing next**.

The work strip opens a drawer. It does not force the user to fill out a ticket. The engineer first resolves the target from the conversation and project links. If “Atlas” still refers to two equally plausible writable repositories, ask which one in the room. Routine implementation choices do not require a question.

### C. Ask a colleague for a review and finish together

The author can initiate a review without the user mentioning a reviewer. Mira knows Oren handles security, so she asks him directly in the room. The same exchange works for a patch, document, investigation, or linked pull request.

Mira: “@Oren, can you review the expiry fix in PR #42? The exact expiry boundary is the bit I want a second pair of eyes on.”

Oren: “One change before this goes in: refresh still accepts a token at the exact expiry instant. Please use the shared validator there and add a regression test.”

Mira: “Good catch. Refresh now uses the same validator; the new regression passes. @Oren, ready for another look.”

Oren: “Checked the updated diff and the boundary test. That fixes it. Approved.”

Mira: “Fixed and reviewed. All 18 checks pass. The patch is complete; PR #42 is open.”

The conversation remains ordinary chat. A compact attached PR/work reference opens its diff, comments, checks, and review history. The review detail shows the current verdict, who reviewed, which revision, the original finding, the author's response, and the re-review. “Changes requested” names an engineering state; it is not an alert asking the human to coordinate. The author continues other work while waiting for a colleague.

Reviews are substantive: the reviewer reads the change and relevant code, challenges assumptions, and requests changes when warranted. Optional suggestions do not masquerade as blockers. A disagreement is discussed with evidence; another colleague can help. The human is brought in only for a real missing decision. No compulsory manager agent or human sign-off step.

A PR summary separates peer review from remote checks and merge state. In the concept, all PR data is explicitly sample data and its review opens locally; there is no invented live GitHub link. In production, the PR reference opens the actual canonical PR. New commits make an older approval visibly outdated and trigger re-review where required. The engineer's name identifies the internal reviewer; publishing under a shared forge account does not pretend to satisfy an independent approval rule.

### D. Interrupt without losing work

“Keep the existing API response shape.” is attached to the selected running job. If the provider supports steering, deliver it to that attempt and show “Mira received your update.” Otherwise show “Queued for Mira's next step,” or offer an explicit interrupt and restart. Never imply immediate delivery when it did not happen.

An unrelated request in the room creates separate work and has its own reply destination. The UI keeps the current thread highlighted to avoid accidentally steering the wrong run. **Stop** cancels the selected job tree; it is not the same as muting the conversation. **Pause after this step** only appears if the adapter implements a truthful safe-boundary pause; otherwise use Stop and later Resume from checkpoint.

### E. Move between rooms with the same engineer

Mira is in both Engineering and Security. Opening either profile shows the same identity and current work. Mira's implementation history in Atlas can inform an authorized later task. Private security findings do not automatically appear in Engineering; the source visibility determines what can cross the boundary.

An agent may ask a permitted engineer in another room for help, but it cannot silently add that engineer to this private room. The UI can show a bounded, explicitly shared handoff, or ask the owner to invite them. An invitation shows what history will become visible before it is applied.

### F. Ask “Where are we with everything?”

In the owner's personal Overview conversation, this asks for the current state across permitted projects. Read the work ledger first. Provide a short answer with live links: “Atlas is fixed and reviewed; Pip is mapping Beacon’s gateway and has asked in Reverse engineering where the separate retry-worker repository lives.”

Known status needs no new worker run. If explanation requires a model, give it the facts and their timestamps, and identify the result as a summary. An agent must not poll every teammate to discover a status the hub already knows. In a shared room, filter the answer to material suitable for that room.

### G. Close the laptop and come back

When execution is remote, the job says “Running on Studio mini.” Returning shows **Since you were here**: decisions, completed outputs, blockers, and any changed assumptions. This is generated from persisted events; the user can expand the exact messages. Do not mark unseen conversations as read merely because a summary exists.

If a worker disappeared, say “Studio mini disconnected. Last confirmed: running gateway tests.” Do not keep showing “Working…” indefinitely or imply that disconnect means no side effects occurred.

### H. Finish and make the result inspectable

The engineer completes the job when its evidence, checks, and required reviews satisfy the task. The result presents what changed, the diff/artifact, checks, reviewer comments, and any unresolved limitation. There is no mandatory **Accept result** step unless the user explicitly required human review. The owner can inspect it later or request changes in the same conversation; a follow-up links to the original job. **Open pull request**, **Push branch**, or **Deploy** are separate, specifically authorized actions. Emoji reactions are social; they are not deployment approval.

## 8. Conversation and work components

### Message anatomy

Avatar; name; role badge on the first message in a group; timestamp; body; optional evidence links; thread action. Show a useful engineering role beside the name when it introduces an author group. Profiles and accessible identity labels clearly identify AI engineers; avoid repeated generic “Agent” labels and model/provider badges in everyday conversation. Consecutive messages from the same author can compact. Avoid fake photographs or presence that implies a human is online.

### Work strip and result

The work strip is a narrow attached element with a state word, objective, owner, and collaborator handoff. State is encoded with label plus shape: hollow circle queued, active bar running, pause-shaped icon waiting, check ready/completed, warning triangle failed. Do not animate a progress percentage without a measurable denominator.

A result expands into one well-structured panel. Its evidence is adjacent to the claim: “Gateway regression test passed” opens the actual recorded command, exit status, revision, and log. A hash or green dot alone is not a useful review experience.

### Genuine questions in the group chat

Before asking, the engineer checks available instructions, code, prior decisions, documentation, and appropriate colleagues. For routine choices it uses judgment and states a consequential assumption in its progress/result. For example, Mira preserves Atlas’s established strict-expiry contract, gets Oren’s review, and completes the fix without asking the user to choose a grace period.

When information is genuinely missing, ask an ordinary message in the source room/thread: “@Brayden, which repository contains Beacon’s retry worker? I checked the linked repos and setup docs but only found the gateway and queue producer. I’m continuing the gateway deduplication map meanwhile.” No form, attention card, or required-input banner. Any suitably authorized participant may provide the answer. Normal replies resolve the correlated question behind the scenes.

Only the dependent step waits. Other tasks and independent investigation continue. If all useful work is exhausted, the job can truthfully wait for that dependency without turning the engineer into an alert. Use normal mention notifications; do not send repeated reminders unless the user requests them.

### Exceptional permission requests

Actions already covered by user instructions or project policy proceed. If extra authority is genuinely required, the request appears inline in the source conversation with the exact action, target, scope, and evidence. Precise controls such as **Allow this push**, **Reject**, and **View diff** apply only to this exceptional action. Changed or expired requests cannot execute. Ordinary natural-language replies answer factual questions; they do not bypass exact-action permission checks.

### Peer-review detail

Show the work or PR title, author, reviewer, current verdict and revision, and checks. Put review comments and author replies in a chronological discussion with file/line links where applicable. Keep “requested changes on the first revision” visible beside “approved the updated revision” so the result has a credible history. Provide **View review**, **View diff**, and the source-conversation link. Routine review exchange needs no button from the owner.

### Engineer profile

Sections: role and instructions; rooms; active/queued work; provider preference; permitted projects; remembered decisions with sources. Editing instructions creates a new version; active runs keep the old snapshot unless deliberately restarted. Renaming the engineer preserves its ID and old messages' attribution history.

### Machines

Show availability, last seen, active slots, execution profile, ready providers, and disk pressure. A connection state and a provider state are separate: the machine may be online while Claude needs sign-in. **Drain machine** stops new assignments and lets active jobs finish; **Stop its work** is a distinct action. A sleeping laptop should not be shown as a mysterious infrastructure failure.

## 9. Empty, error, and waiting states

| Situation | Interface copy / next action |
|---|---|
| Empty room | “Bring a couple of engineers into this room, then tell them what you're working on.” / Add engineers |
| No project | “You can talk here now. Connect a project when you want the team to inspect or change code.” |
| Unavailable provider | “Cursor needs sign-in on Build mini.” / View setup |
| Allowance reached | “This account's allowance is exhausted. Work is saved.” / Wait or choose a configured provider |
| Worker missing | “Last heard from Studio mini at 10:42. The run's outcome is not yet confirmed.” / Inspect recovery |
| Hub unreachable | “Can't reach your workspace. Your draft is saved on this device.” / Retry |
| No permission | “Mira can read Atlas, but this task needs write access.” / Review access |
| Duplicate send retry | Reconcile to the existing message; do not show two requests. |
| Unverified result | “Changes are ready. The browser check did not run.” / Review evidence |
| Unknown action outcome | “The connection ended during the push. Check the remote before trying again.” |

Keep drafts locally per room/thread, clearly distinguish pending sends, and never queue a destructive command for silent replay when the network returns.

## 10. Interaction quality

- Keyboard: Cmd/Ctrl+K search; Escape closes the topmost dismissible layer; room and message actions are reachable without hover. Enter sends, Shift+Enter adds a line; offer a preference and respect IME composition.
- Mentions: accessible combobox, keyboard selection, human-readable name and role, clear unavailable state. Pasted text does not automatically mention people.
- Scrolling: append smoothly when at the bottom; preserve position while reading older messages; show “New messages” without jumping. Virtualized lists must support keyboard/screen-reader history access.
- Motion: small transitions responding to user action, approximately 120–180ms; respect reduced motion. No infinite decorative particles or theatrical “thinking” avatars.
- Focus: visible outline; focus returns to the invoking control after a drawer/dialog closes. Dialogs trap focus; ordinary inline panels do not.
- Accessibility: WCAG 2.2 AA, semantic navigation, labelled controls, 44px effective touch targets, meaningful loading announcements, and no token-by-token live-region spam. [WCAG reference](https://www.w3.org/WAI/WCAG22/quickref/)
- Notifications: relevant completions, ordinary mentions for genuine questions, and meaningful failures under room preferences. No separate human-input queue, repeated unanswered-question nags, or notifications for every tool event or “still working” update. Room muting changes notifications, not execution.
- Preferences: appearance, density, notification scope, keyboard send behaviour. Keep provider flags and deployment controls out of the main composer.

## 11. The experience tests that matter

These are scenario-based product gates, not claims already validated by users:

1. **Familiarity:** after using three rooms, can the owner recognise the same engineer and explain what it is responsible for?
2. **No messenger work:** can a builder ask a reviewer a concrete question and continue without the owner copying text between sessions?
3. **Natural interruption:** can the owner add a constraint while work is running and see whether it was received?
4. **Truthful catch-up:** can the owner understand what changed across projects, including what is blocked, without opening every run?
5. **Continuity:** can a later job reuse an approved decision with a source link, while a private decision remains private?
6. **Trust:** can the owner distinguish a proposed plan, active work, partial output, and verified completion from the UI alone?
7. **Useful quiet:** does an idle room stay idle, and does a collaboration reach an outcome without a stream of empty acknowledgements?
8. **Remote confidence:** can the owner identify where the work runs and close the laptop without wondering whether it will stop?

9. **Independent judgment:** does Mira preserve established behaviour, obtain peer review, and finish the fix while the owner is away, with no unnecessary question or acceptance click?
10. **Natural help:** does Pip ask once in the correct group chat for genuinely missing information, keep independent work moving, and resume the affected step from the eventual reply?

11. **Peer ownership:** does the author choose a suitable reviewer, request review, address a real defect, and get the updated work reviewed without the owner dispatching either participant?
12. **Review truth:** can the owner tell which revision was approved, whether feedback was addressed, and whether the PR is actually merged?

Observe real use and record failures. Do not treat polished onboarding, number of agents, token throughput, or social-media excitement as substitutes for these outcomes. The [MVP brief](03-mvp-build-spec.md) makes the underlying functional behaviours testable.

## 12. Prototype and production handoff

The interactive concept that accompanied this document has been removed at the owner's direction, and the visual direction in section 5 is superseded by [ADR 0012](../decisions/0012-visual-direction.md). Behaviour, content, and accessibility requirements here still apply.

Production implementation must add real streaming, persistence, authentication, permission checks, composer mentions, accessible overlays, durable draft/reconnect behaviour, and every acceptance gate in the build specification. Do not reuse the prototype's simplified local state as the application's coordination model.
