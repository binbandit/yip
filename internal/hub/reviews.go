package hub

import (
	"context"
	"fmt"
	"strings"

	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

// GetReview returns a review with rounds and findings.
func (h *Hub) GetReview(ctx context.Context, userID, id string) (protocol.Review, error) {
	r, err := store.GetReview(ctx, h.st.R(), id)
	if err != nil {
		return r, domain.NotFound("That review doesn't exist.")
	}
	if _, err := h.requireRoom(ctx, h.st.R(), userID, r.Source.RoomID); err != nil {
		return r, domain.NotFound("That review doesn't exist.")
	}
	return r, nil
}

// reviewTarget resolves what an author is asking to have reviewed.
func (h *Hub) reviewTarget(ctx context.Context, q store.Q, job store.JobRow, prNumber int) (protocol.ReviewTarget, error) {
	if prNumber > 0 {
		prs, _ := store.ListPRs(ctx, q, "job_id = ? AND number = ?", job.ID, prNumber)
		if len(prs) == 0 {
			return protocol.ReviewTarget{}, domain.Invalid("PR #%d isn't linked to this job. Link it with forge_link_pr first.", prNumber)
		}
		pr := prs[0]
		return protocol.ReviewTarget{Kind: "pr", RepoID: pr.RepoID, Base: pr.Base, Head: pr.Head, PullRequestID: pr.ID}, nil
	}
	if job.Revision != nil && job.Revision.Head != "" {
		return protocol.ReviewTarget{Kind: "patch", RepoID: job.RepoID, Base: job.Revision.Base, Head: job.Revision.Head}, nil
	}
	if job.Kind == protocol.JobKindCode {
		return protocol.ReviewTarget{}, domain.Invalid("Publish your revision with work_publish_revision before requesting review, so the reviewer sees an immutable result.")
	}
	arts, _ := store.ListJobArtifacts(ctx, q, job.ID)
	for i := len(arts) - 1; i >= 0; i-- {
		if arts[i].Kind == "document" || arts[i].Kind == "file" {
			return protocol.ReviewTarget{Kind: "artifact", ArtifactID: arts[i].ID, Hash: arts[i].Hash}, nil
		}
	}
	return protocol.ReviewTarget{}, domain.Invalid("Publish the document or artifact to review first (artifact_publish).")
}

// Both initial review and re-review retain the original room and project boundary.
func (h *Hub) requireReviewerAccess(ctx context.Context, q store.Q, job store.JobRow, reviewer protocol.Engineer) error {
	if ok, _ := store.IsMember(ctx, q, job.Source.RoomID, protocol.ActorEngineer, reviewer.ID); !ok {
		room, _ := store.GetRoom(ctx, q, job.Source.RoomID)
		return domain.Forbidden("%s needs membership in #%s to review here. Explain the missing access in the room and choose a permitted colleague or ask the owner to invite them.", reviewer.Name, room.Name)
	}
	if job.ProjectID != "" {
		if grant, err := store.GetGrant(ctx, q, job.ProjectID, reviewer.ID); err != nil || grant.Access == "none" {
			project, _ := store.GetProject(ctx, q, job.ProjectID)
			return domain.Forbidden("%s needs read access to %s to review here. Explain the missing access in the room and choose a permitted colleague or ask the owner to grant it.", reviewer.Name, project.Name)
		}
	}
	return nil
}

func targetKey(t protocol.ReviewTarget) string {
	if t.Head != "" {
		return t.Head
	}
	return t.Hash
}

func (h *Hub) toolRequestReview(ctx context.Context, t *txn, env toolEnv, a bridge.WorkRequestReviewArgs) (any, error) {
	job := env.job
	if job.OwnerID != env.eng.ID || job.Kind == protocol.JobKindReply || job.Kind == protocol.JobKindReview {
		return nil, domain.Forbidden("Only the owner of a piece of work can request its review.")
	}
	reviewer, err := h.resolveEngineer(ctx, t.tx, a.Reviewer)
	if err != nil {
		return nil, err
	}
	if reviewer.ID == env.eng.ID {
		return nil, domain.Invalid("Choose a distinct colleague; you can't review your own work.")
	}
	if err := h.requireReviewerAccess(ctx, t.tx, job, reviewer); err != nil {
		return nil, err
	}
	target, err := h.reviewTarget(ctx, t.tx, job, a.PullRequest)
	if err != nil {
		return nil, err
	}
	causeKey := job.ID + ":" + reviewer.ID + ":" + targetKey(target)
	// One logical request: a replayed or duplicated request maps to the same round.
	if existing, err := store.RoundByCauseKey(ctx, t.tx, causeKey); err == nil {
		return map[string]any{"reviewId": existing.ReviewID, "round": existing.Number, "state": existing.State, "duplicate": true}, nil
	}
	review, err := store.GetReviewForJob(ctx, t.tx, job.ID, reviewer.ID)
	number := 1
	if err == nil {
		last := review.Rounds[len(review.Rounds)-1]
		if domain.ReviewOpen(last.State) {
			// A new revision supersedes the open round's applicability.
			if err := store.SetRoundState(ctx, t.tx, last.ID, protocol.ReviewCancelled, "superseded by a newer revision", true); err != nil {
				return nil, err
			}
			if last.ReviewJobID != "" {
				if err := h.cancelOne(ctx, t, last.ReviewJobID, "superseded by a newer revision", env.me); err != nil {
					return nil, err
				}
			}
		}
		number = last.Number + 1
		if number > h.lim.MaxReviewRounds {
			return nil, domain.Limit("This review has used its %d rounds. Summarize the remaining disagreement in the conversation; another qualified colleague can arbitrate, or ask the owner if a real product decision is needed.", h.lim.MaxReviewRounds)
		}
	} else {
		review = protocol.Review{ID: domain.NewID(), JobID: job.ID, AuthorID: env.eng.ID, ReviewerID: reviewer.ID, Source: job.Source,
			TargetKind: target.Kind, Criteria: strings.TrimSpace(a.Criteria), State: protocol.ReviewRequested, CurrentRound: 1,
			PullRequestID: target.PullRequestID, CreatedAt: h.now(), UpdatedAt: h.now()}
		if err := store.InsertReview(ctx, t.tx, h.Org().ID, review); err != nil {
			return nil, err
		}
		if err := store.AddAssignment(ctx, t.tx, job.ID, reviewer.ID, "reviewer"); err != nil {
			return nil, err
		}
	}
	round, reviewJob, err := h.openRound(ctx, t, env, review, reviewer, number, target, causeKey, a.Criteria)
	if err != nil {
		return nil, err
	}
	msg := strings.TrimSpace(a.Message)
	if !strings.Contains(strings.ToLower(msg), "@"+reviewer.Handle) {
		msg = "@" + reviewer.Handle + " " + msg
	}
	if _, err := t.postMessage(newMessage{Room: job.Source.RoomID, Thread: job.Source.ThreadID, Author: env.me, Kind: protocol.MessageReview,
		Body: msg, Mentions: []protocol.Mention{{Kind: protocol.ActorEngineer, ID: reviewer.ID}},
		Refs: []protocol.Ref{{Kind: "review", ID: review.ID}, {Kind: "job", ID: job.ID}}, JobID: job.ID, RunID: env.run.ID, Root: job.RootRequestID}); err != nil {
		return nil, err
	}
	return map[string]any{"reviewId": review.ID, "round": round.Number, "reviewer": reviewer.Name, "target": target, "reviewJobId": reviewJob.ID,
		"note": "Review requested. If you have nothing else useful to do, call work_update {state:'completed'} (it completes once approved) or work_wait {reason:'review'}, then end your turn."}, nil
}

// openRound creates a review round, its reviewer job, and the reviewer's run.
func (h *Hub) openRound(ctx context.Context, t *txn, env toolEnv, review protocol.Review, reviewer protocol.Engineer, number int,
	target protocol.ReviewTarget, causeKey, criteria string) (protocol.ReviewRound, store.JobRow, error) {
	return h.openRoundFor(ctx, t, env.job, env.eng.Name, env.me, env.run.ID, true, review, reviewer, number, target, causeKey, criteria)
}

// openRoundFor opens a round on behalf of an author job; cause identifies
// what triggered it (an author's run, or a forge event).
func (h *Hub) openRoundFor(ctx context.Context, t *txn, author store.JobRow, authorName string, actor protocol.Actor, cause string, automatic bool,
	review protocol.Review, reviewer protocol.Engineer, number int, target protocol.ReviewTarget, causeKey, criteria string) (protocol.ReviewRound, store.JobRow, error) {
	round := protocol.ReviewRound{ID: domain.NewID(), ReviewID: review.ID, Number: number, Target: target, State: protocol.ReviewRequested, CreatedAt: h.now()}
	title := fmt.Sprintf("Review %s's %s", authorName, author.Title)
	if number > 1 {
		title += fmt.Sprintf(" (round %d)", number)
	}
	objective := "Review " + targetDescription(target) + " for: " + author.Objective
	if strings.TrimSpace(criteria) != "" {
		objective += "\nFocus: " + criteria
	}
	rj, err := h.createJob(ctx, t, jobSpec{Kind: protocol.JobKindReview, Title: truncate(title, 110), Objective: objective, Owner: reviewer.ID,
		Parent: &author, Source: author.Source, ProjectID: author.ProjectID, RepoID: target.RepoID, Depth: author.Depth, Actor: actor})
	if err != nil {
		return round, rj, err
	}
	round.ReviewJobID = rj.ID
	if err := store.InsertRound(ctx, t.tx, round, causeKey); err != nil {
		return round, rj, err
	}
	// Reviews don't block the author's job tree the way help does.
	if _, err := store.ResolveDependency(ctx, t.tx, "job", rj.ID); err != nil {
		return round, rj, err
	}
	if _, err := h.enqueueRun(ctx, t, rj, runReason{Purpose: "review", Cause: cause, Automatic: automatic}); err != nil {
		return round, rj, err
	}
	if err := store.SetRoundState(ctx, t.tx, round.ID, protocol.ReviewQueued, "", false); err != nil {
		return round, rj, err
	}
	if err := store.SetReviewState(ctx, t.tx, review.ID, protocol.ReviewQueued, number); err != nil {
		return round, rj, err
	}
	r2, _ := store.GetReview(ctx, t.tx, review.ID)
	return round, rj, t.emit(ev{Type: "review.updated", Actor: actor, Room: review.Source.RoomID, Job: review.JobID, Payload: r2})
}

func targetDescription(t protocol.ReviewTarget) string {
	switch t.Kind {
	case "patch":
		return "revision " + shortRev(t.Head) + " (against " + shortRev(t.Base) + ")"
	case "pr":
		return "the pull request at " + shortRev(t.Head)
	default:
		return "artifact " + shortRev(t.Hash)
	}
}

func (h *Hub) toolReview(ctx context.Context, t *txn, env toolEnv, a bridge.WorkReviewArgs) (any, error) {
	review, round, err := store.ReviewByReviewJob(ctx, t.tx, env.job.ID)
	if err != nil {
		return nil, domain.Invalid("This run has no review assigned.")
	}
	if review.ReviewerID != env.eng.ID {
		return nil, domain.Forbidden("Only the assigned reviewer can record this verdict.")
	}
	if !domain.ReviewOpen(round.State) || round.SupersededBy != "" {
		return nil, domain.Conflict("This review round is no longer current (%s).", round.State)
	}
	if round.Target.Head != "" && a.ExpectedHead != round.Target.Head && !strings.HasPrefix(round.Target.Head, a.ExpectedHead) {
		return nil, domain.Conflict("You reviewed %s, but this round is for %s. Review the current revision.", shortRev(a.ExpectedHead), shortRev(round.Target.Head))
	}
	// A PR target was re-synchronised with the forge just before this
	// transaction (see dispatchTool); a moved head supersedes the round.
	if round.Target.Kind == "pr" {
		if pr, err := store.GetPR(ctx, t.tx, round.Target.PullRequestID); err == nil && pr.Head != "" && pr.Head != round.Target.Head {
			return nil, domain.Conflict("The pull request moved to %s during review. Your verdict applies to %s; a new round is needed.", shortRev(pr.Head), shortRev(round.Target.Head))
		}
	}
	verdict := protocol.ReviewState(a.Verdict)
	if !domain.ReviewVerdict(verdict) {
		return nil, domain.Invalid("verdict must be approved, changes_requested, comments_only, or unable_to_review.")
	}
	blocking := 0
	for _, f := range a.Findings {
		if !contains([]string{"blocking", "suggestion", "note"}, f.Severity) {
			return nil, domain.Invalid("finding severity must be blocking, suggestion, or note.")
		}
		if f.Severity == "blocking" {
			blocking++
			if f.File == "" && strings.TrimSpace(f.Evidence) == "" {
				return nil, domain.Invalid("Blocking findings need file/line or test evidence.")
			}
		}
	}
	open, err := store.OpenBlockingFindings(ctx, t.tx, review.ID)
	if err != nil {
		return nil, err
	}
	stillOpen := 0
	for _, f := range open {
		if !contains(a.Resolve, f.ID) {
			stillOpen++
		}
	}
	if verdict == protocol.ReviewApproved && (blocking > 0 || stillOpen > 0) {
		return nil, domain.Invalid("You can't approve while blocking findings are open. Resolve them (list their IDs in resolve) or request changes.")
	}
	if verdict == protocol.ReviewChangesRequested && blocking == 0 && stillOpen == 0 {
		return nil, domain.Invalid("Requesting changes needs at least one blocking finding with evidence; use comments_only for suggestions.")
	}
	for _, id := range a.Resolve {
		f, rid, err := store.GetFinding(ctx, t.tx, id)
		if err != nil || rid != review.ID {
			return nil, domain.Invalid("Finding %s isn't part of this review.", id)
		}
		if err := store.SetFindingStatus(ctx, t.tx, f.ID, "resolved", round.Number); err != nil {
			return nil, err
		}
	}
	for _, f := range a.Findings {
		if err := store.InsertFinding(ctx, t.tx, review.ID, protocol.Finding{ID: domain.NewID(), RoundID: round.ID, Severity: f.Severity,
			Body: strings.TrimSpace(f.Body), File: f.File, Line: f.Line, Evidence: f.Evidence, Status: "open", CreatedAt: h.now()}); err != nil {
			return nil, err
		}
	}
	if err := store.SetRoundState(ctx, t.tx, round.ID, verdict, strings.TrimSpace(a.Summary), true); err != nil {
		return nil, err
	}
	if err := store.SetRoundReviewer(ctx, t.tx, round.ID, "", env.run.ID); err != nil {
		return nil, err
	}
	if err := store.SetReviewState(ctx, t.tx, review.ID, verdict, round.Number); err != nil {
		return nil, err
	}
	author, _ := store.GetEngineer(ctx, t.tx, review.AuthorID)
	body := strings.TrimSpace(a.Message)
	if body == "" {
		body = strings.TrimSpace(a.Summary)
	}
	var mentions []protocol.Mention
	if verdict != protocol.ReviewApproved && !strings.Contains(strings.ToLower(body), "@"+author.Handle) {
		body = "@" + author.Handle + " " + body
	}
	mentions = h.resolveTextMentions(ctx, t.tx, review.Source.RoomID, body)
	if _, err := t.postMessage(newMessage{Room: review.Source.RoomID, Thread: review.Source.ThreadID, Author: env.me, Kind: protocol.MessageReview,
		Body: body, Mentions: mentions, Refs: []protocol.Ref{{Kind: "review", ID: review.ID}, {Kind: "job", ID: review.JobID}},
		JobID: review.JobID, RunID: env.run.ID}); err != nil {
		return nil, err
	}
	r2, _ := store.GetReview(ctx, t.tx, review.ID)
	if err := t.emit(ev{Type: "review.updated", Actor: env.me, Room: review.Source.RoomID, Job: review.JobID, Payload: r2}); err != nil {
		return nil, err
	}
	// The review job itself is done.
	rj, _ := store.GetJob(ctx, t.tx, env.job.ID)
	if err := store.SetJobCompletion(ctx, t.tx, rj.ID, false, string(verdict)+": "+truncate(a.Summary, 300)); err != nil {
		return nil, err
	}
	if _, err := h.setJobState(ctx, t, rj.ID, protocol.JobCompleted, "", ""); err != nil {
		return nil, err
	}
	// Effects on the author's work.
	if err := h.afterVerdict(ctx, t, review, round, verdict); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "verdict": verdict, "round": round.Number, "revision": round.Target.Head,
		"note": "Recorded for this exact revision. Finish your turn."}, nil
}

