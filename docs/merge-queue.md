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

A real conflict or failing integration test still needs a fix. Resolve it on the
original pull request and get the updated code reviewed. Mergify automatically
queues it again once it is eligible.
Do not resolve conflicts by editing Mergify's temporary branches.

## Repository settings

The queue uses two speculative CI slots and merges one pull request at a time.
All four required CI jobs run again on the temporary integration pull request.
Pullfrog's checks and GitHub's review approval gate the original pull request.

GitHub's rules restrict updates to `main` to Mergify. The original PR must pass
its required checks and review, but its branch does not have to be up to date:
the queue validates the integration instead. The linear-history rule excludes
only `mergify/merge-queue/**` temporary branches; `main` and contributor branches
remain linear.

Merge Queue and Merge Protections must both be activated for this repository in
the Mergify dashboard. Do not disable either product without restoring GitHub's
up-to-date requirement and adjusting the rule restricting updates to `main`.
