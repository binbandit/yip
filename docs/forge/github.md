# GitHub connector

`internal/forge/github` implements `forge.Connector` (see `internal/forge/forge.go`) for github.com and GitHub Enterprise Server (GHES). It uses the GitHub REST API directly through `net/http` and `encoding/json`, with no SDK. This page covers what the connector reads and writes, the guarantees it makes, and the guarantees it does not make.

It implements spec §7 "Pull request integration" and the `pull_requests`/`forge_deliveries` rows of §6 in `docs/spec/01-solution-architecture.md`, plus acceptance rows A39–A41 and A44 in `docs/spec/03-mvp-build-spec.md`.

## Configuration

```go
c := github.New(github.Options{
    Host:       "github.com",                 // or a GHES hostname
    Token:      func(ctx context.Context) (string, error) { ... },
    HTTPClient: http.DefaultClient,           // optional
    Timeout:    30 * time.Second,             // per HTTP request, including the body
})
```

| Option | Default | Notes |
|---|---|---|
| `Host` | `github.com` | Decides which URLs `ParseURL` accepts and which `RepoRef.Host` values the connector will contact. A repository on any other host is refused before a request is sent, so a token is never sent to the wrong forge. |
| `APIBase` | `https://api.github.com`, or `https://{Host}/api/v3` for GHES | Override for tests. |
| `Token` | none (unauthenticated) | Called per request normally; publication and marker reconciliation snapshot one credential for the entire operation so the checked actor cannot change mid-write. Rotation takes effect on the next operation. Sent as `Authorization: Bearer …`. |
| `Timeout` | 30s | Applied through the request context. The caller's `http.Client` is left unchanged. |
| `MaxPages` | 10 | Page cap (100 items per page) for reviews, check runs and statuses. |
| `MergeablePolls` / `MergeablePollInterval` | 3 / 1s | Bounded re-fetch while GitHub computes mergeability. A negative `MergeablePolls` disables polling. |

## API version and headers

Every request sends these headers:

- `Accept: application/vnd.github+json`
- `X-GitHub-Api-Version: 2022-11-28` (the `APIVersion` constant)
- `User-Agent: yip`

The connector never retries on its own, whether for rate limits, 5xx responses or timeouts. The caller owns scheduling.

## Endpoints used

