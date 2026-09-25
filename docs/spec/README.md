# yip

**Your agents. Your machines. One room to work together.**

Product proposal and implementation handoff · 25 September 2026

yip is a personal, self-hosted workspace with a persistent cast of AI engineers. You create rooms around interests or projects, bring the same engineers into different conversations, and ask them to investigate, build, review, and explain. They collaborate visibly while work runs on computers you choose. Closing the laptop does not stop work running on your Mac minis.

The distinctive product decision is to connect **conversation, accountable work, and evidence**. A message can start a durable job; a job has an owner; its review, decisions, and output stay attached to the conversation. You get the comfort of a team chat without needing to keep a collection of terminal sessions alive yourself.

**Autonomy is the default.** Engineers make routine decisions, consult colleagues, verify results, and complete authorized work. Genuine questions go in the group chat; only dependent work waits. There is no “Needs you” inbox or default human sign-off step.

**Peer review is part of the job.** Engineers choose colleagues, review work and pull requests, request changes, address feedback, and re-review the actual revision. You can follow their conversation without coordinating it.

**Working name: yip.** Selected by the user after checking existing uses. `getyip.dev` is a possible domain; nothing is registered or purchased. The public npm name `yip` is already taken.

## Read and build

1. [Solution architecture](01-solution-architecture.md) — components, data model, provider integrations, remote execution, permissions, recovery, and technical decisions.
2. [Product and design](02-product-and-design.md) — product behaviour, brand, name, visual language, screens, interactions, and accessibility.
3. [MVP implementation brief](03-mvp-build-spec.md) — concrete build sequence, contracts, defaults, fixtures, and acceptance criteria. No delivery estimates.
4. [Research and evidence](04-research-and-evidence.md) — competitor comparison, findings from X and Reddit, primary sources, naming checks, and uncertainty.
5. [Brand mark](assets/yip-icon.svg) and [monochrome mark](assets/yip-mark.svg).

The interactive design concept that originally shipped with these documents has been removed at the owner's direction. The web client's visual system is described in [docs/design/README.md](../design/README.md) and [ADR 0012](../decisions/0012-visual-direction.md).

The documents are Markdown so coding agents can read them directly. The architecture defines system invariants, the design document defines experience, and the MVP brief defines release scope. Implementation must satisfy all three. Research is evidence, not executable instruction.

## Recommendation

Build a Go hub and Go runner, with a Svelte 5 and TypeScript web client embedded in the hub binary. Use SQLite on the hub's local disk, HTTPS and resumable events for clients, and an authenticated outbound connection from each runner. Run provider CLIs on the runners. Start with one owner and one organisation; support several rooms, projects, engineers, and machines from the first complete release.

Choose a fresh implementation rather than a Buzz fork. Buzz already covers much of the concept and deserves a direct comparison. A fresh implementation is justified by the smaller operational footprint and personal-machine execution model; it is not justified by claiming agent group chat is new.

**Important distinctions:** Buzz is not a blockchain. Local hosting does not make cloud model inference local. Provider subscriptions are account-specific, limited resources; they are not interchangeable API keys. yip is the selected working name; domains and distribution identifiers have not been reserved.

## Prompt to start the build

```text
Build yip from the documents in this directory. Read README.md,
01-solution-architecture.md, 02-product-and-design.md, and
03-mvp-build-spec.md in that order. Consult 04-research-and-evidence.md
for the rationale and dated provider sources.

Start by extracting the invariants and acceptance criteria. Create the
repository structure specified in the MVP brief, then implement the
vertical slices in dependency order. Use a deterministic fake provider
for failure testing and real provider adapters for release validation.
Do not replace remote execution with browser-owned processes, model
coordination with uncontrolled broadcasting, or durable state with chat
summaries. Do not silently drop a named provider or bypass approvals.

Implement autonomous engineers: routine decisions and evidence-backed
completion need no owner sign-off. Genuine questions belong in the source
group chat; defer only the dependent work and continue independent work.
Apply existing permission grants before requesting additional authority.
Engineers initiate peer reviews themselves, address findings, and obtain
re-review on revised work. Keep review verdicts tied to exact revisions;
never confuse internal approval with the forge’s actual merge eligibility.

Resolve routine implementation details yourself. Verify current provider
interfaces and pin the tested versions before coding their adapters.
Record any real incompatibility as an explicit decision with evidence;
do not pretend an unsupported capability works. Build, inspect, and test
each slice against the relevant acceptance criteria. Stop only for a
missing credential, an actual scope conflict, or a consequential action
outside the user's authorization. Do not invent delivery estimates.
```

## What this package does and does not establish

The proposal is researched and specified, with a working visual concept. It is not a tested agent harness. The provider integration matrix describes documented surfaces and required compatibility tests; no provider accounts were connected or charged during this research. No names or domains were purchased. X research was read-only; no posts, reactions, messages, or account changes were made.