func (h *Hub) afterVerdict(ctx context.Context, t *txn, review protocol.Review, round protocol.ReviewRound, verdict protocol.ReviewState) error {
	job, err := store.GetJob(ctx, t.tx, review.JobID)
	if err != nil || !domain.JobLive(job.State) {
		return err
	}
	if verdict == protocol.ReviewApproved {
		if job.CompletionRequested {
			missing, reviewOnly, err := h.completionMissing(ctx, t.tx, job)
			if err != nil {
				return err
			}
			if len(missing) == 0 {
				_, err := h.finishOrAwaitHuman(ctx, t, job)
				return err
			}
			if reviewOnly {
				_, err := h.setJobState(ctx, t, job.ID, protocol.JobReviewReady, "", "Waiting on "+strings.Join(missing, "; "))
				return err
			}
		}
		_, err := h.enqueueRun(ctx, t, job, runReason{Purpose: "continue", Cause: round.ID, Automatic: true,
			Note: h.engineerName(ctx, t.tx, review.ReviewerID) + " approved " + shortRev(round.Target.Head) + ". Complete the job if its evidence is in place."})
		if isLimit(err) {
			return nil
		}
		return err
	}
	// Changes requested, comments, or unable: the author follows up.
	if err := store.SetJobCompletion(ctx, t.tx, job.ID, false, ""); err != nil {
		return err
	}
	_, err = h.enqueueRun(ctx, t, job, runReason{Purpose: "address_review", Cause: round.ID, Automatic: true})
	if isLimit(err) {
		return nil
	}
	return err
}

