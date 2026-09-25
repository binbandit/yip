package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

type jobSpec struct {
	Kind                string
	Title               string
	Objective           string
	Acceptance          []string
	Owner               string
	Contributors        []string
	Parent              *store.JobRow
	Root                string
	Source              protocol.Destination
	ProjectID           string
	RepoID              string
	ProjectIDs          []string
	RequiresPeerReview  bool
	RequiresHumanReview bool
	Depth               int
	Priority            int
	Actor               protocol.Actor
}

// createJob inserts a job and emits its event. It never starts a run.
func (h *Hub) createJob(ctx context.Context, t *txn, s jobSpec) (store.JobRow, error) {
	if s.Owner == "" {
		return store.JobRow{}, domain.Invalid("Work needs an owner.")
	}
	owner, err := store.GetEngineer(ctx, t.tx, s.Owner)
	if err != nil || owner.Archived {
		return store.JobRow{}, domain.Invalid("That engineer can't own work right now.")
	}
	if s.Parent != nil {
		if s.Depth == 0 {
			s.Depth = s.Parent.Depth
		}
		if s.Root == "" {
			s.Root = s.Parent.RootRequestID
		}
	}
	if s.Depth > h.lim.MaxDelegationDepth {
		return store.JobRow{}, domain.Limit("This would exceed %d levels of delegation. Continue the work directly or ask in the room.", h.lim.MaxDelegationDepth)
	}
	if s.ProjectID == "" && len(s.ProjectIDs) == 1 {
		s.ProjectID = s.ProjectIDs[0]
	}
	now := h.now()
	j := store.JobRow{Job: protocol.Job{
		ID: domain.NewID(), OrgID: h.Org().ID, Kind: s.Kind, Title: strings.TrimSpace(s.Title), Objective: strings.TrimSpace(s.Objective),
		Acceptance: nonNil(s.Acceptance), State: protocol.JobQueued, OwnerID: s.Owner, RootRequestID: s.Root, Source: s.Source,
		ProjectID: s.ProjectID, RepoID: s.RepoID, RequiresPeerReview: s.RequiresPeerReview, RequiresHumanReview: s.RequiresHumanReview,
		Depth: s.Depth, Priority: s.Priority, CreatedAt: now, UpdatedAt: now, LastActivity: "Queued", LastActivityAt: &now,
	}}
	if s.Parent != nil {
		j.ParentID = s.Parent.ID
	}
	if j.Title == "" {
		j.Title = truncate(firstLine(j.Objective), 90)
	}
	if s.RepoID != "" {
		j.Revision = &protocol.Revision{RepoID: s.RepoID}
	}
	if err := store.InsertJob(ctx, t.tx, j); err != nil {
		return j, err
	}
	for _, c := range s.Contributors {
		if c != s.Owner {
			if err := store.AddAssignment(ctx, t.tx, j.ID, c, "contributor"); err != nil {
				return j, err
			}
		}
	}
	if s.Parent != nil {
		if err := store.AddDependency(ctx, t.tx, s.Parent.ID, "job", j.ID); err != nil {
			return j, err
		}
	}
	if s.Source.MessageID != "" && s.Kind != protocol.JobKindReply && s.Parent == nil {
		_ = store.AppendMessageRefs(ctx, t.tx, s.Source.MessageID, protocol.Ref{Kind: "job", ID: j.ID})
		if m, err := store.GetMessage(ctx, t.tx, s.Source.MessageID); err == nil {
			_ = t.emit(ev{Type: "message.updated", Room: m.RoomID, Payload: m})
		}
	}
	if s.Source.ThreadID == "" && s.Source.MessageID != "" {
		_ = store.SetThreadOwner(ctx, t.tx, s.Source.MessageID, s.Source.RoomID, s.Owner)
	}
	j, err = store.GetJob(ctx, t.tx, j.ID)
	if err != nil {
		return j, err
	}
	actor := s.Actor
	if actor.Kind == "" {
		actor = protocol.Actor{Kind: protocol.ActorEngineer, ID: s.Owner}
	}
	return j, t.emit(ev{Type: "job.created", Actor: actor, Room: j.Source.RoomID, Thread: j.Source.ThreadID, Job: j.ID,
		Root: j.RootRequestID, Cause: s.Source.MessageID, Payload: j.Job})
}

// jobChanged reloads a job and emits job.updated.
func (t *txn) jobChanged(ctx context.Context, jobID string) (store.JobRow, error) {
	j, err := store.GetJob(ctx, t.tx, jobID)
	if err != nil {
		return j, err
	}
	return j, t.emit(ev{Type: "job.updated", Room: j.Source.RoomID, Thread: j.Source.ThreadID, Job: j.ID, Root: j.RootRequestID, Payload: j.Job})
}

