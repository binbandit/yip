package hub

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/forge"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

// connectorFor returns the forge connector for a repository.
func (h *Hub) connectorFor(ctx context.Context, repo protocol.Repo) (forge.Connector, forge.RepoRef, error) {
	if repo.Forge != "github" {
		return nil, forge.RepoRef{}, domain.Unavailable("forge", "%s isn't connected to a forge.", repo.Name)
	}
	if h.cfg.ForgeFactory == nil {
		return nil, forge.RepoRef{}, domain.Unavailable("forge", "No forge credential is configured on this hub. Add one with `yip forge github add`.")
	}
	return h.cfg.ForgeFactory(ctx, h, repo)
}

// ForgeToken returns the sealed credential for a forge host (used by the
// connector factory). It never leaves the hub.
func (h *Hub) ForgeToken(ctx context.Context, kind, host string) (string, error) {
	_, sealed, err := store.GetCredential(ctx, h.st.R(), kind, host)
	if err != nil {
		return "", domain.Unavailable("forge_credential", "No %s credential for %s. Add one with `yip forge github add`.", kind, host)
	}
	plain, err := h.sealer.Open(sealed)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// PutForgeToken stores a forge credential sealed with the hub key.
func (h *Hub) PutForgeToken(ctx context.Context, kind, host, label, token string) error {
	sealed, err := h.sealer.Seal([]byte(strings.TrimSpace(token)))
	if err != nil {
		return err
	}
	return h.do(ctx, func(t *txn) error {
		if err := store.PutCredential(ctx, t.tx, domain.NewID(), h.Org().ID, kind, label, host, sealed); err != nil {
			return err
		}
		return t.audit(protocol.Actor{Kind: protocol.ActorSystem, ID: "host"}, "host_access", "credential.add", kind+"@"+host, "ok", label)
	})
}

// syncPR reads the forge's current view of a PR into the local record,
// keeping remote reviews, checks, and merge state as separate facts.
func (h *Hub) syncPR(ctx context.Context, repo protocol.Repo, number int, jobID string) (protocol.PullRequest, error) {
	c, ref, err := h.connectorFor(ctx, repo)
	if err != nil {
		return protocol.PullRequest{}, err
	}
	pr, err := c.GetPR(ctx, ref, number)
	if err != nil {
		return protocol.PullRequest{}, forgeErr(err)
	}
	out := protocol.PullRequest{RepoID: repo.ID, JobID: jobID, Forge: c.Name(), Host: ref.Host, Owner: ref.Owner, Name: ref.Name, Number: number,
		URL: pr.URL, Title: pr.Title, State: pr.State, Base: pr.Base, Head: pr.Head, RemoteAuthor: pr.Author}
	if v, err := c.Viewer(ctx); err == nil {
		out.ViewerActor = v.Login
	}
	if cs, err := c.Checks(ctx, ref, pr.Head); err == nil {
		out.Checks = cs
	} else {
		out.Checks = protocol.ChecksSummary{State: "unknown"}
	}
	if ms, err := c.MergeStatus(ctx, ref, number); err == nil {
		out.Merge = ms
	} else {
		out.Merge = protocol.MergeStatus{Mergeable: "unknown", Reasons: []string{"Could not read merge status: " + err.Error()}}
	}
	if rs, err := c.Reviews(ctx, ref, number); err == nil {
		out.RemoteReviews = rs
	}
	now := h.now()
	out.LastSyncedAt = &now
	return out, nil
}

func forgeErr(err error) error {
	switch {
	case errors.Is(err, forge.ErrNotFound):
		return domain.NotFound("The forge says that pull request doesn't exist or isn't visible to the configured credential.")
	case errors.Is(err, forge.ErrForbidden):
		return domain.Forbidden("The forge credential isn't allowed to do that.")
	case errors.Is(err, forge.ErrStale):
		return domain.Conflict("The pull request moved since it was reviewed.")
	case errors.Is(err, forge.ErrIneligible):
		return domain.Forbidden("The forge credential's account isn't eligible for that review (for example, a PR author can't approve their own PR).")
	case errors.Is(err, forge.ErrAmbiguous):
		return domain.Unavailable("forge", "The forge didn't confirm the outcome. It will be reconciled before any retry.")
	}
	return err
}

// LinkPullRequest validates a PR and links it to a job.
func (h *Hub) LinkPullRequest(ctx context.Context, userID string, req protocol.LinkPullRequestRequest) (protocol.PullRequest, error) {
	job, err := store.GetJob(ctx, h.st.R(), req.JobID)
	if err != nil {
		return protocol.PullRequest{}, domain.NotFound("That work doesn't exist.")
	}
	if _, err := h.requireRoom(ctx, h.st.R(), userID, job.Source.RoomID); err != nil {
		return protocol.PullRequest{}, err
	}
	return h.linkPR(ctx, job, req.RepoID, req.Number, req.URL)
}

func (h *Hub) linkPR(ctx context.Context, job store.JobRow, repoID string, number int, url string) (protocol.PullRequest, error) {
	if repoID == "" {
		repoID = job.RepoID
	}
	repo, err := store.GetRepo(ctx, h.st.R(), repoID)
	if err != nil {
		return protocol.PullRequest{}, domain.Invalid("Choose the job's repository.")
	}
	if url != "" && number == 0 {
		c, _, err := h.connectorFor(ctx, repo)
		if err != nil {
			return protocol.PullRequest{}, err
		}
		ref, n, err := c.ParseURL(url)
		if err != nil {
			return protocol.PullRequest{}, domain.Invalid("That isn't a pull request URL for this forge.")
		}
		if !strings.EqualFold(ref.Owner+"/"+ref.Name, repo.ForgeRepo) {
			return protocol.PullRequest{}, domain.Invalid("That pull request belongs to %s/%s, not %s.", ref.Owner, ref.Name, repo.ForgeRepo)
		}
		number = n
	}
	if number <= 0 {
		return protocol.PullRequest{}, domain.Invalid("Give the pull request number or URL.")
	}
	pr, err := h.syncPR(ctx, repo, number, job.ID)
	if err != nil {
		return protocol.PullRequest{}, err
	}
	err = h.do(ctx, func(t *txn) error {
		id, err := store.UpsertPR(ctx, t.tx, h.Org().ID, pr)
		if err != nil {
			return err
		}
		pr.ID = id
		if pr, err = store.GetPR(ctx, t.tx, id); err != nil {
			return err
		}
		return t.emit(ev{Type: "pr.updated", Room: job.Source.RoomID, Job: job.ID, Payload: pr})
	})
	return pr, err
}

// refreshPR re-reads a PR from the forge (used for freshness checks).
func (h *Hub) refreshPR(ctx context.Context, t *txn, prID string) (protocol.PullRequest, error) {
	pr, err := store.GetPR(ctx, t.tx, prID)
	if err != nil {
		return pr, err
	}
	repo, err := store.GetRepo(ctx, t.tx, pr.RepoID)
	if err != nil {
		return pr, err
	}
	fresh, err := h.syncPR(ctx, repo, pr.Number, pr.JobID)
	if err != nil {
		return pr, err
	}
	fresh.ID = pr.ID
	if _, err := store.UpsertPR(ctx, t.tx, h.Org().ID, fresh); err != nil {
		return pr, err
	}
	return fresh, nil
}

// GetPullRequest returns a PR with its current synchronised facts.
func (h *Hub) GetPullRequest(ctx context.Context, userID, id string, refresh bool) (protocol.PullRequest, error) {
	pr, err := store.GetPR(ctx, h.st.R(), id)
	if err != nil {
		return pr, domain.NotFound("That pull request isn't linked.")
	}
	if pr.JobID != "" {
		job, err := store.GetJob(ctx, h.st.R(), pr.JobID)
		if err == nil {
			if _, err := h.requireRoom(ctx, h.st.R(), userID, job.Source.RoomID); err != nil {
				return pr, err
			}
		}
	}
	if refresh {
		_ = h.do(ctx, func(t *txn) error {
			fresh, err := h.refreshPR(ctx, t, id)
			if err == nil {
				pr = fresh
				return t.emit(ev{Type: "pr.updated", Payload: fresh})
			}
			return nil
		})
	}
	return pr, nil
}

func (h *Hub) toolForgeReadPR(ctx context.Context, run store.RunRow, a bridge.ForgeReadPRArgs) (any, error) {
	job, err := store.GetJob(ctx, h.st.R(), run.JobID)
	if err != nil {
		return nil, err
	}
	repoID := job.RepoID
	if repoID == "" {
		return nil, domain.Invalid("This job has no repository.")
	}
	repo, err := store.GetRepo(ctx, h.st.R(), repoID)
	if err != nil {
		return nil, err
	}
	c, ref, err := h.connectorFor(ctx, repo)
	if err != nil {
		return nil, err
	}
	n := a.Number
	if a.URL != "" {
		r2, num, err := c.ParseURL(a.URL)
		if err != nil || !strings.EqualFold(r2.Owner+"/"+r2.Name, repo.ForgeRepo) {
			return nil, domain.Invalid("That PR isn't in this job's repository.")
		}
		n = num
	}
	pr, err := h.syncPR(ctx, repo, n, "")
	if err != nil {
		return nil, err
	}
	out := map[string]any{"pullRequest": pr, "note": "Remote reviews, checks, and merge state are the forge's facts, separate from yip's internal reviews."}
	if a.IncludeDiff {
		if files, err := c.Files(ctx, ref, n); err == nil {
			var diff []map[string]any
			total := 0
			for _, f := range files {
				patch := f.Patch
				if total+len(patch) > 120000 {
					patch = "(omitted for length)"
				}
				total += len(patch)
				diff = append(diff, map[string]any{"path": f.Path, "status": f.Status, "additions": f.Additions, "deletions": f.Deletions, "patch": patch})
			}
			out["files"] = diff
		}
	}
	return out, nil
}

func (h *Hub) toolForgeLinkPR(ctx context.Context, env toolEnv, a bridge.ForgeReadPRArgs) (any, error) {
	if env.job.OwnerID != env.eng.ID {
		return nil, domain.Forbidden("Only the job owner links its pull request.")
	}
	// The forge is read outside any transaction; the link itself is an
	// idempotent upsert committed afterwards.
	pr, err := h.linkPR(ctx, env.job, env.job.RepoID, a.Number, a.URL)
	if err != nil {
		return nil, err
	}
	return map[string]any{"pullRequest": pr}, nil
}

// toolForgePublishReview publishes a recorded internal verdict to the linked
// PR through the project's authorized credential. It is refused unless the
// reviewer holds the publish_review grant; the forge connector re-verifies
// the head revision and the remote actor's eligibility. Engineer attribution
// and the remote actor are both recorded; several engineers sharing one
// credential remain one remote actor.
func (h *Hub) toolForgePublishReview(ctx context.Context, env toolEnv, a bridge.ForgePublishReviewArgs) (any, error) {
	q := h.st.R()
	review, round, err := store.ReviewByReviewJob(ctx, q, env.job.ID)
	if err != nil || round.Target.Kind != "pr" {
		return nil, domain.Invalid("This review isn't of a pull request.")
	}
	if !domain.ReviewVerdict(round.State) {
		return nil, domain.Invalid("Record your verdict with work_review first.")
	}
	g, err := store.GetGrant(ctx, q, env.job.ProjectID, env.eng.ID)
	if err != nil || !contains(g.Actions, "publish_review") {
		return nil, domain.Forbidden("External publication isn't granted for this project. The internal review is recorded; ask the owner if it should be published.")
	}
	pr, err := store.GetPR(ctx, q, round.Target.PullRequestID)
	if err != nil {
		return nil, err
	}
	repo, err := store.GetRepo(ctx, q, pr.RepoID)
	if err != nil {
		return nil, err
	}
	// Private-room findings can't be published into a broader PR.
	if room, err := store.GetRoom(ctx, q, review.Source.RoomID); err == nil && room.Private {
		return nil, domain.Forbidden("This review happened in a private room; publishing it to the pull request would widen its audience.")
	}
	c, ref, err := h.connectorFor(ctx, repo)
	if err != nil {
		return nil, err
	}
	marker, dedupe := round.ID, "publish:"+round.ID
	if d, err := store.GetForgeDelivery(ctx, q, dedupe); err == nil {
		switch d.Status {
		case "published":
			return map[string]any{"externalId": d.ExternalID, "duplicate": true}, nil
		case "unknown", "pending":
			// Reconcile an ambiguous earlier attempt before any retry.
			id, found, ferr := c.FindReviewByMarker(ctx, ref, pr.Number, marker)
			if ferr != nil {
				return nil, domain.Unavailable("forge", "The earlier publication's outcome is still unknown; not retrying until it can be reconciled.")
			}
			if found {
				_ = h.do(ctx, func(t *txn) error { return store.SetForgeDelivery(ctx, t.tx, d.ID, "published", id, "") })
				return map[string]any{"externalId": id, "reconciled": true}, nil
			}
			_ = h.do(ctx, func(t *txn) error {
				return store.SetForgeDelivery(ctx, t.tx, d.ID, "failed", "", "reconciled: not published")
			})
		case "failed":
		}
	}
	event := forge.EventComment
	switch round.State {
	case protocol.ReviewApproved:
		event = forge.EventApprove
	case protocol.ReviewChangesRequested:
		event = forge.EventRequestChanges
	}
	body := strings.TrimSpace(a.Body)
	if body == "" {
		body = round.Summary
	}
	body += fmt.Sprintf("\n\n— %s (yip engineer), reviewing %s", env.eng.Name, shortRev(round.Target.Head))
	var comments []forge.ReviewComment
	for _, f := range round.Findings {
		if f.File != "" && f.Line > 0 {
			comments = append(comments, forge.ReviewComment{Path: f.File, Line: f.Line, Body: "[" + f.Severity + "] " + f.Body})
		}
	}
	// Record the attempt durably before the network call, so a crash leaves
	// an ambiguous delivery to reconcile rather than a silent retry.
	deliveryID := domain.NewID()
	if err := h.do(ctx, func(t *txn) error {
		_, err := store.InsertForgeDelivery(ctx, t.tx, store.ForgeDelivery{ID: deliveryID, PRID: pr.ID, Kind: "publish_review",
			DedupeKey: dedupe + ":" + deliveryID, Status: "pending", EngineerID: env.eng.ID, ReviewRoundID: round.ID})
		if err != nil {
			return err
		}
		_, err = t.tx.ExecContext(ctx, `UPDATE forge_deliveries SET dedupe_key = ? WHERE id = ? AND NOT EXISTS (SELECT 1 FROM forge_deliveries WHERE dedupe_key = ?)`,
			dedupe, deliveryID, dedupe)
		return err
	}); err != nil {
		return nil, err
	}
	id, perr := c.PublishReview(ctx, ref, pr.Number, forge.PublishReview{CommitID: round.Target.Head, Event: event, Body: body, Comments: comments, Marker: marker})
	status, errText := "published", ""
	switch {
	case perr == nil:
	case errors.Is(perr, forge.ErrAmbiguous):
		status, errText = "unknown", perr.Error()
	default:
		status, errText = "failed", perr.Error()
	}
	_ = h.do(ctx, func(t *txn) error {
		if err := store.SetForgeDelivery(ctx, t.tx, deliveryID, status, id, errText); err != nil {
			return err
		}
		result := "ok"
		if perr != nil {
			result = status
		}
		return t.audit(env.me, "grant:publish_review", "forge.publish_review", pr.URL, result, errText)
	})
	if perr != nil {
		return nil, forgeErr(perr)
	}
	return map[string]any{"externalId": id, "remoteActor": pr.ViewerActor, "event": event,
		"note": "Published as the forge credential's account; the forge decides whether it counts toward merge requirements."}, nil
}

// ---- forge events ----

// HandleForgeWebhook verifies a signed forge delivery, drops replays by
// delivery ID, and re-synchronises the affected pull request.
func (h *Hub) HandleForgeWebhook(ctx context.Context, forgeName string, headers map[string]string, body []byte) error {
	if h.cfg.WebhookVerifier == nil {
		return domain.Unavailable("forge_webhook", "Webhooks aren't configured on this hub.")
	}
	_, sealed, err := store.GetCredential(ctx, h.st.R(), forgeName+"-webhook", "github.com")
	if err != nil {
		return domain.Unavailable("forge_webhook", "No webhook secret is configured. Run `yip forge github webhook-secret`.")
	}
	secret, err := h.sealer.Open(sealed)
	if err != nil {
		return err
	}
	wh, err := h.cfg.WebhookVerifier(secret, headers, body)
	if err != nil {
		return domain.Forbidden("Webhook signature check failed.")
	}
	fresh := false
	if err := h.do(ctx, func(t *txn) error {
		var err error
		fresh, err = store.InsertForgeDelivery(ctx, t.tx, store.ForgeDelivery{ID: domain.NewID(), Kind: "webhook",
			DedupeKey: "webhook:" + forgeName + ":" + wh.DeliveryID, Status: "received", Payload: fmt.Sprintf(`{"event":%q,"action":%q}`, wh.Event, wh.Action)})
		return err
	}); err != nil || !fresh {
		return err // replayed delivery: nothing to do
	}
	if wh.PRNumber == 0 {
		return nil
	}
	prs, err := store.ListPRs(ctx, h.st.R(), "owner = ? AND name = ? AND number = ?", wh.Repo.Owner, wh.Repo.Name, wh.PRNumber)
	if err != nil {
		return err
	}
	for _, pr := range prs {
		if err := h.syncLinkedPR(ctx, pr); err != nil {
			h.log.Warn("PR sync after webhook failed", "pr", pr.URL, "err", err)
		}
	}
	return nil
}

// syncLinkedPR refreshes a linked PR. When its head moved, reviews of the
// old revision stop applying and one new round is scheduled per reviewer.
func (h *Hub) syncLinkedPR(ctx context.Context, pr protocol.PullRequest) error {
	repo, err := store.GetRepo(ctx, h.st.R(), pr.RepoID)
	if err != nil {
		return err
	}
	fresh, err := h.syncPR(ctx, repo, pr.Number, pr.JobID)
	if err != nil {
		return err
	}
	fresh.ID = pr.ID
	return h.do(ctx, func(t *txn) error {
		if _, err := store.UpsertPR(ctx, t.tx, h.Org().ID, fresh); err != nil {
			return err
		}
		cur, _ := store.GetPR(ctx, t.tx, pr.ID)
		if err := t.emit(ev{Type: "pr.updated", Job: pr.JobID, Payload: cur}); err != nil {
			return err
		}
		if pr.Head == "" || fresh.Head == pr.Head {
			return nil
		}
		return h.onPRHeadChanged(ctx, t, cur, pr.Head)
	})
}

func (h *Hub) onPRHeadChanged(ctx context.Context, t *txn, pr protocol.PullRequest, oldHead string) error {
	if pr.JobID == "" {
		return nil
	}
	job, err := store.GetJob(ctx, t.tx, pr.JobID)
	if err != nil || !domain.JobLive(job.State) {
		return err
	}
	reviews, err := store.ListJobReviews(ctx, t.tx, job.ID)
	if err != nil {
		return err
	}
	author, _ := store.GetEngineer(ctx, t.tx, job.OwnerID)
	actor := protocol.Actor{Kind: protocol.ActorSystem, ID: "forge"}
	var names []string
	for _, rv := range reviews {
		if len(rv.Rounds) == 0 {
			continue
		}
		last := rv.Rounds[len(rv.Rounds)-1]
		if last.Target.PullRequestID != pr.ID || last.Target.Head != oldHead {
			continue
		}
		if domain.ReviewOpen(last.State) {
			_ = store.SetRoundState(ctx, t.tx, last.ID, protocol.ReviewCancelled, "the pull request moved to "+shortRev(pr.Head), true)
			if last.ReviewJobID != "" {
				if err := h.cancelOne(ctx, t, last.ReviewJobID, "the pull request moved", actor); err != nil {
					return err
				}
			}
		}
		if last.Number >= h.lim.MaxReviewRounds {
			continue
		}
		reviewer, err := store.GetEngineer(ctx, t.tx, rv.ReviewerID)
		if err != nil {
			continue
		}
		target := protocol.ReviewTarget{Kind: "pr", RepoID: pr.RepoID, Base: pr.Base, Head: pr.Head, PullRequestID: pr.ID}
		causeKey := job.ID + ":" + reviewer.ID + ":" + pr.Head
		if _, err := store.RoundByCauseKey(ctx, t.tx, causeKey); err == nil {
			continue // already scheduled once for this head
		}
		round, _, err := h.openRoundFor(ctx, t, job, author.Name, actor, pr.ID, false, rv, reviewer, last.Number+1, target, causeKey, rv.Criteria)
		if err != nil {
			return err
		}
		_ = store.SupersedeRound(ctx, t.tx, last.ID, round.ID)
		names = append(names, reviewer.Name)
	}
	body := fmt.Sprintf("PR #%d moved from %s to %s. Reviews of the earlier revision stay attached to it and no longer count for the new head.", pr.Number, shortRev(oldHead), shortRev(pr.Head))
	if len(names) > 0 {
		body += " New review round requested from " + strings.Join(names, ", ") + "."
	}
	_, err = t.postMessage(newMessage{Room: job.Source.RoomID, Thread: job.Source.ThreadID, Author: systemActor, Kind: protocol.MessageStatus,
		Body: body, Refs: []protocol.Ref{{Kind: "pr", ID: pr.ID}, {Kind: "job", ID: job.ID}}, JobID: job.ID})
	return err
}

// pollPRs re-synchronises open PRs linked to live work, a bounded fallback
// for hubs that can't receive webhooks.
func (h *Hub) pollPRs(ctx context.Context) {
	if h.cfg.ForgeFactory == nil {
		return
	}
	prs, err := store.ListPRs(ctx, h.st.R(), `state = 'open' AND job_id IN (SELECT id FROM jobs WHERE state IN ('queued','running','waiting','review_ready'))`)
	if err != nil {
		return
	}
	for i, pr := range prs {
		if i >= 20 {
			break
		}
		if pr.LastSyncedAt != nil && h.now().Sub(*pr.LastSyncedAt) < 5*time.Minute {
			continue
		}
		if err := h.syncLinkedPR(ctx, pr); err != nil {
			h.log.Debug("PR poll failed", "pr", pr.URL, "err", err)
		}
	}
}

// SetWebhookSecret stores a new webhook secret sealed with the hub key.
func (h *Hub) SetWebhookSecret(ctx context.Context, forgeName, secret string) error {
	return h.PutForgeToken(ctx, forgeName+"-webhook", "github.com", "webhook secret", secret)
}