func (h *Hub) toolRespondReview(ctx context.Context, t *txn, env toolEnv, a bridge.WorkRespondReviewArgs) (any, error) {
	job := env.job
	if job.OwnerID != env.eng.ID {
		return nil, domain.Forbidden("Only the author responds to review findings.")
	}
	reviews, err := store.ListJobReviews(ctx, t.tx, job.ID)
	if err != nil {
		return nil, err
	}
	if len(reviews) == 0 {
		return nil, domain.Invalid("This job has no reviews.")
	}
	head := ""
	if job.Revision != nil {
		head = job.Revision.Head
	}
	touched := map[string]protocol.Review{}
	for _, r := range a.Responses {
		f, rid, err := store.GetFinding(ctx, t.tx, r.FindingID)
		if err != nil {
			return nil, domain.Invalid("Unknown finding %s.", r.FindingID)
		}
		var review protocol.Review
		for _, rv := range reviews {
			if rv.ID == rid {
				review = rv
			}
		}
		if review.ID == "" {
			return nil, domain.Forbidden("Finding %s isn't about your work.", r.FindingID)
		}
		if err := store.InsertFindingReply(ctx, t.tx, protocol.FindingReply{ID: domain.NewID(), FindingID: f.ID, Author: env.me,
			Body: strings.TrimSpace(r.Body), Evidence: r.Evidence, Revision: head, CreatedAt: h.now()}); err != nil {
			return nil, err
		}
		status := "addressed"
		if r.Disputed {
			status = "disputed"
		}
		// The author can't resolve or dismiss a blocking finding; only the reviewer can.
		if f.Status != "resolved" {
			if err := store.SetFindingStatus(ctx, t.tx, f.ID, status, 0); err != nil {
				return nil, err
			}
		}
		touched[review.ID] = review
	}
	result := map[string]any{"ok": true, "responses": len(a.Responses)}
	if !a.RequestRereview {
		for id := range touched {
			r2, _ := store.GetReview(ctx, t.tx, id)
			_ = t.emit(ev{Type: "review.updated", Actor: env.me, Room: r2.Source.RoomID, Job: r2.JobID, Payload: r2})
		}
		return result, nil
	}
	var rounds []int
	for _, review := range reviews {
		last := review.Rounds[len(review.Rounds)-1]
		if domain.ReviewOpen(last.State) || last.State == protocol.ReviewApproved && last.Target.Head == head {
			continue
		}
		target, err := h.reviewTarget(ctx, t.tx, job, 0)
		if err != nil {
			return nil, err
		}
		if last.Number >= h.lim.MaxReviewRounds {
			return nil, domain.Limit("This review has used its %d rounds. Summarize the remaining disagreement with the reviewer in the conversation; another qualified colleague can arbitrate, or ask the owner only if a product decision is needed.", h.lim.MaxReviewRounds)
		}
		if targetKey(target) == targetKey(last.Target) && !allDisputes(a.Responses) {
			return nil, domain.Invalid("Publish your revised work first (work_publish_revision); re-review applies to a new revision. To dispute a finding without changes, mark each response disputed with evidence.")
		}
		reviewer, _ := store.GetEngineer(ctx, t.tx, review.ReviewerID)
		if err := h.requireReviewerAccess(ctx, t.tx, job, reviewer); err != nil {
			return nil, err
		}
		causeKey := job.ID + ":" + reviewer.ID + ":" + targetKey(target) + fmt.Sprintf(":r%d", last.Number+1)
		if existing, err := store.RoundByCauseKey(ctx, t.tx, causeKey); err == nil {
			rounds = append(rounds, existing.Number)
			continue
		}
		round, _, err := h.openRound(ctx, t, env, review, reviewer, last.Number+1, target, causeKey, review.Criteria)
		if err != nil {
			return nil, err
		}
		if err := store.SupersedeRound(ctx, t.tx, last.ID, round.ID); err != nil {
			return nil, err
		}
		rounds = append(rounds, round.Number)
		msg := strings.TrimSpace(a.Message)
		if msg == "" {
			msg = "Ready for another look at " + shortRev(target.Head) + "."
		}
		if !strings.Contains(strings.ToLower(msg), "@"+reviewer.Handle) {
			msg = "@" + reviewer.Handle + " " + msg
		}
		if _, err := t.postMessage(newMessage{Room: job.Source.RoomID, Thread: job.Source.ThreadID, Author: env.me, Kind: protocol.MessageReview,
			Body: msg, Mentions: h.resolveTextMentions(ctx, t.tx, job.Source.RoomID, msg),
			Refs: []protocol.Ref{{Kind: "review", ID: review.ID}, {Kind: "job", ID: job.ID}}, JobID: job.ID, RunID: env.run.ID}); err != nil {
			return nil, err
		}
	}
	result["newRounds"] = rounds
	result["note"] = "Re-review requested on the new revision. Complete with work_update (it completes once approved) or work_wait {reason:'review'}, then end your turn."
	return result, nil
}

func allDisputes(rs []bridge.FindingResponseArg) bool {
	if len(rs) == 0 {
		return false
	}
	for _, r := range rs {
		if !r.Disputed {
			return false
		}
	}
	return true
}
