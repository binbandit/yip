# Your first real session

A walkthrough for running yip with the Claude Code or Codex sign-in you
already have, from an empty hub to a first finished piece of work. It ends
with the spec's experience tests
([`spec/02-product-and-design.md` §11](spec/02-product-and-design.md)), so you
can record what worked and what didn't.

Everything here uses your own account and allowance. yip never asks for,
reads, or stores provider tokens: each provider signs in with its own tool,
and yip asks the CLI whether it's signed in.

## 1. Sign in where the work will run

On each machine that will run engineers, as the user the runner runs as:

```sh
claude auth login        # Claude Code: your Claude subscription
codex login              # Codex: your ChatGPT sign-in
```

Either is enough. yip does not pass `ANTHROPIC_API_KEY` or `OPENAI_API_KEY`
from the runner's environment, so a subscription sign-in is never silently
swapped for API billing.

## 2. Start the hub and a runner

On one machine, the quickest way is a hub with a local runner:

```sh
yip hub --data ~/.yip/hub --local-runner
```

Open the address it prints, enter the one-time setup code, and create the
owner. To try things first with scripted engineers next to your real ones,
use `yip demo --with-providers claude,codex` instead. The banner then says
which engineers use your account.

Other machines pair from **Machines → Add machine** ([operations](operations.md#add-machines)).
The dialog watches for the machine and, once it connects, shows each provider
it found with its sign-in, billing, and account.

## 3. Check Machines

For each machine, **Machines** shows every provider as *Signed in*, *Needs
sign-in* (with the exact command to run there), or *Not installed*, along
with its billing (subscription, API billed, or unknown) and account. After
signing in, choose **Check sign-in again**. Otherwise the runner re-checks
every five minutes.

**Runs at once on this account** defaults to 1. Runs on one account share
its allowance, so raise it only if your plan allows. If the allowance runs
out, the whole account pauses until the reset time the provider reports.
Machines says so, and its work carries on by itself afterwards.

## 4. Give engineers a provider

Open an engineer (**Engineers → Mira**) and set:

- **Provider:** Claude Code or Codex.
- **Model:** optional. The machine's list is used when the provider reports
  one.
- **Account:** optional. Pin the engineer to one signed-in account if you
  have several.
- **Allow runs billed to an API key:** off by default. Leave it off unless
  you mean to pay per token. With it off, an API-billed installation is never
  used for this engineer, and the work says why it's waiting.

The **Getting started** checklist on Overview follows these steps and ticks
them off from live state.

## 5. A room, a project, a first task

1. Create a room (or use one) and add two engineers. With only one engineer
   in the conversation, code work is reviewed by you instead of by a peer,
   and waits for your acceptance.
2. Link a project and connect its repository when you want code inspected or
   changed. You can talk without one.
3. Ask for something small and real, mentioning an engineer:
   *"@Mira can you find why the session refresh test is flaky? Don't change
   anything yet."*

What to expect:

- An ordinary reply in the room, then a work item in the strip: owner,
  machine, and last confirmed activity.
- The drawer (select the work) shows its short work ID, project and
  repository, changes, checks with the exact revision they ran on, reviews,
  and every attempt.
- If the engineer genuinely needs something, they ask once in the room. Your
  reply resumes the dependent step.
- **Stop** asks the machine to stop the attempt and says when it has.
  **Resume** continues stopped work.

## 6. When something is off

| You see | Meaning | Do |
|---|---|---|
| *Provider needs sign-in* | The CLI on that machine isn't signed in, or its sign-in expired | Run the command shown, then **Check sign-in again** |
| *Allowance reached* / account paused | The account's usage limit was hit | Nothing: it resumes at the reset time |
| *Waiting for a machine* | No connected machine has the provider signed in, or all are busy | See Machines; the reason names the machine |
| *billed to an API key* | Only an API-billed installation could run it | Allow API billing on the engineer, or sign in with a subscription |
| *Not confirmed* | A machine went quiet mid-run | Check before retrying anything that pushes or publishes |

`yip doctor` on a machine shows its identity, certificate, journal, and each
provider's version and sign-in state.

## 7. Verify the adapters on your machine

The real-provider smoke tests use a small amount of your allowance and are
off by default:

```sh
YIP_REAL_PROVIDER_TESTS=1 go test -run TestRealClaudeSmoke -v ./internal/providers/claude/
YIP_REAL_PROVIDER_TESTS=1 go test -run TestRealCodex -v ./internal/providers/codex/
```

Record the results in [compatibility.md](compatibility.md).

## 8. Record the experience tests

After a few days of real use, go through the spec's gates and note failures,
not impressions:

| # | Test | What to look at |
|---|---|---|
| 1 | Familiarity | Engineer profiles, and whether you'd predict who answers |
| 2 | No messenger work | Help and review requests between engineers in the room |
| 3 | Natural interruption | The receipt under your message while work runs |
| 4 | Truthful catch-up | Overview → Since you were here, and "Where are we with everything?" |
| 5 | Continuity | Decisions with sources; a private room's decision staying private |
| 6 | Trust | Work state words, checks bound to revisions, *Not confirmed* |
| 7 | Useful quiet | An idle room staying idle; no acknowledgement chatter |
| 8 | Remote confidence | "Running on …" in the strip; closing the laptop |
| 9 | Independent judgment | A fix completing with peer review while you're away |
| 10 | Natural help | One question, independent work continuing, resume on reply |
| 11 | Peer ownership | Reviewer choice, findings, revision, re-review |
| 12 | Review truth | Which revision was approved; PR state kept separate |
