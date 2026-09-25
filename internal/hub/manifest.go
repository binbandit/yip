package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/binbandit/yip/internal/bridge"
	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

const settingOrgRules = "org_rules"

// FakeScripter selects a deterministic script for the fake provider from a
// run's manifest. It is only consulted for engineers configured with the
// explicitly labelled fake provider.
var FakeScripter func(m *manifest.Manifest) json.RawMessage

// buildManifest gathers the permitted context for a run on a chosen node.
// Everything comes from the run's destination scope.
func (h *Hub) buildManifest(ctx context.Context, q store.Q, run store.RunRow, job store.JobRow, nodeID string) (protocol.ExecutionManifest, *manifest.Manifest, error) {
	var intent struct {
		Purpose    string `json:"purpose"`
		Note       string `json:"note"`
		Checkpoint bool   `json:"checkpoint"`
	}
	_ = json.Unmarshal(run.Manifest, &intent)
	ver, err := store.GetEngineerVersion(ctx, q, run.EngineerVersionID)
	if err != nil {
		return protocol.ExecutionManifest{}, nil, err
	}
	eng, err := store.GetEngineer(ctx, q, run.EngineerID)
	if err != nil {
		return protocol.ExecutionManifest{}, nil, err
	}
	room, err := store.GetRoom(ctx, q, run.Destination.RoomID)
	if err != nil {
		return protocol.ExecutionManifest{}, nil, err
	}
	users, _ := store.ListUsers(ctx, q)
	ownerName, ownerHandle := "the owner", ""
	if len(users) > 0 {
		ownerName, ownerHandle = users[0].Name, users[0].Handle
	}
	orgRules, _, _ := store.Setting(ctx, q, settingOrgRules)
	m := &manifest.Manifest{
		Version: manifest.Version, RunID: run.ID, Purpose: intent.Purpose, Note: intent.Note, OrgName: h.Org().Name,
		OwnerName: ownerName, OwnerHandle: ownerHandle, EngineerVersionID: ver.ID,
		Engineer:            manifest.Colleague{ID: eng.ID, Name: ver.Name, Handle: eng.Handle, Role: ver.Role, Tags: ver.CapabilityTags},
		EngineerDescription: ver.Description, EngineerInstructions: ver.Instructions, OrgRules: orgRules,
		RoomName: room.Name, RoomPurpose: room.Purpose, RoomPrivate: room.Private, InThread: run.Destination.ThreadID != "",
		Mode: run.Mode, Now: h.now(),
	}

	// Projects linked to the destination room, plus the job's own project.
	projectIDs := append([]string{}, room.ProjectIDs...)
	if job.ProjectID != "" && !contains(projectIDs, job.ProjectID) {
		projectIDs = append(projectIDs, job.ProjectID)
	}
	projectNames := map[string]string{}
	for _, pid := range projectIDs {
		p, err := store.GetProject(ctx, q, pid)
		if err != nil {
			continue
		}
		projectNames[pid] = p.Name
		repos := make([]string, 0, len(p.Repos))
		for _, r := range p.Repos {
			repos = append(repos, r.Name)
		}
		desc := p.Name
		if len(repos) > 0 {
			desc += " (repos: " + strings.Join(repos, ", ") + ")"
		}
		m.Projects = append(m.Projects, desc)
		if p.Instructions != "" {
			m.ProjectRules = append(m.ProjectRules, p.Name+": "+p.Instructions)
		}
	}

	// Conversation: only the destination room (or its thread).
	threadID := run.Destination.ThreadID
	if threadID == "" && job.Kind != protocol.JobKindReply && job.Source.MessageID != "" {
		threadID = "" // top-level work sees top-level history
	}
	msgs, err := store.RecentMessages(ctx, q, room.ID, threadID, h.lim.ContextMessageLimit)
	if err != nil {
		return protocol.ExecutionManifest{}, nil, err
	}
	budget := h.lim.ContextCharBudget
	names := h.actorNames(ctx, q)
	for i := len(msgs) - 1; i >= 0; i-- {
		msg := msgs[i]
		body := msg.Body
		trimmed := false
		if len(body) > 2000 {
			body, trimmed = body[:2000]+"…", true
		}
		if budget-len(body) < 0 {
			m.Omitted = append(m.Omitted, "earlier messages before seq "+itoa(int(msg.Seq)))
			break
		}
		budget -= len(body)
		n := names[msg.Author.Kind+":"+msg.Author.ID]
		m.Conversation = append([]manifest.Message{{ID: msg.ID, Seq: msg.Seq, Author: n.name, Role: n.role, Kind: msg.Kind,
			At: msg.CreatedAt, Body: body, Trimmed: trimmed}}, m.Conversation...)
	}
	if run.CauseID != "" {
		if cm, err := store.GetMessage(ctx, q, run.CauseID); err == nil && cm.RoomID == room.ID {
			n := names[cm.Author.Kind+":"+cm.Author.ID]
			m.Request = &manifest.Message{ID: cm.ID, Seq: cm.Seq, Author: n.name, Kind: cm.Kind, At: cm.CreatedAt, Body: cm.Body}
		}
	}
	if m.Request == nil && job.Source.MessageID != "" {
		if cm, err := store.GetMessage(ctx, q, job.Source.MessageID); err == nil && cm.RoomID == room.ID {
			n := names[cm.Author.Kind+":"+cm.Author.ID]
			m.Request = &manifest.Message{ID: cm.ID, Seq: cm.Seq, Author: n.name, Kind: cm.Kind, At: cm.CreatedAt, Body: cm.Body}
		}
	}

	// Job facts.
	jf := manifest.Job{ID: job.ID, Kind: job.Kind, Title: job.Title, Objective: job.Objective, Acceptance: job.Acceptance,
		State: string(job.State), Project: projectNames[job.ProjectID], PeerReview: job.RequiresPeerReview,
		HumanReview: job.RequiresHumanReview, Parent: job.ParentID, Summary: job.Summary}
	var repo *protocol.Repo
	if job.RepoID != "" {
		if r, err := store.GetRepo(ctx, q, job.RepoID); err == nil {
			repo = &r
			jf.Repo = r.Name
		}
	}
	if job.Revision != nil {
		jf.Branch, jf.Base, jf.Head = job.Revision.Branch, job.Revision.Base, job.Revision.Head
	}
	if jf.Branch == "" && repo != nil && run.Mode == protocol.ModeEdit {
		jf.Branch = "yip/" + domain.Handle(eng.Handle) + "/" + domain.Short(job.ID)
	}
	checks, _ := store.ListChecks(ctx, q, job.ID)
	for _, c := range checks {
		jf.Checks = append(jf.Checks, manifest.Check{Command: c.Command, Passed: c.Passed, ExitCode: c.ExitCode, Revision: c.Revision})
	}
	if job.HelpRequest && job.ParentID != "" {
		if p, err := store.GetJob(ctx, q, job.ParentID); err == nil {
			jf.HelpFrom = names["engineer:"+p.OwnerID].name
		}
	}
	m.Job = jf

	// Review facts.
	var snapshot *protocol.ReviewTarget
	if job.Kind == protocol.JobKindReview {
		if rev, round, err := store.ReviewByReviewJob(ctx, q, job.ID); err == nil {
			r := h.reviewFacts(ctx, q, rev, round, names)
			m.Review = &r
			t := round.Target
			snapshot = &t
		}
	} else if reviews, _ := store.ListJobReviews(ctx, q, job.ID); len(reviews) > 0 {
		for _, rev := range reviews {
			if len(rev.Rounds) == 0 {
				continue
			}
			round := rev.Rounds[len(rev.Rounds)-1]
			if round.State == protocol.ReviewChangesRequested || round.State == protocol.ReviewCommentsOnly || round.State == protocol.ReviewUnable {
				r := h.reviewFacts(ctx, q, rev, round, names)
				m.OwnReview = &r
			}
		}
	}

	// Inputs: owner steering, answers to this job's questions, help answers.
	inputs, _ := store.UnconsumedInputs(ctx, q, job.ID)
	for _, in := range inputs {
		m.Inputs = append(m.Inputs, manifest.Input{ID: in.ID, At: in.CreatedAt, Body: in.Body, Kind: "owner_input", From: ownerName})
	}
	qs, _ := store.ListQuestions(ctx, q, "job_id = ? AND status = 'answered'", job.ID)
	for _, x := range qs {
		if am, err := store.GetMessage(ctx, q, x.AnswerMessageID); err == nil {
			n := names[am.Author.Kind+":"+am.Author.ID]
			m.Inputs = append(m.Inputs, manifest.Input{ID: x.ID, At: am.CreatedAt, Kind: "answer", From: n.name,
				Body: "Q: " + x.MissingFact + "\nA: " + am.Body})
		}
	}
	children, _ := store.ListJobs(ctx, q, store.JobFilter{ParentID: job.ID, IncludeReply: true,
		States: []protocol.JobState{protocol.JobCompleted, protocol.JobFailed, protocol.JobCancelled}})
	for _, c := range children {
		if c.Kind == protocol.JobKindReview {
			continue
		}
		from := names["engineer:"+c.OwnerID].name
		switch {
		case c.State == protocol.JobCompleted && c.HelpRequest:
			m.Inputs = append(m.Inputs, manifest.Input{ID: c.ID, At: c.UpdatedAt, Kind: "help_answer", From: from,
				Body: "Your request \"" + truncate(c.Objective, 200) + "\" was answered: " + c.Summary})
		case c.State == protocol.JobCompleted:
			m.Inputs = append(m.Inputs, manifest.Input{ID: c.ID, At: c.UpdatedAt, Kind: "dependency_outcome", From: from,
				Body: "Child work \"" + c.Title + "\" completed: " + c.Summary})
		default:
			m.Inputs = append(m.Inputs, manifest.Input{ID: c.ID, At: c.UpdatedAt, Kind: "dependency_outcome", From: from,
				Body: "Child work \"" + c.Title + "\" " + string(c.State) + ": " + firstNonEmpty(c.StateDetail, c.Summary)})
		}
	}

	// Decisions visible in this destination.
	decisions, _ := h.visibleDecisions(ctx, q, room, "")
	for _, d := range decisions {
		scope := d.Scope.Kind
		if d.Scope.Kind == "project" {
			scope = "project " + projectNames[d.Scope.ID]
		}
		src := ""
		if len(d.Sources) > 0 {
			src = d.Sources[0].Kind + " " + d.Sources[0].ID
		}
		m.Decisions = append(m.Decisions, manifest.Decision{ID: d.ID, Title: d.Title, Body: d.Body, Scope: scope, Source: src})
	}

	// Colleagues and their permitted access.
	engineers, _ := store.ListEngineers(ctx, q)
	for _, e := range engineers {
		if e.ID == eng.ID || e.Archived {
			continue
		}
		c := manifest.Colleague{ID: e.ID, Name: e.Name, Handle: e.Handle, Role: e.Role, Tags: e.CapabilityTags, InRoom: contains(e.RoomIDs, room.ID)}
		if job.ProjectID != "" {
			if g, err := store.GetGrant(ctx, q, job.ProjectID, e.ID); err == nil {
				c.Access = g.Access
			} else {
				c.Access = "none"
			}
		}
		if n, _ := store.CountActiveRuns(ctx, q, e.ID, "", "", true); n > 0 {
			c.Busy = "busy right now"
		} else if len(e.ActiveJobIDs) > 0 {
			c.Busy = itoa(len(e.ActiveJobIDs)) + " open job(s)"
		}
		m.Colleagues = append(m.Colleagues, c)
	}
	used, _, _ := store.WakeupsUsed(ctx, q, job.RootRequestID)
	m.Budget = manifest.Budget{WakeupsUsed: used, WakeupsMax: h.lim.MaxWakeupsPerRoot, Depth: job.Depth, DepthMax: h.lim.MaxDelegationDepth}

	grants, _ := store.GrantsDigest(ctx, q, eng.ID)
	membersVersion, _ := store.MembersVersion(ctx, q, room.ID)
	m.ScopeFingerprint = manifest.Fingerprint(eng.ID, room.ID, itoa(int(membersVersion)), boolStr(room.Private),
		strings.Join(room.ProjectIDs, ","), grants, contextKey(run))

	// Execution manifest for the runner.
	x := protocol.ExecutionManifest{
		RunID: run.ID, JobID: job.ID, Attempt: run.Attempt, EngineerID: eng.ID, EngineerName: ver.Name, EngineerVersionID: ver.ID,
		Provider: run.Provider, Model: firstNonEmpty(run.Model, ver.Provider.Model), Mode: run.Mode, ExecutionProfile: run.ExecutionProfile,
		ScopeFingerprint: m.ScopeFingerprint, Tools: bridge.NamesForMode(run.Mode), TimeoutMs: h.lim.RunTimeout.Milliseconds(),
	}
	if repo != nil {
		spec := &protocol.RepoSpec{RepoID: repo.ID, Name: repo.Name, RemoteURL: repo.RemoteURL, DefaultBranch: repo.DefaultBranch,
			BaseRev: jf.Base, Branch: jf.Branch}
		if snapshot != nil && snapshot.Head != "" {
			spec.SnapshotRev, spec.BaseRev, spec.Branch = snapshot.Head, snapshot.Base, ""
			if snapshot.PullRequestID != "" {
				if pr, err := store.GetPR(ctx, q, snapshot.PullRequestID); err == nil && pr.Forge == "github" {
					spec.FetchRefs = []string{fmt.Sprintf("refs/pull/%d/head", pr.Number)}
				}
			}
			if b := h.bundleFor(ctx, q, snapshot.Head); b != nil {
				spec.CheckpointArtifact = b
			}
		} else if jf.Head != "" {
			// Continuing work: the runner reuses its worktree, or restores the
			// verified bundle of the latest published revision on another node.
			if b := h.bundleFor(ctx, q, jf.Head); b != nil {
				spec.CheckpointArtifact = b
			}
		}
		x.Repo = spec
		if spec.CheckpointArtifact != nil {
			m.RestoreArtifactID = spec.CheckpointArtifact.ID
		}
		if p, err := store.GetProject(ctx, q, repo.ProjectID); err == nil {
			x.Checks = p.Policy.Checks
		}
	}
	if s, err := store.FindProviderSession(ctx, q, eng.ID, run.Provider, run.ProfileID, contextKey(run), nodeID, m.ScopeFingerprint); err == nil {
		x.ResumeSessionID = s.VendorSessionID
	}
	x.Instructions = m.Instructions()
	x.Prompt = m.Prompt()
	if raw, err := json.Marshal(m); err == nil {
		x.Context = raw
	}
	if run.Provider == "fake" && FakeScripter != nil {
		x.FakeScript = FakeScripter(m)
	}
	return x, m, nil
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

type actorName struct{ name, role string }

func (h *Hub) actorNames(ctx context.Context, q store.Q) map[string]actorName {
	out := map[string]actorName{"system:hub": {name: "yip"}}
	if users, err := store.ListUsers(ctx, q); err == nil {
		for _, u := range users {
			out["user:"+u.ID] = actorName{name: u.Name}
		}
	}
	if engs, err := store.ListEngineers(ctx, q); err == nil {
		for _, e := range engs {
			out["engineer:"+e.ID] = actorName{name: e.Name, role: e.Role}
		}
	}
	return out
}

func (h *Hub) reviewFacts(ctx context.Context, q store.Q, rev protocol.Review, round protocol.ReviewRound, names map[string]actorName) manifest.Review {
	r := manifest.Review{ReviewID: rev.ID, Round: round.Number, Author: names["engineer:"+rev.AuthorID].name,
		Reviewer: names["engineer:"+rev.ReviewerID].name, Criteria: rev.Criteria, TargetKind: round.Target.Kind,
		Base: round.Target.Base, Head: round.Target.Head, ArtifactID: round.Target.ArtifactID, Verdict: string(round.State),
		RoundsLeft: h.lim.MaxReviewRounds - round.Number}
	if round.Target.PullRequestID != "" {
		if pr, err := store.GetPR(ctx, q, round.Target.PullRequestID); err == nil {
			r.PullRequest = "#" + itoa(pr.Number) + " " + pr.URL
		}
	}
	for _, rd := range rev.Rounds {
		for _, f := range rd.Findings {
			mf := manifest.Finding{ID: f.ID, Round: rd.Number, Severity: f.Severity, Body: f.Body, File: f.File, Line: f.Line,
				Evidence: f.Evidence, Status: f.Status}
			for _, rp := range f.Replies {
				mf.Replies = append(mf.Replies, names[rp.Author.Kind+":"+rp.Author.ID].name+": "+rp.Body)
			}
			r.Findings = append(r.Findings, mf)
		}
		if rd.Number < round.Number {
			r.PriorRounds = append(r.PriorRounds, "round "+itoa(rd.Number)+" on "+shortRev(rd.Target.Head)+": "+string(rd.State))
		}
	}
	return r
}

// bundleFor finds the verified bundle artifact of a published revision.
func (h *Hub) bundleFor(ctx context.Context, q store.Q, head string) *protocol.Artifact {
	var id string
	err := q.QueryRowContext(ctx, `SELECT id FROM artifacts WHERE kind = 'bundle' AND revision = ? AND verified = 1 ORDER BY created_at DESC LIMIT 1`, head).Scan(&id)
	if err != nil {
		return nil
	}
	a, err := store.GetArtifact(ctx, q, id)
	if err != nil {
		return nil
	}
	return &a
}

// visibleDecisions returns accepted, current decisions visible in a room:
// project-scoped decisions for projects linked to the room whose visibility
// is unrestricted or includes this room, room-scoped decisions of this room,
// and organisation-wide decisions. A query narrows by full-text search.
func (h *Hub) visibleDecisions(ctx context.Context, q store.Q, room protocol.Room, query string) ([]protocol.Decision, error) {
	all, err := store.ListDecisions(ctx, q, `status = 'accepted'`)
	if err != nil {
		return nil, err
	}
	var match map[string]bool
	if strings.TrimSpace(query) != "" {
		ids, err := store.SearchDecisions(ctx, q, ftsQuery(query), 50)
		if err == nil {
			match = map[string]bool{}
			for _, id := range ids {
				match[id] = true
			}
		}
	}
	var out []protocol.Decision
	for _, d := range all {
		if match != nil && !match[d.ID] {
			continue
		}
		if !decisionVisibleIn(d, room) {
			continue
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func decisionVisibleIn(d protocol.Decision, room protocol.Room) bool {
	if d.VisibleRoomIDs != nil && !contains(d.VisibleRoomIDs, room.ID) {
		return false
	}
	switch d.Scope.Kind {
	case "project":
		return contains(room.ProjectIDs, d.Scope.ID)
	case "room":
		return d.Scope.ID == room.ID
	case "org":
		return true
	}
	return false
}

// ftsQuery turns free text into a safe FTS5 query (prefix-matched terms).
func ftsQuery(s string) string {
	var terms []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127)
	}) {
		if len(w) < 2 {
			continue
		}
		terms = append(terms, `"`+w+`"*`)
		if len(terms) == 8 {
			break
		}
	}
	if len(terms) == 0 {
		return `""`
	}
	return strings.Join(terms, " ")
}
