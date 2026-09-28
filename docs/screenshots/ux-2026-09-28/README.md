# Conversation and catch-up UI evidence, 28 September 2026

These Chrome captures come from isolated test workspaces. The Atlas demo
engineers use a deterministic fake provider. The
onboarding scenes use disposable setup records; a provider name in those
screens is not evidence that a model ran. Only the recall captures below show
the separately recorded real Claude conversation. No daily-use workspace was
changed for these captures.

Earlier captures retain the notices and development controls visible when
recorded. The final production layout captures reflect the owner's request
to remove the persistent demo banner and its notice wrapper. Actual connection
failures and retry status remain part of the interface.

| Capture | What it establishes |
| --- | --- |
| [Overview before](overview-before.png) / [after](overview-after.png) | The old “Ask about everything” box answered an Atlas decision question with an unrelated work digest. The corrected Overview presents current outcomes and an explicit Workspace summary action. |
| [Overview on a phone](overview-mobile-day.png) | Catch-up and work rows at phone width in day mode, with the summary available through its separate view. |
| [Work drawer before](work-before.png) / [after](work-after.png) | The same long evidence view previously squeezed the tab bar away. Evidence, Review, Activity and Runs are visible after the fix. |
| [Follow-up draft transition](follow-up-live.png) | A draft stays addressed when its selected work finishes. This is a transient live frame with review detail still catching up, not final review evidence. |
| [Follow-up target](follow-up-target.png) | The original completed assignment remains selected while a separate assignment in the room waits for an answer. |
| [Follow-up result](follow-up-after.png) | The follow-up and engineer response appear in the original assignment's thread, beside the independent active work. |
| [Onboarding before](onboarding-before.png) / [after](onboarding-after.png) | Unrelated objects no longer imply readiness. The guide points to the actual missing author access and reviewer membership. |
| [Onboarding while offline](onboarding-offline.png) | Existing setup stays completed while current machine availability is explained separately. |
| [Engineer profile setup links](onboarding-profile-after.png) | An engineer without room membership or project access gets direct links to the appropriate choices. |
| [Questions before answering](questions-before-answer.png) / [after answering](questions-after-answer.png) | The owner's answer resolves its correlated question and resumes the dependent work; Overview's Needs a look count changes from two to one while the unrelated failure remains. |
| [Real-provider recall before](real-provider-recall-before.png) / [after](real-provider-recall.png) | Claude recalls both reviewed results, corrects the mistaken feature name, distinguishes local results from publication and states the missing chronology. The corrected rendering supports bold labels containing inline code. |
| Phone: [day](room-mobile-day.png), [night](room-mobile-night.png), [night before formatting fix](room-mobile-night-before.png) | The real answer and composer at 390px. The earlier capture preserves the literal bold delimiters that prompted the formatting regression. |
| 900px room: [day](room-900-day.png), [night](room-900-night.png) | Conversation layout at tablet width. |
| 1280px room: [day](room-1280-day.png), [night](room-1280-night.png) | Conversation layout at compact desktop width. |
| 1440px room: [day](room-1440-day.png), [night](room-1440-night.png) | Conversation layout at wide desktop width. |
| [Tablet evidence](evidence-tablet-night.png) | Evidence opens as an overlay at tablet width. The browser journey also checked that Escape returns focus to the original work row. |

All four room widths were checked in both themes without horizontal overflow.
Screenshots document the visible layout; the focus return, live refresh and
answer/resume claims also rely on the recorded browser interactions and
automated regressions.

The [UX campaign report](../../simulations/2026-09-28-ux.md) records behavior,
automated checks and remaining limits. The
[real recall record](../../simulations/2026-09-28-ux-real-recall.json) preserves
the full request and answer. The earlier
[adversarial campaign](../../simulations/2026-09-28.md) is a separate body of
evidence; these screenshots do not imply every scenario or enterprise feature
has been verified.
