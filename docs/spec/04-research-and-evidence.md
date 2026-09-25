# yip · Research and evidence

Research cut-off: 25 September 2026 · Companion to the three implementation documents

## 1. What the research changes

The broad idea already exists. Several products combine persistent agents, team communication, coding tools, and remote execution. yip should not claim to invent “Slack for agents.” The proposed opportunity is a particular experience: a familiar cast of engineers, usable across rooms and projects, doing accountable work on computers you own, with very little context relaying by the human.

The strongest conclusion is a product hypothesis, not a finding of an empty market: **conversation needs to preserve the continuity of work.** Identity, responsibility, evidence, interruption, and recovery must fit together. Attractive chat alone will not provide that continuity. Neither will a task dashboard alone.

Axel's criticism supports investigating this hypothesis. It does not establish which missing interaction matters most or prove that a competitor has failed. Lack of visible breakout adoption is also not evidence that a particular feature is missing; distribution, switching costs, reliability, timing, pricing, and audience can all matter.

## 2. Method and confidence

Research combined primary product documentation and repositories, published product imagery, specific GitHub issue reports, Reddit discussions, and public X posts read through the user's authenticated browser session. The X work was read-only. No messages, reactions, follows, or account changes were made.

Searches included Buzz/Block/agents, agent coordination and context loss, multi-agent reliability, remote coding sessions, self-hosted agent collaboration, provider programmatic interfaces, and candidate names. This was a purposive qualitative sample, not a representative user survey. Promotional replies and launch announcements were treated as claims by their authors.

| Evidence class | What it can establish | What it cannot establish here |
|---|---|---|
| Official documentation / source repository | Published architecture, interfaces, documented constraints | End-to-end reliability in the user's environment |
| Published screenshot | Visible hierarchy and visual decisions | Interaction quality, accessibility, responsiveness |
| Issue report | A concrete reported failure mode worth testing | Frequency, current unresolved status, all-user impact |
| Social discussion | A expressed need or first-person impression | Market size, causal explanation, representative sentiment |
| Registry / exact-name lookup | A bounded search result at a particular moment | Worldwide non-use, trademark clearance, reservation |

Competitors were not installed or benchmarked. Provider accounts were not connected. The proposed adapters and acceptance tests are engineering recommendations grounded in documented interfaces, not results of a completed integration trial. Live documents can change; pin provider versions and retain conformance records when building.

## 3. The competitive landscape

The distinctions below describe published emphasis and documented boundaries. A missing claim in a README is not proof a feature is absent.

