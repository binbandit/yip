# Simulation UI evidence, 28 September 2026

These are real system WebKit captures from disposable demo hubs. All people,
projects and messages in these scenes are synthetic. No live workspace was
changed. The PNGs retain the browser's native 2x capture scale.

| Scenario | Viewport | Before | After |
| --- | --- | --- | --- |
| Twelve engineers; keyboard selection in a crowded mention menu | 1440 x 900 | [Before](before/large-team-1440-night.png) | [After](after/large-team-1440-night.png) |
| Long enterprise project name in the context picker | 390 x 844 | [Before](before/project-picker-390-night.png) | [After](after/project-picker-390-night.png) |

The large-team scene originally hid the selected engineer below the menu's
scroll area and listed every member in the empty-state paragraph. The selected
row now stays visible, and the introduction stays concise.

The phone's project picker originally extended to 534px in a 390px viewport.
It now stays inside the composer, wraps long identifiers, and keeps its controls
reachable. These captures keep the picker open so the corrected bounds can be
inspected directly.

The browser regressions live in
[`simulation.js`](../../../web/tests/webkit/simulation.js) and
[`simulation-stress.js`](../../../web/tests/webkit/simulation-stress.js).
The [campaign report](../../simulations/2026-09-28.md) records the wider
recovery, review, permission and real-provider results.
