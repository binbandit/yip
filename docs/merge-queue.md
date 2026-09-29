# Merge queue

Yip uses Mergify to test pull requests against the latest `main` before merging.
The configuration is in [`.mergify.yml`](../.mergify.yml), and the
[queue dashboard](https://dashboard.mergify.com/orgs/binbandit/repos/yip/queues/status)
shows progress and failures.

## Finishing a pull request

1. Finish the changes and mark the pull request ready for review.
2. Wait for the required CI and Pullfrog review, addressing any findings.
3. Mergify automatically queues the pull request once its required checks and
   review protections pass. No label, comment, dashboard action, or GitHub
   auto-merge setting is needed.
4. Leave the branch alone while it is queued. Mergify tests a temporary
   integration branch and squash-merges the original pull request after the
   combined code passes CI. Ordinary queue updates do not rewrite the original
   branch or restart its review.

Keep unfinished work in draft. To stop a ready pull request from being merged,
convert it back to draft and dequeue it if it has already entered the queue.

The queue retries failed integration checks up to twice automatically, so a
transient failure does not immediately require someone to requeue the PR.
A real conflict or persistently failing integration test still needs a fix. Resolve it on the
original pull request and get the updated code reviewed. Mergify automatically
queues it again once it is eligible.
Do not resolve conflicts by editing Mergify's temporary branches.

## Repository settings

The queue uses two speculative CI slots and merges one pull request at a time.
All four required CI jobs run again on the temporary integration pull request.
Pullfrog's checks are explicit queue-entry conditions, and GitHub's review
approval gates the original pull request. Keep the two Pullfrog checks out of
GitHub's required-check list and Mergify's merge protections: those checks are
otherwise copied onto temporary integration PRs and trigger redundant reviews.
GitHub still requires the four CI checks, Mergify Merge Protections, and an
approving review; only Mergify can update `main`.

GitHub's rules restrict updates to `main` to Mergify. The original PR must pass
its required checks and review, but its branch does not have to be up to date:
the queue validates the integration instead. The linear-history rule excludes
only `mergify/merge-queue/**` temporary branches; `main` and contributor branches
remain linear.

Keep Pullfrog review enabled for ready PRs, turn off its draft-PR reviews, and
leave its separate auto-merge feature off. Mergify creates draft integration PRs
for CI and handles all merges.

Merge Queue and Merge Protections must both be activated for this repository in
the Mergify dashboard. Do not disable either product without restoring GitHub's
up-to-date requirement and adjusting the rule restricting updates to `main`.
