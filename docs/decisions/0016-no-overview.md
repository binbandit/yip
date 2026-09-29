# 0016 — No Overview page; yip opens in your rooms

**Decision.** The Overview page (spec 02 §6) and the owner's personal
Overview conversation (journey F) are removed, along with `GET /v1/overview`,
`POST /v1/overview/seen`, the `overview` room kind and the `lastSeenAt`
preference. `/` opens the room you were last in, or the first room. A
workspace with no rooms, or without an engineer who can work on a project in
a room with a reviewer, opens on **Getting started** (`/start`) instead,
until the owner skips it; the checklist stays in the profile menu. Existing
Overview conversations are archived by migration 0013.

**Why.** Every part of the page repeated something the rooms already show,
in a second place that had to be kept consistent with the first:

- Active, waiting and failed work is the room's work strip; finished work is
  its result card in the conversation. Each engineer's page lists their
  active and queued work, and each project lists its open work.
- Unread rooms, mentions and engineers mid-run are marked in the sidebar.
- Questions are messages in the room (spec 02 §6: "no Needs you inbox"),
  yet the page's "Needs a look" list was that inbox.
- Decisions are on their project, their engineer and in search.
- The Overview conversation accepted exactly one sentence, "Where are we
  with everything?", and refused anything else; it was a button presented
  as a chat, occupying a room of its own for every owner.

Keeping catch-up truthful across all of these (pinned visit windows,
de-duplicated assignments, current review verdicts) cost a hub endpoint, a
catch-up projection over events, a client refresh loop and their tests, for
a page that showed the same rows again.

**Consequence.** A21 (status answered from the ledger without a run) is
withdrawn with journey F; status is still never produced by waking an
engineer (0008), because every surface that shows it reads the ledger.
Experience test 4 (truthful catch-up) is judged in the rooms and the
sidebar.
