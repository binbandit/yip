# Architecture decision records

Departures from, or interpretations of, the build documents in `docs/spec/`.
Each record states the decision, why, and the consequence for the acceptance
criteria.

| # | Decision |
|---|---|
| [0001](0001-hub-package.md) | Orchestration lives in `internal/hub`; `internal/domain` stays pure |
| [0002](0002-review-mutations-via-tools.md) | Review, finding, and revision mutations are agent-tool operations, not browser endpoints |
| [0003](0003-container-profile.md) | The container profile is a runner running inside a restricted container |
| [0004](0004-fake-provider-director.md) | The fake provider runs hub-selected scripts; it never stands in for real-provider gates |
| [0005](0005-evidence-from-the-runner.md) | Checks and revisions count only when the runner produced them; checks run policed and without credentials |
| [0006](0006-approval-policy.md) | Deterministic, parsed mapping of permission requests and checks onto grants |
| [0007](0007-replicas-not-mirrors.md) | Runner replicas are bare clones with a remote-tracking namespace |
| [0008](0008-wakeups.md) | Only structured requests wake engineers; status never needs a run |
| [0009](0009-restart-outcomes.md) | Attempts interrupted by a runner restart are reported as unknown |
| [0010](0010-tooling.md) | npm for the web build; generated types from Go structs |
| [0011](0011-delivery-and-completion.md) | Delivery, wakeup, and completion invariants from the backend review |
| [0012](0012-visual-direction.md) | The web client looks like Buzz, not the spec's concept; the concept was removed |