| Product | What is relevant | Consequence for yip |
|---|---|---|
| **[Buzz by Block](https://github.com/block/buzz)** | Very close to the core concept: humans and agents in persistent conversations, with coding harness integration and a broader collaboration surface. | Benchmark the actual workflow against Buzz. A fresh app needs to earn its existence through simpler personal operation and a better connected experience. |
| **[Nebula](https://docs.nebula.gg/docs/channels/agents)** | Channel teams can include existing agents. A mandatory workspace orchestrator directs work. Its documented own-computer agents answer only in DMs; channel use requires moving them to a cloud device. | Make local execution and group collaboration work together. Let a normal engineer be a replaceable room steward; avoid a required special boss. |
| **[Paperclip](https://github.com/paperclipai/paperclip)** | Self-hosted agent organisations, goals, work assignment, governance, and budgets. Its centre of gravity is managed work and organisation structure. | Borrow accountability and bounded budgets, but make an ordinary conversation the starting point. Do not build a simulated company to solve a personal workflow. |
| **[OpenClaw multi-agent routing](https://docs.openclaw.ai/concepts/multi-agent)** | Distinct agent workspaces, identities, sessions, and message routing. | Persistent identity is already a known pattern. Separate it from execution sessions and make cross-room continuity understandable to the user. |
| **[Happier](https://github.com/happier-dev/happier)** | Close adjacent competitor: remote work on owned machines, multiple providers, self-hosting, clients, and collaboration features. | Remote access alone is insufficient differentiation. Compare the work loop, not just whether a phone can see a terminal. |
| **[AionUi](https://github.com/iOfficeAI/AionUi)** | A unified cowork interface around multiple coding CLIs, including remote WebUI and automation capabilities. | Provider switching and a unified shell are useful baseline capabilities, not the central new idea. |
| **[Vibe Kanban](https://www.vibekanban.com/)** | Agent work organised around projects, tasks, isolated work, and review. | Code isolation and review must be credible, while rooms remain free to discuss several projects naturally. |
| **[Claude Code agent teams](https://code.claude.com/docs/en/agent-teams)** | Native teammate sessions and collaboration, with documented experimental status and operating constraints. | Do not confuse one provider's team feature with a durable cross-provider organisation. App-level coordination must not depend on spawning native teams in non-interactive mode. |

The defendable claim is modest: yip deliberately combines these requirements around the user's personal multi-machine workflow. Whether it feels substantially better must be demonstrated with real work.

## 4. Buzz deserves a fair reading

### It is not a blockchain

Buzz uses signed Nostr events and relay infrastructure. A signed event protocol is not a blockchain: it does not imply blocks, mining, or a global consensus ledger. The relevant question is whether that event/federation model and its operating components are useful for this particular personal application. The proposed hub does not need them. See [Buzz's architecture](https://github.com/block/buzz/blob/main/ARCHITECTURE.md).

The reason to choose a small local Go/SQLite hub is operational scope, not a claim that Buzz puts chat on a blockchain. A fork would inherit significant product and protocol decisions. A fresh implementation gives a narrower dependency and recovery model, while forfeiting substantial existing work.

### Preserve the useful visual hierarchy

The [Projects in Buzz article](https://engineering.block.xyz/blog/projects-in-buzz) was read and its published desktop screenshot visually inspected. Soft surrounding chrome, compact navigation, a distinct inner work surface, and nearby context are useful cues. yip uses its own aqua/petrol palette, typography rules, mark, and work interactions. The reference should inform hierarchy rather than lead to copied assets.

### Read maturity labels literally

Buzz's repository distinguishes implemented surfaces from broader aspirations. Its [remote-agents document](https://github.com/block/buzz/blob/main/docs/remote-agents.md) describes a draft provider protocol. Treat a proposed remote lifecycle separately from a shipped, tested workflow. The same standard applies to this yip proposal.

Block's [22 September account of moving project work into Buzz](https://engineering.block.xyz/blog/shifting-a-project-to-buzz-for-acceleration) is evidence of a real internal use case and the team's reported experience. It is a first-party before/after account, not a controlled demonstration that the interface caused productivity gains. There is no need to promise numerical productivity improvements for yip.

### Learn from specific failure reports

| Report | General lesson | yip requirement |
|---|---|---|
| [Issue 5190: reply destination tied to an in-flight turn](https://github.com/block/buzz/issues/5190) | A new message arriving during work can expose ambiguous ownership of reply routing. | Bind each run to an immutable trigger and reply destination; test overlapping top-level messages and threads. |
| [Issue 4352: replies stream but do not persist for an additional managed profile](https://github.com/block/buzz/issues/4352) | A convincing streaming UI is not durable completion. | Persist canonical message revisions and final output before showing success; replay after refresh. |

These are reported cases, not independently reproduced bugs or statements that the current release still suffers from them. Their value here is to make our own acceptance tests more concrete.

## 5. X and Reddit: what people are actually pointing at

### X

**[Axel, 20 September](https://x.com/itsmraxel/status/2101582902727147786).** The expanded public post was read in the user's signed-in browser. Axel reports that Buzz is not quite working for him yet, finds something missing in Nebula, and frames communication between humans and agents as a UX problem. This closely matches the user's concern. The post does not specify a complete replacement design; our continuity thesis is an inference from the need, not something Axel has validated.

**[Wes Billman, 9 September](https://x.com/wesbillman/status/2097326263354663394).** A Buzz release post highlights everyday interaction fixes, including mention recipients and agent invitations. A [27 August post](https://x.com/wesbillman/status/2092625750017065200) also discusses navigation/search behaviour. These are first-party release signals, not independent satisfaction reviews. They are a useful reminder that “small” routing and navigation details decide whether a team interface feels trustworthy.

The overall X sample included both enthusiasm for agent rooms and dissatisfaction with current implementations. It does not support a one-sided claim that everyone dislikes the available products.

### Reddit

**[Multi-agent orchestration discussion](https://www.reddit.com/r/AgentsOfAI/comments/1v0ysli/is_anyone_actually_orchestrating_multiagent/).** Participants describe operational problems such as stalled work, retry behaviour, and losing track of what is happening. Treat these as anecdotes, with promotional comments in the same discussion. The design implication is useful even without prevalence data: waits need reasons, recovery must be explicit, and collaboration must have limits.

**[Agent coordination and context discussion](https://www.reddit.com/r/AI_Agents/comments/1v2nk2a/how_do_you_handle_agent_coordination_without_the/).** The discussion centres on the cost of coordination and repeatedly accumulating context. The proposed response is bounded requests, summaries backed by source references, and a durable state model. It is not to place every transcript into every prompt.

**[Remote viewing of Claude work](https://www.reddit.com/r/ClaudeAI/comments/1voo9ms/is_there_a_way_to_remote_view_claude_work_on/).** This illustrates the practical desire to follow work from another device. Suggestions in replies were not used as authoritative integration instructions. yip's stronger requirement is that the executing process actually lives on the always-on runner, independent of the viewing device.

### Translate the signal into a falsifiable experience

| Observed concern | Proposed response | How to discover that our hypothesis is wrong |
|---|---|---|
| “These tools are close, but something is missing.” | Preserve one connected loop from request to owner to collaborator to evidence. | Users still prefer separate provider windows even when the loop works reliably. |
| Repeating context and passing messages between agents | Stable engineers, scoped decisions, direct bounded colleague requests. | People repeatedly paste information the app should already be able to retrieve. |
| Unclear progress or silent waiting | Confirmed activity, named blocker, durable questions and recovery. | Users still cannot explain what is running or what needs them. |
| Too much agent chatter | One owner responds; routine coordination can be expanded. | Users mute rooms or cannot find outcomes without reading the entire log. |
| Work tied to a laptop | Always-on hub/runner and resumable browser client. | Closing the client changes job execution or loses its result. |

Use the realistic walkthrough and acceptance matrix in the MVP brief. First test whether the user can return after an interruption, understand the situation, answer the needed question, inspect a result, and move on. A pleasant screenshot is necessary for this brief but cannot answer that test.

## 6. Provider integration findings

The subscription requirement is feasible only through supported, account-specific provider integrations. “Local agents” describes where the harness and tools run; a cloud model still receives the selected context. Provider policies, installed versions, and plan capabilities must be verified at implementation and distribution.

| Integration | Primary evidence | Design decision |
|---|---|---|
| Codex | [App Server documentation](https://learn.chatgpt.com/docs/app-server) describes client integration, authentication, thread/turn lifecycle, approvals, and streaming. | Run the supported app-server process beside the runner over stdio. Generate bindings from the tested installed version. Do not expose a raw unauthenticated provider transport over the network. |
| Claude Code CLI | [Programmatic use](https://code.claude.com/docs/en/headless) and [CLI reference](https://code.claude.com/docs/en/cli-reference) cover structured output, resume, and a permission-prompt tool. | Use an unmodified CLI and a tested approval bridge. An unanswered print-mode permission request is not a working approval UI. Check repository hooks/configuration before launch. |
| Claude authentication | [Integration guidance](https://code.claude.com/docs/en/legal-and-compliance) distinguishes supported unmodified-binary authentication from collecting or intermediating credentials. [Agent SDK guidance](https://code.claude.com/docs/en/agent-sdk/overview) describes a separate API authentication route. | Keep the user's provider-managed login on the executing machine. Do not repurpose subscription credentials as an SDK API key or pool another person's login. |
| Cursor | [ACP integration](https://cursor.com/docs/cli/acp) and [CLI authentication](https://cursor.com/docs/cli/reference/authentication) provide a native integration path. | Prefer `agent acp`; implement permission requests and blocking Cursor extensions, including user questions/plans. Probe capability support rather than assuming all tools work identically. |

The [ACP protocol](https://agentclientprotocol.com/protocol/v1/overview) gives a useful session/prompt/update/permission boundary. It does not supply an organisation, a scheduler, source-scoped memory, or durable job recovery. Keep ACP inside a provider adapter instead of making it the entire product architecture.

Shared quotas belong to the actual account, even when several engineers use it. A friendly persona is not a new allowance. Unknown usage must be displayed as unknown. A provider limit must not silently trigger paid API fallback. No provider plan or contractual entitlement is guaranteed by this proposal.

## 7. Why this technical stack

These are recommendations for this workload, not external benchmark claims.

| Decision | Rationale and relevant reference |
|---|---|
| Go hub and runner | One distributable service for the user's Macs/Linux boxes, explicit process supervision, strong standard networking and concurrency support. The architecture contains the operational details; language choice is not a claimed performance measurement. |
| Svelte 5 + TypeScript | The interface has enough live state, draft persistence, keyboard behaviour, and streaming content to justify components. Plain DOM code could implement it but would make repeated state coordination more manual. [Svelte documentation](https://svelte.dev/docs/svelte/overview). |
| SQLite on local disk | Appropriate initial administrative footprint and transaction model. WAL is not a shared network-filesystem database design. [SQLite WAL documentation](https://sqlite.org/wal.html). The proposed [Go SQLite driver](https://pkg.go.dev/modernc.org/sqlite) must be pinned and its required features tested. |
| Worktree per code job | Separate working directories and branches without repeatedly recloning the same local object store. It is source-control isolation, not a security sandbox. [Git worktree documentation](https://git-scm.com/docs/git-worktree). |
| Native execution plus explicit container profile | Mac-native builds need the host environment. Containers can constrain other workloads but introduce a Linux VM on Mac and do not make hostile code harmless automatically. [Docker Desktop VMM documentation](https://docs.docker.com/desktop/features/vmm/). |
| Private network access, optional Tailscale | The product owns authentication and runner identity. A private connectivity layer can simplify reachability; it does not replace authorisation. [Tailscale Serve](https://tailscale.com/kb/1312/serve). |
| Accessibility as acceptance criteria | Keyboard navigation, focus, scaling, labels, contrast, and reduced motion must be tested in the implemented product. [WCAG 2.2 reference](https://www.w3.org/WAI/WCAG22/quickref/). |

No Redis, external broker, Kubernetes, mandatory cloud control plane, federation, or full Git forge is required for the specified first release. Revisit a component when a measured need or a changed product requirement justifies it.

## 8. Name and brand screen

### Selected working name: yip

On 25 September 2026 the user selected **yip**, having been told that neither yip nor yap is completely unused. Use lowercase `yip` in the wordmark and prose. `getyip.dev` is a possible domain, not a purchased or confirmed registrar reservation. The user prefers names such as Buzz, Insomnia, and Zed; yip follows the short, distinctive direction.

| Check on 25 September 2026 | yip | yap |
|---|---|---|
| Exact npm name | [Taken: Electron music player](https://registry.npmjs.org/yip) | [Taken: Promise module](https://registry.npmjs.org/yap) |
| `.com` registration | [Registered](https://rdap.verisign.com/com/v1/domain/yip.com) | [Registered](https://rdap.verisign.com/com/v1/domain/yap.com) |
| `.dev` registration | [Registered](https://pubapi.registry.google/rdap/domain/yip.dev) | [Registered](https://pubapi.registry.google/rdap/domain/yap.dev) |
| `.app` registration | [Registered](https://pubapi.registry.google/rdap/domain/yip.app) | [Registered](https://pubapi.registry.google/rdap/domain/yap.app) |
| `get… .dev` registration | [getyip.dev: registry returned 404](https://pubapi.registry.google/rdap/domain/getyip.dev) | [getyap.dev: registry returned 404](https://pubapi.registry.google/rdap/domain/getyap.dev) |
| Existing product examples | [Yip text sharing](https://yip.chat/), [yip mesh VPN](https://github.com/femboyisp/yip) | [Yap AI group chat](https://heyyap.app/), [Yap agent voice software](https://github.com/latent-variable/Yap) |

The preference for yip is a judgment about the observed naming landscape, not a claim of global uniqueness or trademark clearance. Registry 404 means no registration record returned; a registrar must confirm availability, reservation status, and price. The unscoped npm names are unavailable; any package distribution needs a separately verified namespace. No domain, handle, package, or trademark was reserved or purchased.

The new prototype mark is a compact branching **y** in the existing petrol, aqua, white, and yellow palette. It replaces the earlier folded-wing direction.

### Historical rejected direction


**Historical proposal: Deskflock — rejected by the user.** The checks below record the original screen, not a current recommendation. Sidehall, Rooklet, and Covelet were also rejected before the user selected yip. The earlier folded-shape icon has been replaced.

Checks performed on 25 September 2026:

| Check | Observed result | Limit |
|---|---|---|
| Exact web query `"Deskflock"` and broader software queries | No exact-name result found | Search engines are incomplete and can miss new/private uses. |
| [Public GitHub repository search API](https://api.github.com/search/repositories?q=deskflock) | `total_count: 0`, not marked incomplete | Does not check private repositories or all identifiers. |
| [npm package endpoint](https://registry.npmjs.org/deskflock) | HTTP 404; package not found | Does not reserve the name or prove it can be published. |
| [Verisign .com RDAP endpoint](https://rdap.verisign.com/com/v1/domain/deskflock.com) | HTTP 404; no registration record returned | Does not establish registrar availability, lack of reservation, or rights to register. |
| Trademark registers, social handles, app stores | Not comprehensively checked | Required before a public brand commitment. |

There is a material similarity consideration: **[Flock](https://www.flock.com/) is already a team messaging product.** Do not shorten Deskflock to Flock. An unused exact compound is not the same as a cleared brand. Keep the name replaceable during private development and check the relevant official trademark registers and confusingly similar marks before public launch. Nothing was registered or purchased.

Rejected candidates, with observed uses:

| Candidate | Collision found |
|---|---|
| Benchfolk | [Existing AI visibility business](https://benchfolk.com/) |
| Crewstead | [Existing Slack-oriented product](https://crewstead.com/) |
| Benchbird | [Existing bakery software](https://www.thehealeyfarm.com/benchbird) |
| Threadstead | [Existing community usage](https://homepageagain.com/help/guidelines) and [services business](https://www.threadsteadservices.com/) |
| Patchfolk | [Existing music identity](https://soundcloud.com/user1285967) |

The historical screen does not establish clearance for yip or endorse the rejected name. The current choice and its availability limits are recorded above.

### Product refinement from direct user feedback

On 25 September, the user specified that engineers should act autonomously and ask genuine questions in the group chat like ordinary colleagues. This is a product requirement, not a market-research finding. The revised design removes the attention inbox and default human acceptance, makes routine decisions and verified completion autonomous, and limits waiting to the dependent work.

The user also asked for the working relationship to feel like a team of regular engineers. The requirements now include engineers independently choosing colleagues for peer reviews of work and pull requests, substantive critique, requests for changes, author responses, and re-review of updated revisions. The owner does not dispatch or relay that exchange. Engineers have stable names, roles, judgment, and shared history; ordinary conversation leads the UI. These are explicit product decisions, not claims that the prototype already runs real engineers.

GitHub is the proposed first forge integration. Its documented [review model](https://docs.github.com/en/pull-requests/reference/pull-request-reviews) supports comments, change requests, and approvals. The [reviewing guide](https://docs.github.com/en/pull-requests/how-tos/review-pull-requests/reviewing-proposed-changes-in-a-pull-request) states that PR authors cannot approve their own PRs. Consequently, the specification distinguishes an internal engineer's review from the actual remote actor's approval and merge eligibility, especially when several engineers share one credential. The implementation must validate [review APIs](https://docs.github.com/en/rest/pulls/reviews) and [review requests](https://docs.github.com/en/rest/pulls/review-requests) against its pinned integration version.

## 9. Open questions that require a real product

1. Does returning to the same engineer feel useful after several projects, or do users prefer explicit project-local personas? Test both while preserving access boundaries.
2. How often should an unmentioned room steward respond? Begin conservatively and make quiet mode easy to choose.
3. Can collaborator summaries remain useful without hiding disagreement? Inspect real review threads and expandability.
4. Which provider versions support all needed lifecycle and permission operations reliably? Record real adapter tests; never substitute the fake-provider suite for this.
5. How does an always-on Mac behave after reboot, FileVault unlock, provider login expiry, and keychain changes? Validate unattended readiness explicitly.
6. Is the result sufficiently better than using Buzz, Happier, or a provider's own remote tools? Compare the same ordinary workflow rather than a curated demo.

The three primary documents turn these uncertainties into testable requirements while keeping the product focused on the user's original request.
