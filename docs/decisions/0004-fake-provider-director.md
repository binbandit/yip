# 0004 — The fake provider and its director

**Decision.** The deterministic fake provider is a real MCP client of the
`yip bridge`, so it exercises bridge → runner → hub exactly like a provider
CLI. The hub selects its script from the run's context manifest
(`internal/providers/fake/director.go`) only for engineers explicitly
configured with provider `fake`. Scripts run real shell commands and edits in
the run's workspace (the reviewer finds the seeded Atlas defect by inspecting
the actual revision), and inject crash, hang, rate-limit, and sign-in faults.
Anything outside the scripted demo workflows gets an explicit "fake provider
can't do that" reply.

**Why.** The brief asks for deterministic fixtures and failure testing without
pretending a fake demo satisfies the real-provider gates.

**Consequence.** The demo workspace and the integration suite use it. Slices B
and C (and A28) remain **incomplete** until the real-provider smoke tests are
run; see `docs/compatibility.md` and `docs/release-checklist.md`.