| Connector method | Endpoint(s) |
|---|---|
| `Viewer` | `GET /user` |
| `GetPR` | `GET /repos/{o}/{r}/pulls/{n}` |
| `Files` / `FilesAt` | `GET /repos/{o}/{r}/pulls/{n}` (before and after), `GET /repos/{o}/{r}/pulls/{n}/files` (paginated, up to 30 pages × 100 = GitHub's 3000-file maximum) |
| `Checks` | `GET /repos/{o}/{r}/commits/{sha}/check-runs?filter=latest` (paginated), `GET /repos/{o}/{r}/commits/{sha}/status` (paginated) |
| `Reviews` | `GET /repos/{o}/{r}/pulls/{n}/reviews` (paginated) |
| `MergeStatus` | `GET /repos/{o}/{r}/pulls/{n}`, re-fetched while `mergeable` is `null` |
| `PublishReview` | `GET /user`, `GET …/pulls/{n}/reviews`, `GET …/pulls/{n}`, `POST /repos/{o}/{r}/pulls/{n}/reviews` |
| `FindReviewByMarker` | `GET /user`, `GET …/pulls/{n}/reviews` (paginated, up to 100 pages) |
| `VerifyWebhook` | none; it only checks the signature and parses the payload |

Pagination follows `rel="next"` in the `Link` header. A next link to a different scheme or host is refused, so the credential stays with GitHub.

### Token permissions

These are the minimum permissions for a fine-grained token or GitHub App user token:

- Pull requests: read, plus write to publish reviews.
- Contents: read.
- Checks: read.
- Commit statuses: read.
- Metadata: read.

A classic token needs `repo` for private repositories.

If Checks or Commit statuses are unreadable, `Checks` returns `unknown`. It does not fail, and it never reports `success`.

GitHub App installation tokens cannot call `GET /user`, so `Viewer` does not support them (see [Limitations](#limitations)).

## Identity, eligibility and shared credentials

The credential determines the remote actor, and `Viewer` returns that actor. The spec's rules follow from that:

- **One token is one remote actor.** When several yip engineers share a token, they are one GitHub reviewer. Three engineers approving through one token add up to one GitHub approval by that login, never three.
- **An internal approval is not a remote approval, and a remote approval does not make a PR mergeable.** yip records "Oren approved revision X" separately from the reviews that `Reviews` returns. `MergeStatus` reports only what GitHub says about merging. Neither result is derived from the other.
- **Authors cannot approve their own PR.** Before posting, `PublishReview` compares the viewer login with the PR author (case-insensitive). If they match and the event is `APPROVE` or `REQUEST_CHANGES`, it returns `forge.ErrIneligible` and makes no POST. `COMMENT` is allowed. GitHub enforces the same rule itself: a 422 containing "Can not approve your own pull request", "Can not request changes on your own pull request" or "GitHub Actions is not permitted to approve pull requests" also maps to `ErrIneligible`.
- **The connector never invents an identity.** Attribution text in a review body ("Oren reviewed this…") is only text. GitHub does not count it as a reviewer or as a branch-protection approval.

## Revision binding

- `GetPR` returns the exact `Base` and `Head` SHAs, both refs, the author, `State` (`open`, `closed`, or `merged` when GitHub reports `merged: true` or a `merged_at`), `Draft`, and the canonical `html_url`. After a rename or transfer, the repository identity comes from the canonical `base.repo`.
- `Files` reads the PR head and base, lists the files, and reads the PR again. If the head or base moved in between, it returns `forge.ErrStale`, so the listing always belongs to one revision.
  - `FilesAt(…, expectHead)` also returns `ErrStale` when the head is not the expected SHA.
  - GitHub omits `patch` for binary and very large files.
  - If the listing is shorter than the PR's `changed_files`, for example past GitHub's 3000-file cap, the partial list is returned together with `ErrTruncated`.
- `PublishReview` re-fetches the PR right before posting. It returns `forge.ErrStale` when the PR is closed or merged, or when its head is not `req.CommitID`. The POST pins `commit_id` to that verified head.

## Publishing, idempotency and reconciliation

`PublishReview(ctx, repo, n, req)` runs these steps in order:

1. Validates the request locally. The marker must match `[A-Za-z0-9][A-Za-z0-9._:-]{0,127}` and must not contain `--`. The event must be `APPROVE`, `REQUEST_CHANGES` or `COMMENT`. The commit must be a hex SHA. Each comment needs a path, a positive line and a body. No API call is made until these pass.
2. Calls `GET /user` to find the viewer.
3. Looks for an existing review by the viewer that contains the marker. If one exists, it returns that review's ID without posting, so a blind retry cannot create a duplicate. This check runs before the stale check on purpose: a review that was already published is a fact, even if the PR has moved since.
4. Re-fetches the PR and applies the stale and eligibility checks described above.
5. Appends `<!-- yip-review:{marker} -->` to the body. `MarkerComment(marker)` returns this string. It is invisible when rendered but present in the raw `body`.
6. `POST …/pulls/{n}/reviews` with `commit_id`, `event`, `body` and `comments[]` (`path`, `line`, `side: "RIGHT"`, `body`).
7. Returns the GitHub review `id` as the external ID.

The POST outcome maps to errors as follows. All errors wrap forge sentinels, so `errors.Is` works.

| Outcome | Error |
|---|---|
| 422 self-review refusal (see above) | `forge.ErrIneligible` (and `*APIError`) |
| 422 "not part of the pull request" or closed/merged PR | `forge.ErrStale` |
| 403 (not a rate limit) | `forge.ErrForbidden` |
| 401 | `ErrBadCredentials` and `forge.ErrForbidden` |
| 404 / 410 | `forge.ErrNotFound` |
| 403/429 rate limit | `*RateLimitError` (`errors.Is(err, github.ErrRateLimited)`); GitHub rejected the request, so nothing was posted |
| Timeout, including the caller's context deadline | `forge.ErrAmbiguous` (also wraps `context.DeadlineExceeded`) |
| Connection broken after the request headers were written | `forge.ErrAmbiguous` |
| 5xx | `forge.ErrAmbiguous` (and `*APIError`) |
| 2xx with an unreadable body or no review ID | `forge.ErrAmbiguous` |
| Connection refused or DNS failure before anything was sent | Plain transport error, not ambiguous |

After `ErrAmbiguous`, the caller must call `FindReviewByMarker` before retrying. `FindReviewByMarker` scans the reviews for one whose body contains the exact marker comment:

- Only reviews written by the viewer count. Anyone with read access can see the marker in the raw markdown, so a copied marker in someone else's review is ignored.
- Pending drafts do not confirm publication. A dismissed submitted review still records a past publication and prevents a duplicate.
- If the scan cannot finish, because there are more than 100 pages of reviews, it returns an error wrapping `ErrTruncated` rather than `found == false`. A false "not found" would lead to a duplicate review.

The connector does not claim exactly-once delivery. GitHub's review listing can briefly lag a successful create, so a reconciliation that runs immediately after a timeout may miss the review. Wait before reconciling, or reconcile again later. Callers should journal the marker before the publish attempt.

The hub claims one canonical delivery record per review round before sending.
A retry after a definite failure retains the failed attempt for audit and
takes over that canonical key atomically. Confirmed success returns the saved
external ID without consulting a possibly lagging listing. Concurrent calls
cannot both claim the round. Pending or unknown outcomes remain unresolved
when the marker is absent; the tool explains that remote verification and
later reconciliation are needed and sends no replacement review. There is
currently no automatic reset of an unresolved publication.

For an existing journal written by older versions, the hub recognizes retry
rows under suffixed keys and atomically adopts a confirmed or unresolved
attempt before considering another send. This also prevents duplicate
publication after upgrading and rotating the credential.

The hub saves the response under a bounded context that survives cancellation
of the provider request. If that save fails, the tool reports an unresolved
outcome, not confirmed success. It does not report a cached PR viewer as the
identity of a new publication; those credentials may have changed.

## Checks

`Checks(repo, sha)` combines the latest check runs (`filter=latest`) with the individual commit statuses. It counts the statuses one by one because GitHub reports the combined `state` as `pending` when a commit has no statuses at all.

| Item | Counted as |
|---|---|
| Check run `completed` with `success`, `neutral` or `skipped` | passed |
| Check run `completed` with `failure`, `cancelled`, `timed_out`, `action_required`, `stale` or `startup_failure` | failed |
| Any other check-run status (`queued`, `in_progress`, `waiting`, `requested`, `pending`) | pending |
| Status `success` | passed |
| Status `failure` or `error` | failed |
| Status `pending` | pending |

The overall state is the first rule that applies:

1. **`failure`** if any item failed. A failure seen in a readable source is definite.
2. **`unknown`** if either source is unreadable (403 or 404, meaning missing permission) or truncated (`total_count` is larger than what was listed, or the page cap was reached). Missing permission never produces `success`.
3. **`pending`** if any item is pending.
4. **`none`** if there are no items.
5. **`success`** otherwise.

If both sources return 404, `Checks` returns `forge.ErrNotFound`. A rate limit or any other error aborts the call instead of degrading to `unknown`.

GitHub limits check runs to the 1000 most recent check suites on a ref. The count covers what GitHub returns.

If access is lost on a later page, counts already observed are retained. A
known failure stays a failure; a readable prefix of passing checks cannot
turn an unreadable remainder into success.

## Merge status

`MergeStatus` reads `merged`, `state`, `draft`, `mergeable` and `mergeable_state` from the PR. `Reasons` is never nil.

| Condition | `Mergeable` | Reason |
|---|---|---|
| Merged | `blocked`, with `Merged: true` | "already merged" |
| Closed | `blocked` | "closed" |
| Draft, or `mergeable_state: draft` | `blocked` | "draft" |
| `mergeable: null` after the bounded polls | `unknown` | GitHub is still computing. The connector returns `unknown` rather than guessing. |
| `clean` | `clean` | none |
| `has_hooks` | `clean` | Pre-receive hooks will run |
| `unstable` | `unstable` | Some statuses or checks are not passing |
| `behind` | `behind` | Head branch is out of date |
| `dirty` | `dirty` | Merge conflicts |
| `blocked` | `blocked` | GitHub reports that merging is blocked. The specific rule is not visible to this credential. |
| `mergeable: false` with an unknown or unrecognized state | `dirty` | GitHub could not create a test merge commit |
| Anything else | `unknown` | The unrecognized state is quoted |

The connector does not read the branch-protection or ruleset APIs, which need admin access. It never names a protection rule it has not read. Local merge policy and authorization belong to the caller: an approval does not grant permission to merge.

## Webhooks

`VerifyWebhook(secret, headers, body)` processes a delivery in this order:

1. Refuses an empty secret.
2. Requires `X-Hub-Signature-256: sha256=<hex>`. The legacy SHA-1 header is ignored.
3. Checks the HMAC-SHA256 of the raw body with `hmac.Equal`, a constant-time compare, before reading anything else.

A missing, malformed or mismatched signature returns `ErrWebhookSignature`.

After the signature passes, the delivery also needs:

- `X-GitHub-Delivery`, which becomes `DeliveryID`, the deduplication key to store in `forge_deliveries`. A replayed delivery reuses its ID.
- `X-GitHub-Event`.

If either header is missing, or the JSON cannot be parsed, the result is `ErrWebhookMalformed`. Header names are matched case-insensitively.

| Event | Fields set |
|---|---|
| `pull_request`, `pull_request_review`, `pull_request_review_comment`, `pull_request_review_thread` | `Action`, `Repo`, `PRNumber`, `HeadSHA` (`pull_request.head.sha`) |
| `check_run`, `check_suite` | `Action`, `Repo`, `HeadSHA`. `PRNumber` only when the payload links exactly one PR whose base is this repository. GitHub does not link fork PRs, so resolve those by `HeadSHA`. |
| `status` | `Repo`, `HeadSHA` (`sha`), and `Action` set to the status `state`, because this event has no `action` |
| Anything else (e.g. `ping`) | `DeliveryID`, `Event`, `Action`, `Repo`; no error |

A webhook is a hint to re-synchronize, not a source of truth. After a delivery, re-read the PR, checks and reviews through the API. When webhooks are unavailable, use bounded polling instead.

## Rate limits

A 429, or a 403 that has `X-RateLimit-Remaining: 0`, a `Retry-After` header, or a "rate limit" message, returns `*RateLimitError`:

- `ResetAt` comes from `Retry-After` (seconds or an HTTP date) first, then `X-RateLimit-Reset`. Without either, it is one minute from now, per GitHub's guidance for secondary limits.
- `Secondary` is true for secondary limits.
- The error also carries `Limit`, `Remaining` and `Resource`.

A rate limit never matches `forge.ErrForbidden`. The connector never sleeps or retries on a rate limit. The only waiting it does is the `MergeablePolls` loop, which is bounded and respects the context.

## Error reference

- `forge.ErrNotFound`, `forge.ErrForbidden`, `forge.ErrStale`, `forge.ErrIneligible` and `forge.ErrAmbiguous` are wrapped as described above.
- `*github.APIError` gives the status code, method, path, GitHub's message and error details, and `X-GitHub-Request-Id`.
- `*github.RateLimitError` matches `github.ErrRateLimited`.
- `github.ErrBadCredentials` means a 401.
- `github.ErrTruncated` accompanies a partial result from `Files` or `Reviews`, or an incomplete marker scan.
- `github.ErrInvalidURL` comes from `ParseURL`.
- `github.ErrWebhookSignature` and `github.ErrWebhookMalformed` come from `VerifyWebhook`.

## Limitations

- **GitHub App installation tokens:** `GET /user` returns 403 for them, so `Viewer`, `PublishReview` and `FindReviewByMarker` fail. Use a user-scoped credential: a fine-grained or classic personal access token, an OAuth token, or a GitHub App *user* access token. Supporting app bots would need a configured actor login (`{slug}[bot]`).
- **Renamed files:** `FileDiff` has no field for the previous path, so GitHub's `previous_filename` is dropped for renames.
- **Review threads:** review threads, dismissals and review-request APIs are not implemented yet.

## Tests

`go test ./internal/forge/github/` runs against an `httptest.Server` that emulates GitHub's JSON shapes, `Link` pagination, rate-limit headers and error bodies. It never contacts GitHub. The tests cover:

- URL parsing for github.com and GHES.
- PR fetch, including open, merged and closed states.
- Pagination of files and reviews, page caps and truncation, and refusal of foreign-origin links.
- Checks aggregation: mixed results, pending, success, none, permission denied (unknown), a known failure despite an unreadable source, and incomplete `total_count`.
- Merge-state mapping, including a `null` `mergeable` that stays unknown and one that resolves.
- Rate limits: primary, secondary with `Retry-After`, and secondary without headers. Each case checks that no retry happens.
- Error mapping for 401 and credential-source failures.
- Publishing:
  - A stale head, or a closed or merged PR, returns `ErrStale` with no POST.
  - The author approving or requesting changes returns `ErrIneligible` with no POST; the author commenting is allowed.
  - GitHub's 422 self-review refusal returns `ErrIneligible`.
  - 403 and 404 map to `ErrForbidden` and `ErrNotFound`.
  - A timeout after the request is sent returns `ErrAmbiguous`; `FindReviewByMarker` then finds the review, and a retry makes no second POST.
  - 5xx and a dropped connection return `ErrAmbiguous`.
  - A dial failure is not ambiguous.
  - A copied marker in another actor's review is ignored.
  - Invalid requests fail before any API call.
- Webhooks:
  - GitHub's documented HMAC test vector.
  - Wrong secret, tampered body, missing, unprefixed, non-hex or truncated signature, and empty secret.
  - Missing delivery or event header.
  - Case-insensitive header names.
  - `pull_request`, `pull_request_review`, `check_run`, `check_suite` (both one and several linked PRs) and `status` payloads.

### Contract test

`TestContractGitHub` in `contract_test.go` runs against a real repository. It is skipped unless both `YIP_GITHUB_CONTRACT_REPO=owner/name` and `YIP_GITHUB_TOKEN` are set. Optional variables:

| Variable | Effect |
|---|---|
| `YIP_GITHUB_HOST` | GHES host |
| `YIP_GITHUB_CONTRACT_PR` | PR number to use. Default: the most recently updated open PR. |
| `YIP_GITHUB_CONTRACT_WRITE=1` | Also exercises publishing |

The default run is read-only. It covers:

- `Viewer`, `GetPR` field completeness, and a `ParseURL` round trip of the canonical URL.
- `Files`, `Checks` (valid state and consistent counts), `Reviews` and `MergeStatus` (valid enum).
- `FindReviewByMarker` with a fresh marker, which must not be found.
- `PublishReview` against a wrong commit, which must return `ErrStale` before any POST.

With `YIP_GITHUB_CONTRACT_WRITE=1`, it also:

- Checks that `APPROVE` returns `ErrIneligible` without posting, when the token belongs to the PR author.
- Publishes one `COMMENT` review with a random marker. The review stays visible on the PR.
- Confirms that `FindReviewByMarker` returns that review's ID.
- Confirms that publishing again with the same marker returns the same ID and creates no duplicate.

On 28 September 2026, the write-enabled contract test passed against the
dedicated public playground under all three authorized accounts. A separate
32-case real GitHub campaign covered private access, fork contributors,
protected branches, changing checks, stale reviews, drafts, terminal PRs and
conflicts. Three full hub/runner/bridge scenarios verified publication grants,
self-review refusal and permitted publication. See the
[campaign record](../simulations/2026-09-28.md) for fixtures and limitations.