// setJobState applies a validated job transition.
func (h *Hub) setJobState(ctx context.Context, t *txn, jobID string, to protocol.JobState, waiting, detail string) (store.JobRow, error) {
	j, err := store.GetJob(ctx, t.tx, jobID)
	if err != nil {
		return j, err
	}
	if j.State == to && j.WaitingReason == waiting && j.StateDetail == detail {
		return j, nil
	}
	if err := domain.CheckJobTransition(j.State, to); err != nil {
		return j, domain.Conflict("%s", err.Error())
	}
	if to != protocol.JobWaiting {
		waiting = ""
	}
	ok, err := store.SetJobState(ctx, t.tx, jobID, j.Version, to, waiting, detail)
	if err != nil {
		return j, err
	}
	if !ok {
		return j, domain.Conflict("The work changed concurrently; try again.")
	}
	if domain.JobTerminal(to) || to == protocol.JobFailed {
		if _, err := store.CancelJobQuestions(ctx, t.tx, jobID); err != nil {
			return j, err
		}
		// A parent waiting on this job learns its outcome, whatever it is.
		if to != protocol.JobCompleted {
			if cur, err := store.GetJob(ctx, t.tx, jobID); err == nil && cur.ParentID != "" {
				if err := h.resolveJobDependency(ctx, t, cur); err != nil {
					return j, err
				}
			}
		}
	}
	t.kickAfter()
	return t.jobChanged(ctx, jobID)
}

// runReason describes why a run is being created.
type runReason struct {
	Purpose    string // message | start | address_review | review | answer | input | help | help_answered | continue | retry | resume
	Cause      string
	Automatic  bool // an agent-caused wakeup that counts against the root budget
	Note       string
	Checkpoint bool
}

// enqueueRun creates the next attempt for a job unless one is already
// pending or active. Automatic wakeups are bounded per root request.
func (h *Hub) enqueueRun(ctx context.Context, t *txn, job store.JobRow, why runReason) (store.RunRow, error) {
	if !domain.JobLive(job.State) {
		return store.RunRow{}, domain.Conflict("That work is %s.", job.State)
	}
	if active, err := store.ActiveRunForJob(ctx, t.tx, job.ID); err == nil {
		// One attempt at a time. A trigger that arrives while an attempt is
		// active is deferred and replayed when it ends; an unknown attempt
		// blocks automatic relaunch until explicitly recovered.
		if active.State != protocol.RunUnknown && why.Purpose != "" {
			if err := store.SetPendingWake(ctx, t.tx, job.ID, &store.PendingWake{Purpose: why.Purpose, Cause: why.Cause,
				Note: why.Note, Automatic: why.Automatic}); err != nil {
				return store.RunRow{}, err
			}
		}
		return active, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.RunRow{}, err
	}
	if why.Automatic {
		key := fmt.Sprintf("%s:%s:%s", why.Cause, job.ID, why.Purpose)
		claimed, dup, err := store.ClaimWakeup(ctx, t.tx, domain.NewID(), job.RootRequestID, why.Cause, job.OwnerID, why.Purpose, key, true, h.lim.MaxWakeupsPerRoot)
		if err != nil {
			return store.RunRow{}, err
		}
		if dup {
			return store.RunRow{}, nil
		}
		if !claimed {
			if _, err := h.setJobState(ctx, t, job.ID, protocol.JobWaiting, protocol.WaitDependency,
				fmt.Sprintf("The automatic collaboration budget for this request (%d wakeups) is used up.", h.lim.MaxWakeupsPerRoot)); err != nil && !isConflict(err) {
				return store.RunRow{}, err
			}
			return store.RunRow{}, domain.Limit("This request has used its %d automatic wakeups. Existing work and evidence stay available; continue directly or ask in the room.", h.lim.MaxWakeupsPerRoot)
		}
	}
	eng, err := store.GetEngineer(ctx, t.tx, job.OwnerID)
	if err != nil {
		return store.RunRow{}, err
	}
	attempt, err := store.NextAttempt(ctx, t.tx, job.ID)
	if err != nil {
		return store.RunRow{}, err
	}
	var prev string
	if runs, _ := store.ListJobRuns(ctx, t.tx, job.ID); len(runs) > 0 {
		prev = runs[len(runs)-1].ID
	}
	run := store.RunRow{Run: protocol.Run{
		ID: domain.NewID(), JobID: job.ID, Attempt: attempt, EngineerID: eng.ID, EngineerVersionID: eng.VersionID,
		Provider: eng.Provider.Provider, Model: eng.Provider.Model, ProfileID: eng.Provider.ProfileID, Mode: runMode(job),
		State: protocol.RunCreated, Destination: job.Source, CauseID: firstNonEmpty(why.Cause, job.RootRequestID),
		RootRequestID: job.RootRequestID, PreviousRunID: prev, CreatedAt: h.now(),
	}, ExecutionProfile: "native"}
	if job.Revision != nil {
		run.Branch, run.BaseRev = job.Revision.Branch, job.Revision.Base
	}
	if p, err := store.GetProject(ctx, t.tx, job.ProjectID); err == nil && p.Policy.ExecutionProfile != "" {
		run.ExecutionProfile = p.Policy.ExecutionProfile
	}
	if run.Mode == protocol.ModeReadOnly && run.ExecutionProfile == "native" {
		run.ExecutionProfile = "readonly"
	}
	intent, _ := jsonMarshal(map[string]any{"purpose": why.Purpose, "note": why.Note, "checkpoint": why.Checkpoint})
	run.Manifest = intent
	if err := store.InsertRun(ctx, t.tx, run); err != nil {
		return run, err
	}
	if err := store.SetJobCurrentRun(ctx, t.tx, job.ID, run.ID); err != nil {
		return run, err
	}
	if job.State != protocol.JobQueued && job.State != protocol.JobRunning {
		if _, err := h.setJobState(ctx, t, job.ID, protocol.JobQueued, "", ""); err != nil {
			return run, err
		}
	} else if _, err := t.jobChanged(ctx, job.ID); err != nil {
		return run, err
	}
	run, err = store.GetRun(ctx, t.tx, run.ID)
	if err != nil {
		return run, err
	}
	t.kickAfter()
	return run, t.emit(ev{Type: "run.created", Room: run.Destination.RoomID, Thread: run.Destination.ThreadID, Job: job.ID, Run: run.ID,
		Cause: run.CauseID, Root: run.RootRequestID, Payload: run.Run})
}

