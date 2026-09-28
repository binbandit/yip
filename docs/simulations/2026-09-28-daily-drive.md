# Daily-driver session, 28 September 2026

One person used yip as their everyday agent harness for an evening: a fresh
hub with its local runner on a MacBook, real Claude Code (2.1.283) and Codex
(0.157.1) sign-ins, and two private GitHub projects with issues filed by other
accounts. Everything was driven through the web client in Chrome, the way a
user would, with screenshots checked at each step. Problems were fixed as they
were found, then the journey continued.

## Setup

- Hub: `yip hub --local-runner --local-providers codex,claude` on isolated
  ports and a scratch data directory. The daily-use hub was not touched.
- Engineers: Mira (platform, Claude Code), Oren (security, Claude Code),
  Pip (test, Codex).
- Projects: [pocketledger](https://github.com/binbandit/yip-sim-pocketledger)
  (Go CLI and API) and [trailnotes](https://github.com/binbandit/yip-sim-trailnotes)
  (Python site builder). Both are synthetic, with real seeded bugs that their
  tests don't catch: month-end rows dropped from summaries, `12.5` parsed as
  12.05, CSV quoting, and Unicode page slugs.
- GitHub: `binbandit` owns the repositories and authors pull requests;
  `sage-smudge` is yip's forge credential; `sage-smudge` and `WastedHippie`
  filed issues.

## What it felt like

The core loop works and is genuinely good once it gets going. Asking
"@Mira can you take issue #2?" produced a root-cause fix with a regression
test that fails on the old code, a review request with a specific question for
the reviewer, and a completion with evidence. In trailnotes, Pip (Codex)
implemented, Oren (Claude Code) found a real defect (file I/O depended on the
machine's locale), Pip fixed it with a regression test, and Oren approved the
new revision. That is the product promise working with real models.

Where it fell down was friction around the loop: the first minute of setup,
approval prompts for routine commands, dead ends when a colleague couldn't
run, and too much ceremony for small requests. Most of this session's fixes
are about that.

## Found and fixed

| Area | What happened | Now |
|---|---|---|
| First run | `yip hub --local-runner` on a fresh data directory crashed: the runner's enrollment referenced a workspace that setup hadn't created | The local runner pairs once setup finishes |
| First run | The setup code had to be copied from the terminal | The terminal prints a link with the code in its fragment, which the setup page fills in |
| First run | New engineer dialog said Claude Code "isn't signed in" while Overview said it was ready | Provider readiness follows machine reports after the page loads |
| Codex | Any `~/.codex/rules/*.rules` file (Codex writes one whenever you choose "always allow") made Codex unable to reply or review, silently | Machines explains why and offers "Allow them for reviews and replies" per machine; the owner's choice reaches the adapter. The limitation is still shown truthfully |
| Codex | Read-only Codex runs failed: the granular approval policy needs Codex's experimental API | Read-only runs use the stable `never` policy with the read-only sandbox |
| Reviews | Mira asked Pip, who could never run a review; the work sat "queued" | A request to a colleague who can't run is refused with the reason and who can review instead; colleagues' review ability is in the author's context |
| Reviews | Nobody could withdraw that request, so Oren's approval couldn't complete the work | `work_withdraw_review`; completion explains when an open review can't start |
| Reviews | Re-publishing an approved commit (to open its PR) triggered a second review of identical code | An approval is bound to its exact revision and counts in follow-up work |
| Reviews | Document reviews had only the document, so Oren couldn't verify claims about the code | The reviewer also gets a read-only checkout of the repository |
| Conversation | Every question about code became a job and a peer review, because replies had no code to read | A reply gets a read-only checkout of the room's one readable repository; a quick question was answered in 20 seconds |
| Conversation | In a DM, Oren didn't know what project "issue #1" was in | A DM spans the projects that engineer is granted |
| Conversation | Adding a message to existing work also posted a narrated "I've added this to the fix job" message | The receipt is the acknowledgement; the reply stays quiet |
| Questions | Answering Mira's question showed "the work waiting on it resumed" but nothing ran | Answers resume the asker whatever it moved on to wait for, and answers that land before an attempt starts are no longer lost |
| Approvals | `$(…)` in any command needed owner approval, although its commands were already inspectable | Substitutions are classified like other commands; a computed git or gh subcommand now needs approval (it previously passed) |
| Approvals | `gh pr create --repo <this repository>` was treated as another repository | Naming the work's own GitHub repository is allowed under the grant |
| Approvals | A reviewer's `python3 -c` probe needed approval | Inline code is routine in `work_run_check`'s isolated environment, still exceptional in the engineer's own shell |
| Approvals | A run waiting on the owner held the account's only slot, stalling every other Claude engineer | Waiting runs don't count against account concurrency |
| Approvals | A multi-line command rendered as markdown headings in the room | Approval summaries use the first line |
| Recovery | Closing the laptop lid for 16 minutes failed work that had lost nothing: the hub expired leases before its own runner could report | After the hub itself was paused, machines get a lease period to report in |
| Runner | Heartbeats ignored the hub's interval until the next connection | The interval is re-read each second |
| Runner | A machine reported a run finished before freeing its slot, then declined the next offer | It cleans up and frees the slot first |
| Forge | No hint that PRs aren't tracked until a credential is added | The project page shows the one-line setup when it's missing |
| UI | Composer "Answering…" banner hid the newest message; misaligned setup fields; Enter in a policy check field saved the form and dropped the check; policy checks rendered as one string; cryptic "1m" room pill; stuck "queued" review with no reason; tool jargon ("Using yip work wait"); `/bin/zsh -lc` wrappers; walls of raw markdown in Overview and engineer notes; failed work pinned in the strip after a successful retry; DM header and empty state written for rooms | Fixed, each checked in the browser |

Each backend fix has a focused test that fails without it. Web fixes have unit
tests where they carry logic.

## Still rough

- **Verbosity.** Claude's completion messages ran to three paragraphs that
  repeat the result card; Codex posted five progress updates in two minutes.
  The writing guidance now asks for a chat-length summary; progress chatter
  would need the hub to collapse consecutive updates.
- **Ceremony for small asks.** "Open a PR" and "attach PR #5" each became
  their own job. Approval carry-over removes the repeated reviews, but a
  conversation still can't act without creating work.
- **Approval expiry while away.** A request waits 15 minutes, then the
  engineer is told no. Coming back to a failed task after a coffee break is
  jarring; the right answer is probably an engineer that parks and asks again
  when the owner returns.
- **Codex rules are all-or-nothing per machine.** Allowing them lets every
  always-allowed command (here including `git push` and `gh api`) run outside
  the read-only sandbox. `codex app-server` has no switch to ignore user rules
  for one thread; `codex exec --ignore-rules` does.
- **Default account concurrency of 1** serializes Claude engineers. A power
  user with a larger plan will want to raise it early; nothing prompts them.
- **Search** lists review jobs beside their parent work, so one fix appears
  three times.
- Not covered: two physical machines, multi-day unattended use, several
  humans, and Cursor.
