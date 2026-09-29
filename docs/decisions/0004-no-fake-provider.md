# 0004 — No fake provider or demo mode

**Decision.** yip ships no scripted provider and no demo mode. The build
documents ask for a deterministic fake provider, a fixture demo and
fake-provider integration tests (`spec/README.md`, `spec/03-mvp-build-spec.md`
§3, slice B and §9); yip has none of them. Every engineer runs on Codex,
Claude Code or Cursor.

**Why.** The scripted engineers, the seeded workspace and the protocol fields
that carried them (`ExecutionManifest.FakeScript`, `Bootstrap.Demo`,
`ProviderSummary.Fake`) added code paths and interface special cases that
real use never reaches, and made a scripted run easy to mistake for evidence.

**Consequence.** The integration suite drives the hub in process through the
browser API and the runner protocol, with the test playing the runner, so it
covers routing, dispatch, leases, replay and recovery without a provider run.
Each adapter is tested against a double of its vendor's interface, and the
real-provider gates still need the opt-in smoke tests and campaigns
(`scripts/simulation/`). The browser journeys ran against the demo and were
removed with it; the web client relies on its unit and smoke suites. The
README screenshots predate this decision and show the old scripted workspace.