func isConflict(err error) bool {
	var de *domain.Error
	return errors.As(err, &de) && de.Code == "conflict"
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// runMode maps job kind and scope to the execution boundary.
func runMode(j store.JobRow) string {
	switch j.Kind {
	case protocol.JobKindReply:
		return protocol.ModeConversation
	case protocol.JobKindReview:
		return protocol.ModeReadOnly
	case protocol.JobKindCode:
		return protocol.ModeEdit
	default:
		if j.RepoID != "" {
			return protocol.ModeEdit
		}
		return protocol.ModeConversation
	}
}

// ---- inputs (steering) ----

// addJobInput attaches a follow-up to a selected job. The receipt reports the
// actual delivery mode; it never claims immediate delivery that didn't happen.
func (h *Hub) addJobInput(ctx context.Context, t *txn, userID, jobID string, msg protocol.Message, clientKey string) (protocol.JobInput, error) {
	job, err := store.GetJob(ctx, t.tx, jobID)
	if err != nil {
		return protocol.JobInput{}, domain.NotFound("That work doesn't exist.")
	}
	if job.Source.RoomID != msg.RoomID {
		return protocol.JobInput{}, domain.Invalid("That work belongs to another conversation.")
	}
	if !domain.JobLive(job.State) {
		return protocol.JobInput{}, domain.Conflict("That work is %s. Send a new message to start follow-up work.", job.State)
	}
	in := protocol.JobInput{ID: domain.NewID(), JobID: jobID, Body: msg.Body, MessageID: msg.ID, CreatedAt: h.now(), Delivery: "queued"}
	run, err := store.ActiveRunForJob(ctx, t.tx, jobID)
	switch {
	case err == nil && (run.State == protocol.RunRunning || run.State == protocol.RunAwaitingInput || run.State == protocol.RunPreparing || run.State == protocol.RunOffered):
		in.Delivery, in.RunID = "pending", run.ID
		if err := store.InsertJobInput(ctx, t.tx, in, clientKey); err != nil {
			return in, err
		}
		if err := h.queueCommand(ctx, t, run.NodeID, run.ID, run.LeaseEpoch, protocol.CmdDeliverInput, "input:"+in.ID,
			protocol.DeliverInput{InputID: in.ID, Text: msg.Body}); err != nil {
			return in, err
		}
	case err == nil:
		// Created (not yet offered): the next manifest includes it.
		if err := store.InsertJobInput(ctx, t.tx, in, clientKey); err != nil {
			return in, err
		}
	case errors.Is(err, store.ErrNotFound):
		if err := store.InsertJobInput(ctx, t.tx, in, clientKey); err != nil {
			return in, err
		}
		// New input from the owner always gives the engineer a chance to act
		// on it, including work that was waiting for information.
		if _, err := h.enqueueRun(ctx, t, job, runReason{Purpose: "input", Cause: msg.ID}); err != nil {
			return in, err
		}
	default:
		return in, err
	}
	_ = store.AppendMessageRefs(ctx, t.tx, msg.ID, protocol.Ref{Kind: "job", ID: jobID})
	if m, err := store.GetMessage(ctx, t.tx, msg.ID); err == nil {
		_ = t.emit(ev{Type: "message.updated", Room: m.RoomID, Payload: m})
	}
	return in, t.emit(ev{Type: "input.updated", Actor: userActor(userID), Room: job.Source.RoomID, Job: jobID, Payload: in})
}

// AddJobInput is the HTTP entry for POST /v1/jobs/{id}/input.
func (h *Hub) AddJobInput(ctx context.Context, userID, jobID string, req protocol.JobInputRequest) (protocol.JobInputResponse, error) {
	var resp protocol.JobInputResponse
	err := h.do(ctx, func(t *txn) error {
		if req.ClientKey != "" {
			if prior, err := store.GetJobInputByClientKey(ctx, t.tx, req.ClientKey); err == nil {
				resp.Input = prior
				return nil
			}
		}
		job, err := store.GetJob(ctx, t.tx, jobID)
		if err != nil {
			return domain.NotFound("That work doesn't exist.")
		}
		if _, err := h.requireRoom(ctx, t.tx, userID, job.Source.RoomID); err != nil {
			return err
		}
		msg, err := t.postMessage(newMessage{Room: job.Source.RoomID, Thread: job.Source.ThreadID, Author: userActor(userID),
			Body: strings.TrimSpace(req.Body), Refs: []protocol.Ref{{Kind: "job", ID: jobID}}})
		if err != nil {
			return err
		}
		resp.Message = &msg
		if req.QuestionID != "" {
			if _, err := h.answerQuestion(ctx, t, req.QuestionID, msg); err != nil {
				return err
			}
			return nil
		}
		resp.Input, err = h.addJobInput(ctx, t, userID, jobID, msg, req.ClientKey)
		return err
	})
	return resp, err
}

// ---- completion policy ----

// completionMissing lists the evidence a job still lacks. An engineer saying
// "done" is not completion; the published revision, recorded checks, and
// required reviews are.
func (h *Hub) completionMissing(ctx context.Context, q store.Q, job store.JobRow) ([]string, bool, error) {
	var missing []string
	reviewOnly := true
	switch job.Kind {
	case protocol.JobKindCode:
		head := ""
		if job.Revision != nil {
			head = job.Revision.Head
		}
		if head == "" {
			missing = append(missing, "a published result revision (call work_publish_revision)")
			reviewOnly = false
			break
		}
		if job.DiffArtifactID == "" {
			missing = append(missing, "the diff for "+shortRev(head))
			reviewOnly = false
		}
		checks, err := store.ListChecks(ctx, q, job.ID)
		if err != nil {
			return nil, false, err
		}
		latest := map[string]protocol.Check{}
		for _, c := range checks {
			if c.Revision == head {
				latest[c.Command] = c
			}
		}
		passing := 0
		var failing []string
		for cmd, c := range latest {
			if c.Passed {
				passing++
			} else {
				failing = append(failing, cmd)
			}
		}
		sort.Strings(failing)
		for _, f := range failing {
			missing = append(missing, "a passing run of `"+f+"` on "+shortRev(head))
			reviewOnly = false
		}
		if p, err := store.GetProject(ctx, q, job.ProjectID); err == nil {
			for _, cmd := range p.Policy.Checks {
				if c, ok := latest[cmd]; !ok || !c.Passed {
					if ok {
						continue // already reported as failing
					}
					missing = append(missing, "the project check `"+cmd+"` on "+shortRev(head))
					reviewOnly = false
				}
			}
		}
		if passing == 0 && len(failing) == 0 {
			missing = append(missing, "at least one recorded passing check on "+shortRev(head)+" (use work_run_check)")
			reviewOnly = false
		}
		if job.RequiresPeerReview {
			ok, why, err := h.peerApproved(ctx, q, job, head)
			if err != nil {
				return nil, false, err
			}
			if !ok {
				missing = append(missing, why)
			}
		}
	case protocol.JobKindInvestigation, protocol.JobKindDocument:
		if strings.TrimSpace(job.Summary) == "" {
			missing = append(missing, "a summary of findings")
			reviewOnly = false
		}
		if job.RequiresPeerReview {
			rs, _ := store.ListJobReviews(ctx, q, job.ID)
			approved := false
			for _, r := range rs {
				if r.State == protocol.ReviewApproved {
					approved = true
				}
			}
			if !approved {
				missing = append(missing, "a peer review approval")
			}
		}
	case protocol.JobKindReview:
		_, round, err := store.ReviewByReviewJob(ctx, q, job.ID)
		if err == nil && domain.ReviewOpen(round.State) {
			missing = append(missing, "a recorded review verdict (work_review)")
			reviewOnly = false
		}
	}
	return missing, reviewOnly && len(missing) > 0, nil
}

// peerApproved reports whether an independent colleague approved exactly
// this head revision with no unresolved blocking findings.
func (h *Hub) peerApproved(ctx context.Context, q store.Q, job store.JobRow, head string) (bool, string, error) {
	reviews, err := store.ListJobReviews(ctx, q, job.ID)
	if err != nil {
		return false, "", err
	}
	if len(reviews) == 0 {
		return false, "a peer review of " + shortRev(head) + " (choose a suitable colleague with work_request_review)", nil
	}
	var pending []string
	for _, r := range reviews {
		if len(r.Rounds) == 0 {
			continue
		}
		cur := r.Rounds[len(r.Rounds)-1]
		name := h.engineerName(ctx, q, r.ReviewerID)
		if cur.Target.Head != head {
			pending = append(pending, name+"'s review is of an older revision; request another round on "+shortRev(head))
			continue
		}
		if cur.State != protocol.ReviewApproved {
			pending = append(pending, name+"'s approval of "+shortRev(head)+" ("+strings.ReplaceAll(string(cur.State), "_", " ")+")")
			continue
		}
		open, err := store.OpenBlockingFindings(ctx, q, r.ID)
		if err != nil {
			return false, "", err
		}
		if len(open) > 0 {
			pending = append(pending, fmt.Sprintf("%d unresolved blocking finding(s) from %s", len(open), name))
			continue
		}
		return true, "", nil
	}
	return false, strings.Join(pending, "; "), nil
}

func shortRev(rev string) string {
	if len(rev) > 8 {
		return rev[:8]
	}
	return rev
}

// requestCompletion handles work_update(state=completed).
func (h *Hub) requestCompletion(ctx context.Context, t *txn, job store.JobRow, summary string) (string, error) {
	if err := store.SetJobCompletion(ctx, t.tx, job.ID, true, strings.TrimSpace(summary)); err != nil {
		return "", err
	}
	job, err := store.GetJob(ctx, t.tx, job.ID)
	if err != nil {
		return "", err
	}
	missing, reviewOnly, err := h.completionMissing(ctx, t.tx, job)
	if err != nil {
		return "", err
	}
	if len(missing) > 0 && !reviewOnly {
		if err := store.SetJobCompletion(ctx, t.tx, job.ID, false, ""); err != nil {
			return "", err
		}
		return "", domain.Incomplete(missing, "Not complete yet. Missing: %s.", strings.Join(missing, "; "))
	}
	if len(missing) > 0 {
		detail := "Waiting on " + strings.Join(missing, "; ")
		if _, err := h.setJobState(ctx, t, job.ID, protocol.JobReviewReady, "", detail); err != nil {
			return "", err
		}
		return "Completion recorded. The job completes automatically once " + strings.Join(missing, "; ") + ". Finish your turn now.", nil
	}
	return h.finishOrAwaitHuman(ctx, t, job)
}

// finishOrAwaitHuman completes a job whose evidence satisfies policy, unless
// an explicit human-review requirement applies.
func (h *Hub) finishOrAwaitHuman(ctx context.Context, t *txn, job store.JobRow) (string, error) {
	if job.RequiresHumanReview {
		rev := ""
		if job.Revision != nil {
			rev = shortRev(job.Revision.Head)
		}
		detail := "Checks and reviews pass. Waiting for the owner to accept " + firstNonEmpty(rev, "the result") + " (required by policy)."
		if _, err := h.setJobState(ctx, t, job.ID, protocol.JobReviewReady, "", detail); err != nil {
			return "", err
		}
		owner, _ := h.ownerID(ctx, t.tx)
		if _, err := t.postMessage(newMessage{Room: job.Source.RoomID, Thread: job.Source.ThreadID,
			Author: protocol.Actor{Kind: protocol.ActorEngineer, ID: job.OwnerID}, Kind: protocol.MessageResult,
			Body:     firstNonEmpty(job.Summary, job.Title) + "\n\nThis project requires your acceptance of the exact result revision before it counts as complete.",
			Mentions: []protocol.Mention{{Kind: protocol.ActorUser, ID: owner}}, Refs: []protocol.Ref{{Kind: "job", ID: job.ID}}, JobID: job.ID,
			Root: job.RootRequestID}); err != nil {
			return "", err
		}
		return "Evidence and reviews pass. The project requires the owner's acceptance of this exact revision; the job is review_ready.", nil
	}
	if err := h.finishJob(ctx, t, job); err != nil {
		return "", err
	}
	return "Completed. The result has been posted with its evidence.", nil
}

// finishJob marks a job completed, posts the result, and resumes dependents.
func (h *Hub) finishJob(ctx context.Context, t *txn, job store.JobRow) error {
	if _, err := h.setJobState(ctx, t, job.ID, protocol.JobCompleted, "", ""); err != nil {
		return err
	}
	if job.Kind != protocol.JobKindReply && job.Kind != protocol.JobKindReview {
		body := strings.TrimSpace(job.Summary)
		if body == "" {
			body = "Completed: " + job.Title
		}
		if _, err := t.postMessage(newMessage{Room: job.Source.RoomID, Thread: job.Source.ThreadID,
			Author: protocol.Actor{Kind: protocol.ActorEngineer, ID: job.OwnerID}, Kind: protocol.MessageResult, Body: body,
			Refs: []protocol.Ref{{Kind: "job", ID: job.ID}}, JobID: job.ID, Root: job.RootRequestID}); err != nil {
			return err
		}
		if err := h.autoAcceptDecisions(ctx, t, job); err != nil {
			return err
		}
	}
	return h.resolveJobDependency(ctx, t, job)
}

// resolveJobDependency resumes a parent waiting on this job.
func (h *Hub) resolveJobDependency(ctx context.Context, t *txn, job store.JobRow) error {
	parents, err := store.ResolveDependency(ctx, t.tx, "job", job.ID)
	if err != nil {
		return err
	}
	for _, pid := range parents {
		p, err := store.GetJob(ctx, t.tx, pid)
		if err != nil || !domain.JobLive(p.State) {
			continue
		}
		if p.State == protocol.JobWaiting && p.WaitingReason == protocol.WaitDependency {
			if open, _ := store.OpenDependencies(ctx, t.tx, pid); open == 0 {
				if _, err := h.enqueueRun(ctx, t, p, runReason{Purpose: "dependency_resolved", Cause: job.ID, Automatic: true}); err != nil && !isLimit(err) {
					return err
				}
			}
		}
	}
	return nil
}

func isLimit(err error) bool {
	var de *domain.Error
	return errors.As(err, &de) && de.Code == "limit_reached"
}

// ---- owner operations ----

// ListJobs returns the factual work ledger visible to the user.
func (h *Hub) ListJobs(ctx context.Context, userID string, f store.JobFilter) ([]protocol.Job, error) {
	rooms, err := store.RoomIDsForMember(ctx, h.st.R(), protocol.ActorUser, userID)
	if err != nil {
		return nil, err
	}
	f.RoomIDs = rooms
	rows, err := store.ListJobs(ctx, h.st.R(), f)
	if err != nil {
		return nil, err
	}
	out := make([]protocol.Job, len(rows))
	for i, r := range rows {
		out[i] = r.Job
	}
	return out, nil
}

// JobDetail returns everything needed to inspect a job and its evidence.
func (h *Hub) JobDetail(ctx context.Context, userID, jobID string) (protocol.JobDetail, error) {
	q := h.st.R()
	job, err := store.GetJob(ctx, q, jobID)
	if err != nil {
		return protocol.JobDetail{}, domain.NotFound("That work doesn't exist.")
	}
	if _, err := h.requireRoom(ctx, q, userID, job.Source.RoomID); err != nil {
		return protocol.JobDetail{}, domain.NotFound("That work doesn't exist.")
	}
	d := protocol.JobDetail{Job: job.Job}
	runs, err := store.ListJobRuns(ctx, q, jobID)
	if err != nil {
		return d, err
	}
	for _, r := range runs {
		d.Runs = append(d.Runs, r.Run)
	}
	if d.Checks, err = store.ListChecks(ctx, q, jobID); err != nil {
		return d, err
	}
	if d.Artifacts, err = store.ListJobArtifacts(ctx, q, jobID); err != nil {
		return d, err
	}
	if d.Reviews, err = store.ListJobReviews(ctx, q, jobID); err != nil {
		return d, err
	}
	if d.Questions, err = store.ListQuestions(ctx, q, "job_id = ?", jobID); err != nil {
		return d, err
	}
	approvals, err := store.ListApprovals(ctx, q, "job_id = ?", jobID)
	if err != nil {
		return d, err
	}
	for _, a := range approvals {
		d.Approvals = append(d.Approvals, a.Approval)
	}
	if d.PullRequests, err = store.ListPRs(ctx, q, "job_id = ?", jobID); err != nil {
		return d, err
	}
	children, err := store.ListJobs(ctx, q, store.JobFilter{ParentID: jobID, IncludeReply: true})
	if err != nil {
		return d, err
	}
	for _, c := range children {
		d.Children = append(d.Children, c.Job)
	}
	if d.Inputs, err = store.ListJobInputs(ctx, q, jobID); err != nil {
		return d, err
	}
	d.Decisions, _ = store.ListDecisions(ctx, q, `id IN (SELECT decision_id FROM decision_sources WHERE source_kind = 'job' AND source_id = ?)`, jobID)
	d.Activity, _ = h.jobActivity(ctx, q, jobID)
	d.Revisions = []protocol.RevisionRecord{}
	if evs, err := store.EventsForJob(ctx, q, jobID, 1000); err == nil {
		for _, e := range evs {
			if e.Type == "revision.published" {
				var r protocol.RevisionRecord
				if json.Unmarshal(e.Payload, &r) == nil {
					d.Revisions = append(d.Revisions, r)
				}
			}
		}
	}
	if domain.JobLive(job.State) && job.Kind != protocol.JobKindReply {
		d.Missing, _, _ = h.completionMissing(ctx, q, job)
	}
	d.Runs, d.Checks, d.Artifacts = nonNilRuns(d.Runs), nonNilChecks(d.Checks), nonNilArtifacts(d.Artifacts)
	if d.Approvals == nil {
		d.Approvals = []protocol.Approval{}
	}
	if d.Children == nil {
		d.Children = []protocol.Job{}
	}
	if d.Missing == nil {
		d.Missing = []string{}
	}
	return d, nil
}

func nonNilRuns(v []protocol.Run) []protocol.Run {
	if v == nil {
		return []protocol.Run{}
	}
	return v
}
func nonNilChecks(v []protocol.Check) []protocol.Check {
	if v == nil {
		return []protocol.Check{}
	}
	return v
}
func nonNilArtifacts(v []protocol.Artifact) []protocol.Artifact {
	if v == nil {
		return []protocol.Artifact{}
	}
	return v
}

// CancelJob stops a job and (optionally) its whole tree. Active processes
// receive stop commands; queued children leave the queue. Termination that
// cannot be confirmed is later shown as unknown, not as cancelled.
func (h *Hub) CancelJob(ctx context.Context, userID, jobID string, req protocol.CancelJobRequest) (protocol.Job, error) {
	var out protocol.Job
	err := h.do(ctx, func(t *txn) error {
		job, err := store.GetJob(ctx, t.tx, jobID)
		if err != nil {
			return domain.NotFound("That work doesn't exist.")
		}
		if _, err := h.requireRoom(ctx, t.tx, userID, job.Source.RoomID); err != nil {
			return err
		}
		ids := []string{jobID}
		desc, err := store.JobDescendants(ctx, t.tx, jobID)
		if err != nil {
			return err
		}
		ids = append(ids, desc...) // cancel is always the whole tree beneath
		reason := firstNonEmpty(strings.TrimSpace(req.Reason), "Stopped by the owner")
		for _, id := range ids {
			if err := h.cancelOne(ctx, t, id, reason, userActor(userID)); err != nil {
				return err
			}
		}
		j, err := store.GetJob(ctx, t.tx, jobID)
		out = j.Job
		return err
	})
	return out, err
}

func (h *Hub) cancelOne(ctx context.Context, t *txn, jobID, reason string, actor protocol.Actor) error {
	job, err := store.GetJob(ctx, t.tx, jobID)
	if err != nil {
		return err
	}
	if domain.JobTerminal(job.State) {
		return nil
	}
	runs, err := store.ListJobRuns(ctx, t.tx, jobID)
	if err != nil {
		return err
	}
	for _, r := range runs {
		switch {
		case r.State == protocol.RunCreated:
			if err := store.SetRunState(ctx, t.tx, r.ID, protocol.RunCancelled, reason); err != nil {
				return err
			}
		case r.State == protocol.RunOffered:
			if err := store.SetRunState(ctx, t.tx, r.ID, protocol.RunCancelled, reason); err != nil {
				return err
			}
			if err := store.CancelOutboxForRun(ctx, t.tx, r.ID, "offer:"); err != nil {
				return err
			}
			if err := h.queueCommand(ctx, t, r.NodeID, r.ID, r.LeaseEpoch, protocol.CmdCancelRun, "cancel:"+r.ID,
				protocol.CancelRun{Reason: reason, GraceMs: 10000}); err != nil {
				return err
			}
		case domain.RunHoldsLease(r.State) && r.State != protocol.RunStopping:
			if err := store.SetRunState(ctx, t.tx, r.ID, protocol.RunStopping, reason); err != nil {
				return err
			}
			if err := h.queueCommand(ctx, t, r.NodeID, r.ID, r.LeaseEpoch, protocol.CmdCancelRun, "cancel:"+r.ID,
				protocol.CancelRun{Reason: reason, GraceMs: 10000}); err != nil {
				return err
			}
		default:
			continue
		}
		rr, _ := store.GetRun(ctx, t.tx, r.ID)
		if err := t.emit(ev{Type: "run.updated", Room: rr.Destination.RoomID, Job: jobID, Run: r.ID, Payload: rr.Run}); err != nil {
			return err
		}
	}
	// Pending exact-action approvals and open review rounds end with the job.
	approvals, _ := store.ListApprovals(ctx, t.tx, "job_id = ? AND status = 'pending'", jobID)
	for _, a := range approvals {
		if _, err := store.SetApprovalStatus(ctx, t.tx, a.ID, "pending", "cancelled"); err != nil {
			return err
		}
		a2, _ := store.GetApproval(ctx, t.tx, a.ID)
		_ = t.emit(ev{Type: "approval.updated", Room: a.Source.RoomID, Job: jobID, Payload: a2.Approval})
	}
	if job.Kind == protocol.JobKindReview {
		if rev, round, err := store.ReviewByReviewJob(ctx, t.tx, jobID); err == nil && domain.ReviewOpen(round.State) {
			_ = store.SetRoundState(ctx, t.tx, round.ID, protocol.ReviewCancelled, reason, true)
			_ = store.SetReviewState(ctx, t.tx, rev.ID, protocol.ReviewCancelled, round.Number)
			r2, _ := store.GetReview(ctx, t.tx, rev.ID)
			_ = t.emit(ev{Type: "review.updated", Room: rev.Source.RoomID, Job: rev.JobID, Payload: r2})
		}
	}
	if _, err := h.setJobState(ctx, t, jobID, protocol.JobCancelled, "", reason); err != nil {
		return err
	}
	return t.audit(actor, "owner", "job.cancel", jobID, "ok", reason)
}

// RetryJob starts a new explicit attempt after recovery checks. An attempt
// whose outcome is unknown is superseded explicitly, never silently.
func (h *Hub) RetryJob(ctx context.Context, userID, jobID string, req protocol.RetryJobRequest) (protocol.Job, error) {
	var out protocol.Job
	err := h.do(ctx, func(t *txn) error {
		job, err := store.GetJob(ctx, t.tx, jobID)
		if err != nil {
			return domain.NotFound("That work doesn't exist.")
		}
		if _, err := h.requireRoom(ctx, t.tx, userID, job.Source.RoomID); err != nil {
			return err
		}
		if domain.JobTerminal(job.State) {
			return domain.Conflict("That work is %s. Start follow-up work with a new message.", job.State)
		}
		if active, err := store.ActiveRunForJob(ctx, t.tx, jobID); err == nil {
			if active.State != protocol.RunUnknown {
				return domain.Conflict("An attempt is already %s.", active.State)
			}
			note := "Superseded by an explicit retry; the earlier attempt's outcome was never confirmed."
			if err := store.SetRunState(ctx, t.tx, active.ID, protocol.RunFailed, note); err != nil {
				return err
			}
		}
		if job.State == protocol.JobFailed {
			if _, err := h.setJobState(ctx, t, jobID, protocol.JobQueued, "", ""); err != nil {
				return err
			}
			job, _ = store.GetJob(ctx, t.tx, jobID)
		}
		if err := store.SetJobRetry(ctx, t.tx, jobID, nil, 0); err != nil {
			return err
		}
		if _, err := h.enqueueRun(ctx, t, job, runReason{Purpose: "retry", Cause: jobID, Note: req.Reason, Checkpoint: req.FromCheckpoint}); err != nil {
			return err
		}
		if err := t.audit(userActor(userID), "owner", "job.retry", jobID, "ok", req.Reason); err != nil {
			return err
		}
		j, err := store.GetJob(ctx, t.tx, jobID)
		out = j.Job
		return err
	})
	return out, err
}

// AcceptJob accepts an exact result revision when human review is required.
func (h *Hub) AcceptJob(ctx context.Context, userID, jobID string, req protocol.AcceptJobRequest) (protocol.Job, error) {
	var out protocol.Job
	err := h.do(ctx, func(t *txn) error {
		job, err := store.GetJob(ctx, t.tx, jobID)
		if err != nil {
			return domain.NotFound("That work doesn't exist.")
		}
		if _, err := h.requireRoom(ctx, t.tx, userID, job.Source.RoomID); err != nil {
			return err
		}
		if !job.RequiresHumanReview {
			return domain.Invalid("This work completes through its evidence and peer review; it has no acceptance step.")
		}
		if job.Version != req.Version {
			return domain.Conflict("The work changed since you looked at it. Review the current revision.")
		}
		if job.State != protocol.JobReviewReady {
			return domain.Conflict("The work isn't ready for acceptance (%s).", job.State)
		}
		head := ""
		if job.Revision != nil {
			head = job.Revision.Head
		}
		if req.Revision != head {
			return domain.Conflict("You reviewed %s, but the current result is %s.", shortRev(req.Revision), shortRev(head))
		}
		missing, _, err := h.completionMissing(ctx, t.tx, job)
		if err != nil {
			return err
		}
		if len(missing) > 0 {
			return domain.Incomplete(missing, "The result still lacks: %s.", strings.Join(missing, "; "))
		}
		if err := t.audit(userActor(userID), "owner", "job.accept", jobID, "ok", "revision "+head); err != nil {
			return err
		}
		if err := h.finishJob(ctx, t, job); err != nil {
			return err
		}
		j, err := store.GetJob(ctx, t.tx, jobID)
		out = j.Job
		return err
	})
	return out, err
}

func (h *Hub) jobActivity(ctx context.Context, q store.Q, jobID string) ([]protocol.ActivityItem, error) {
	evs, err := store.EventsForJob(ctx, q, jobID, 500)
	if err != nil {
		return nil, err
	}
	var out []protocol.ActivityItem
	for _, e := range evs {
		text := describeEvent(ctx, h, q, e)
		if text == "" {
			continue
		}
		out = append(out, protocol.ActivityItem{At: e.OccurredAt, Actor: e.Actor, Type: e.Type, Text: text, RoomID: e.RoomID, Refs: []protocol.Ref{}})
	}
	if out == nil {
		out = []protocol.ActivityItem{}
	}
	return out, nil
}

// retryDelay is yip's own re-check interval when a provider gave no reset time.
const retryDelay = 10 * time.Minute
