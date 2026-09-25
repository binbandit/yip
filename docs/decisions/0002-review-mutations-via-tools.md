# 0002 — Review mutations through the agent tool path

**Decision.** The brief lists `POST /v1/reviews/{id}/findings`, `/verdict`,
and `/revisions` and `POST /v1/jobs/{id}/reviews`. In yip these operations are
performed by engineers, so they are exposed as authenticated agent tools
(`work_request_review`, `work_review`, `work_respond_to_review`) bound to the
calling run's lease. The browser API exposes `GET /v1/reviews/{id}` and the
job detail, and the owner can link PRs (`POST /v1/pull-requests/link`).

**Why.** The single human owner is never the reviewer in this release, and
binding reviewer identity to the run (not to a request parameter) is what
enforces "reviewer-only decision" and "author can't dismiss a blocking
finding". The semantics the brief requires — expected round and revision,
freshness validation, evidence on blocking findings, supersession — are
enforced in those tools.

**Consequence.** A36–A44 are exercised through the tool path
(`test/integration`). Adding human reviewers later means adding browser
endpoints over the same hub functions.
