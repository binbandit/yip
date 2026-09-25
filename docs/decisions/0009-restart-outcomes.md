# 0009 — Runner restarts report unknown outcomes

**Decision.** When a runner process restarts while an attempt was running,
the journal reports that attempt as `unknown` (the provider process may have
outlived the runner), not as failed. An attempt accepted but never started is
reported failed (nothing ran). A graceful runner shutdown mid-attempt stops
the provider and reports a failure that can be retried. Retrying after an
unknown outcome is an explicit new attempt.

**Consequence.** Matches "A lost running machine produces unknown, not failed,
safe to repeat" (A13, A30).
