# Team conversation room comparisons

Security room, with the scripted Atlas expiry change, requested changes,
author correction, re-review and completed result. Every capture uses a
throwaway hub; no real provider account or live workspace data is used.

The 390, 1280 and 1440px before images are copied unchanged from the supplied
[Machines redesign baseline](../machines-redesign/README.md), recorded at
`fd56c0b`. The missing 900px baseline was captured from the preserved
`71f9656` binary; its room UI is identical to `fd56c0b` (the intervening web
change only adds work-record labels in Engineer Notes). The after set uses
the completed conversation changes, including truthful review labels,
concise room messages and one result announcement.

| Width | Light before | Light after | Dark before | Dark after |
|---|---|---|---|---|
| 390px | [Before](before/room-security-390-light.png) | [After](after/room-security-390-light.png) | [Before](before/room-security-390-dark.png) | [After](after/room-security-390-dark.png) |
| 900px | [Before](before/room-security-900-light.png) | [After](after/room-security-900-light.png) | [Before](before/room-security-900-dark.png) | [After](after/room-security-900-dark.png) |
| 1280px | [Before](before/room-security-1280-light.png) | [After](after/room-security-1280-light.png) | [Before](before/room-security-1280-dark.png) | [After](after/room-security-1280-dark.png) |
| 1440px | [Before](before/room-security-1440-light.png) | [After](after/room-security-1440-light.png) | [Before](before/room-security-1440-dark.png) | [After](after/room-security-1440-dark.png) |

Retake the after set with an already built binary and existing dependencies:

```sh
scripts/e2e/run-webkit.sh docs/screenshots/team-conversation/after scripts/e2e/shots/room.js
```

The helper disables animations, opens the completed conversation, scrolls to
the result and checks for page overflow. `YIP_ROOM_SHOT_BIN` can select a
preserved baseline binary. The original 390px baseline is a 2x capture
(780 image pixels); both represent a 390 CSS-pixel viewport. The 1280px
views retain the original 800px height. Both themes use the same fixture. A difference
in message positions can also reflect the shorter text and scroll position.

The complete scripted journey is `web/tests/webkit/team-conversation.js`.
It adds a clarification, question and answer, checks one assignment and one
completion announcement, recalls the result in another room, and opens the
Overview review evidence with Enter. The shared room journeys additionally
exercise mention navigation, Tab, Shift-Tab, Escape, search shortcuts, focus
containment/return, resizing and 200% zoom. Counts and real-provider gaps
are recorded in [the release checklist](../../release-checklist.md).

All 34 scripted journeys passed in one invocation, followed by all eight
room capture checks.

## Overview layouts

After correcting the overlapping side panels, all 16 Overview checks passed
at four widths in both themes. Review evidence replaces the optional
conversation panel; catch-up rows and setup controls wrap to their available
column width. Setup starts compact once recorded work exists.

Each check expands and collapses setup and checks for clipped controls and
horizontal overflow. Review checks open evidence with Enter, close it with
Escape, verify that the conversation returns where space permits, and reopen
the evidence for the capture.

| Width | Light summary | Light review | Dark summary | Dark review |
|---|---|---|---|---|
| 390px | [Summary](overview/overview-summary-390-light.png) | [Review](overview/overview-review-390-light.png) | [Summary](overview/overview-summary-390-dark.png) | [Review](overview/overview-review-390-dark.png) |
| 900px | [Summary](overview/overview-summary-900-light.png) | [Review](overview/overview-review-900-light.png) | [Summary](overview/overview-summary-900-dark.png) | [Review](overview/overview-review-900-dark.png) |
| 1280px | [Summary](overview/overview-summary-1280-light.png) | [Review](overview/overview-review-1280-light.png) | [Summary](overview/overview-summary-1280-dark.png) | [Review](overview/overview-review-1280-dark.png) |
| 1440px | [Summary](overview/overview-summary-1440-light.png) | [Review](overview/overview-review-1440-light.png) | [Summary](overview/overview-summary-1440-dark.png) | [Review](overview/overview-review-1440-dark.png) |

Retake with an already built binary and existing dependencies:

```sh
scripts/e2e/run-webkit.sh docs/screenshots/team-conversation/overview scripts/e2e/shots/overview.js
```
