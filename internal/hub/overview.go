package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

var liveStates = []protocol.JobState{protocol.JobQueued, protocol.JobRunning, protocol.JobWaiting, protocol.JobReviewReady, protocol.JobFailed}

// ledgerForRoom lists work visible from a room: jobs started there, plus work
// in the room's projects that originated in non-private rooms. Status in a
// shared room is filtered to material suitable for that room.
func (h *Hub) ledgerForRoom(ctx context.Context, q store.Q, room protocol.Room, includeCompleted bool) ([]protocol.WorkRow, error) {
	states := liveStates
	if includeCompleted {
		states = append(append([]protocol.JobState{}, liveStates...), protocol.JobCompleted)
	}
	jobs, err := store.ListJobs(ctx, q, store.JobFilter{States: states, Limit: 200})
	if err != nil {
		return nil, err
	}
	var out []protocol.WorkRow
	for _, j := range jobs {
		if j.Kind == protocol.JobKindReview || j.ParentID != "" {
			continue
		}
		if room.Kind != protocol.RoomKindOverview && j.Source.RoomID != room.ID {
			if !contains(room.ProjectIDs, j.ProjectID) {
				continue
			}
			src, err := store.GetRoom(ctx, q, j.Source.RoomID)
			if err != nil || src.Private {
				continue
			}
		}
		out = append(out, h.workRow(ctx, q, j))
	}
	return out, nil
}

func (h *Hub) workRow(ctx context.Context, q store.Q, j store.JobRow) protocol.WorkRow {
	row := protocol.WorkRow{Job: j.Job, LastConfirmed: j.LastActivity, LastConfirmedAt: j.LastActivityAt}
	if j.ProjectID != "" {
		if p, err := store.GetProject(ctx, q, j.ProjectID); err == nil {
			row.ProjectName = p.Name
		}
	}
	if r, err := store.GetRoom(ctx, q, j.Source.RoomID); err == nil {
		row.RoomName = r.Name
	}
	if j.NodeID != "" {
		row.NodeName = h.nodeName(ctx, q, j.NodeID)
	}
	if j.CurrentRunID != "" {
		if r, err := store.GetRun(ctx, q, j.CurrentRunID); err == nil {
			row.RunState = r.State
		}
	}
	switch j.State {
	case protocol.JobWaiting, protocol.JobFailed:
		row.Blocker = j.StateDetail
	case protocol.JobReviewReady:
		row.Blocker = j.StateDetail
	}
	return row
}

// Overview returns the owner's cross-project view: catch-up since the last
// visit, the factual work ledger, and useful decisions.
func (h *Hub) Overview(ctx context.Context, userID string, markSeen bool) (protocol.Overview, error) {
	q := h.st.R()
	u, err := store.GetUser(ctx, q, userID)
	if err != nil {
		return protocol.Overview{}, err
	}
	var since *time.Time
	if u.Preferences.LastSeenAt != "" {
		if t, err := time.Parse(time.RFC3339, u.Preferences.LastSeenAt); err == nil {
			since = &t
		}
	}
	ov := protocol.Overview{Since: since, Catchup: []protocol.CatchupItem{}, Work: []protocol.WorkRow{}, Decisions: []protocol.Decision{}, Questions: []protocol.Question{}}
	ov.RoomID, _ = store.OverviewRoomID(ctx, q, userID)
	rooms, err := store.RoomIDsForMember(ctx, q, protocol.ActorUser, userID)
	if err != nil {
		return ov, err
	}
	jobs, err := store.ListJobs(ctx, q, store.JobFilter{RoomIDs: rooms, States: append(append([]protocol.JobState{}, liveStates...), protocol.JobCompleted), Limit: 100})
	if err != nil {
		return ov, err
	}
	for _, j := range jobs {
		if j.Kind == protocol.JobKindReview || j.ParentID != "" {
			continue
		}
		if j.State == protocol.JobCompleted && j.CompletedAt != nil && h.now().Sub(*j.CompletedAt) > 7*24*time.Hour {
			continue
		}
		ov.Work = append(ov.Work, h.workRow(ctx, q, j))
	}
	sort.SliceStable(ov.Work, func(a, b int) bool { return workRank(ov.Work[a].Job) < workRank(ov.Work[b].Job) })
	ov.Catchup = h.catchup(ctx, q, rooms, since)
	if ds, err := h.ListDecisions(ctx, userID, "accepted"); err == nil {
		if len(ds) > 6 {
			ds = ds[:6]
		}
		ov.Decisions = ds
	}
	if qs, err := store.ListQuestions(ctx, q, "status = 'open' AND recipient_id = ?", userID); err == nil {
		ov.Questions = qs
	}
	if markSeen {
		u.Preferences.LastSeenAt = h.now().Format(time.RFC3339)
		_ = h.SetPreferences(ctx, userID, u.Preferences)
	}
	return ov, nil
}

func workRank(j protocol.Job) int {
	switch j.State {
	case protocol.JobWaiting, protocol.JobFailed:
		return 0
	case protocol.JobRunning, protocol.JobReviewReady:
		return 1
	case protocol.JobQueued:
		return 2
	}
	return 3
}

// catchup is generated from persisted events, so every item traces back to
// its source. Unseen conversations are not marked read by it.
func (h *Hub) catchup(ctx context.Context, q store.Q, rooms []string, since *time.Time) []protocol.CatchupItem {
	from := h.now().Add(-24 * time.Hour)
	if since != nil {
		from = *since
	}
	evs, err := store.EventsSince(ctx, q, from, []string{"job.updated", "decision.updated", "question.created", "approval.created"}, 400)
	if err != nil {
		return []protocol.CatchupItem{}
	}
	names := h.actorNames(ctx, q)
	seen := map[string]bool{}
	var out []protocol.CatchupItem
	for _, e := range evs {
		if e.RoomID != "" && !contains(rooms, e.RoomID) {
			continue
		}
		switch e.Type {
		case "job.updated":
			var j protocol.Job
			if json.Unmarshal(e.Payload, &j) != nil || j.Kind == protocol.JobKindReply || j.Kind == protocol.JobKindReview || seen["job:"+j.ID] {
				continue
			}
			// Events arrive newest first: the first one per job is its current
			// state, and older ones (an earlier wait or failure) are history.
			seen["job:"+j.ID] = true
			owner := names["engineer:"+j.OwnerID].name
			var item *protocol.CatchupItem
			switch j.State {
			case protocol.JobCompleted:
				item = &protocol.CatchupItem{Kind: "completed", Title: owner + " completed " + j.Title, Detail: j.Summary}
			case protocol.JobFailed:
				item = &protocol.CatchupItem{Kind: "failed", Title: j.Title + " failed", Detail: j.StateDetail}
			case protocol.JobWaiting:
				kind := "blocker"
				if j.WaitingReason == protocol.WaitRecovery {
					kind = "unknown"
				}
				item = &protocol.CatchupItem{Kind: kind, Title: owner + " is waiting: " + j.Title, Detail: j.StateDetail}
			default:
				continue // running, queued or ready: nothing to catch up on
			}
			item.At, item.ActorID, item.RoomID, item.ThreadID, item.MessageID = e.OccurredAt, j.OwnerID, j.Source.RoomID, j.Source.ThreadID, j.Source.MessageID
			item.Refs, item.EventSeq = []protocol.Ref{{Kind: "job", ID: j.ID}}, e.Sequence
			out = append(out, *item)
		case "decision.updated":
			var d protocol.Decision
			if json.Unmarshal(e.Payload, &d) != nil || seen["decision:"+d.ID] {
				continue
			}
			seen["decision:"+d.ID] = true // a later supersession or rejection wins
			if d.Status != "accepted" {
				continue
			}
			if d.VisibleRoomIDs != nil && !anyIn(d.VisibleRoomIDs, rooms) {
				continue
			}
			out = append(out, protocol.CatchupItem{Kind: "decision", Title: "Decision: " + d.Title, Detail: truncate(d.Body, 240), At: e.OccurredAt,
				RoomID: firstSourceRoom(d), Refs: []protocol.Ref{{Kind: "decision", ID: d.ID}}, EventSeq: e.Sequence})
		case "question.created":
			var x protocol.Question
			if json.Unmarshal(e.Payload, &x) != nil {
				continue
			}
			cur, err := store.GetQuestion(ctx, q, x.ID)
			if err != nil || cur.Status != "open" {
				continue
			}
			out = append(out, protocol.CatchupItem{Kind: "question", Title: names["engineer:"+x.AskerID].name + " asked: " + truncate(x.MissingFact, 120),
				Detail: "Continuing meanwhile: " + firstNonEmpty(x.ContinuingWith, "independent work"), At: e.OccurredAt, ActorID: x.AskerID,
				RoomID: x.Source.RoomID, ThreadID: x.Source.ThreadID, MessageID: x.MessageID, Refs: []protocol.Ref{{Kind: "question", ID: x.ID}}, EventSeq: e.Sequence})
		case "approval.created":
			var a protocol.Approval
			if json.Unmarshal(e.Payload, &a) != nil {
				continue
			}
			cur, err := store.GetApproval(ctx, q, a.ID)
			if err != nil || cur.Status != "pending" || h.now().After(cur.ExpiresAt) {
				continue
			}
			out = append(out, protocol.CatchupItem{Kind: "approval", Title: names["engineer:"+cur.EngineerID].name + " needs your permission: " + truncate(cur.Action.Summary, 120),
				Detail: cur.Action.Detail, At: e.OccurredAt, ActorID: cur.EngineerID, RoomID: cur.Source.RoomID, ThreadID: cur.Source.ThreadID,
				MessageID: cur.Source.MessageID, Refs: []protocol.Ref{{Kind: "approval", ID: cur.ID}, {Kind: "job", ID: cur.JobID}}, EventSeq: e.Sequence})
		}
	}
	if out == nil {
		out = []protocol.CatchupItem{}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].At.After(out[b].At) })
	return out
}

// answerStatus replies in the Overview conversation from the ledger itself:
// known status needs no engineer run, and nothing invents progress. It covers
// every project the user's rooms reach, saying so when one is quiet, and
// lists what is waiting on the user (questions and permissions).
func (h *Hub) answerStatus(ctx context.Context, t *txn, userID string, room protocol.Room, msg protocol.Message) error {
	rooms, err := store.RoomIDsForMember(ctx, t.tx, protocol.ActorUser, userID)
	if err != nil {
		return err
	}
	// Active work is never crowded out by finished work: they're read apart.
	live, err := store.ListJobs(ctx, t.tx, store.JobFilter{RoomIDs: rooms, States: liveStates, Limit: 200})
	if err != nil {
		return err
	}
	since := h.now().Add(-72 * time.Hour)
	done, err := store.ListJobs(ctx, t.tx, store.JobFilter{RoomIDs: rooms, States: []protocol.JobState{protocol.JobCompleted}, Since: &since, Limit: 40})
	if err != nil {
		return err
	}
	names := h.actorNames(ctx, t.tx)
	byProject := map[string][]store.JobRow{}
	var order []string
	add := func(key string) {
		if _, ok := byProject[key]; !ok {
			byProject[key] = nil
			order = append(order, key)
		}
	}
	for _, j := range append(live, done...) {
		if j.Kind == protocol.JobKindReview || j.Kind == protocol.JobKindReply {
			continue
		}
		if j.State == protocol.JobCompleted && (j.CompletedAt == nil || j.CompletedAt.Before(since)) {
			continue
		}
		key := "No project"
		if p, err := store.GetProject(ctx, t.tx, j.ProjectID); err == nil {
			key = p.Name
		}
		add(key)
		byProject[key] = append(byProject[key], j)
	}
	// Every project linked to one of the user's rooms is accounted for.
	var quiet []string
	if ps, err := store.ListProjects(ctx, t.tx); err == nil {
		for _, p := range ps {
			if !anyIn(p.RoomIDs, rooms) {
				continue
			}
			if _, ok := byProject[p.Name]; !ok {
				quiet = append(quiet, p.Name)
			}
		}
	}
	sort.Strings(order)
	sort.Strings(quiet)
	var b strings.Builder
	if len(order) == 0 {
		b.WriteString("Nothing is in flight, and nothing finished in the last three days.")
	} else {
		b.WriteString("Here's where things stand, from the work ledger:\n")
		for _, p := range order {
			fmt.Fprintf(&b, "\n**%s**\n", p)
			for _, j := range byProject[p] {
				fmt.Fprintf(&b, "- %s\n", statusLine(j, names["engineer:"+j.OwnerID].name))
			}
		}
	}
	if len(quiet) > 0 {
		fmt.Fprintf(&b, "\nQuiet: %s — nothing in flight or finished in the last three days.\n", strings.Join(quiet, ", "))
	}
	open, _ := store.ListQuestions(ctx, t.tx, "status = 'open' AND recipient_id = ?", userID)
	pending, _ := store.ListApprovals(ctx, t.tx, "status = 'pending' AND expires_at > ?", store.TS(h.now()))
	var waits []string
	for _, x := range open {
		if !contains(rooms, x.Source.RoomID) {
			continue
		}
		r, _ := store.GetRoom(ctx, t.tx, x.Source.RoomID)
		waits = append(waits, fmt.Sprintf("%s asked in #%s: %s", names["engineer:"+x.AskerID].name, r.Name, x.MissingFact))
	}
	for _, a := range pending {
		if !contains(rooms, a.Source.RoomID) {
			continue
		}
		r, _ := store.GetRoom(ctx, t.tx, a.Source.RoomID)
		waits = append(waits, fmt.Sprintf("%s needs your permission in #%s: %s", names["engineer:"+a.EngineerID].name, r.Name, a.Action.Summary))
	}
	if len(waits) > 0 {
		b.WriteString("\nWaiting on you, in their conversations:\n")
		for _, w := range waits {
			fmt.Fprintf(&b, "- %s\n", w)
		}
	}
	var refs []protocol.Ref
	for _, p := range order {
		for _, j := range byProject[p] {
			refs = append(refs, protocol.Ref{Kind: "job", ID: j.ID})
		}
	}
	_, err = t.postMessage(newMessage{Room: room.ID, Author: systemActor, Kind: protocol.MessageStatus, Body: strings.TrimSpace(b.String()),
		Refs: refs, ReplyTo: msg.ID, Cause: msg.ID})
	return err
}

// statusLine is one job's line in a status answer, from its recorded state.
func statusLine(j store.JobRow, owner string) string {
	switch j.State {
	case protocol.JobCompleted:
		return fmt.Sprintf("%s completed: %s", owner, j.Title)
	case protocol.JobRunning:
		return fmt.Sprintf("%s is working on %s: %s", owner, j.Title, strings.ToLower(firstNonEmpty(j.LastActivity, "in progress")))
	case protocol.JobQueued:
		return fmt.Sprintf("%s has %s queued%s", owner, j.Title, detailSuffix(j.StateDetail))
	case protocol.JobReviewReady:
		return fmt.Sprintf("%s: %s — %s", owner, j.Title, firstNonEmpty(j.StateDetail, "in review"))
	case protocol.JobWaiting:
		return fmt.Sprintf("%s is waiting on %s: %s", owner, j.Title, firstNonEmpty(j.StateDetail, j.WaitingReason))
	case protocol.JobFailed:
		return fmt.Sprintf("%s: %s failed — %s", owner, j.Title, j.StateDetail)
	}
	return fmt.Sprintf("%s: %s (%s)", owner, j.Title, j.State)
}

func detailSuffix(s string) string {
	if s == "" {
		return ""
	}
	return " — " + s
}

// Search finds rooms, engineers, projects, messages, work, and decisions the
// user may see. Every result opens its actual source. Room and project
// filters apply inside the queries, before ranking. A work ID (whole, or six
// or more characters from either end) finds that work directly.
func (h *Hub) Search(ctx context.Context, userID, query, roomScope, projectScope string) ([]protocol.SearchResult, error) {
	q := h.st.R()
	query = strings.TrimSpace(query)
	out := []protocol.SearchResult{}
	if query == "" {
		return out, nil
	}
	rooms, err := store.ListRoomsForUser(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	// visible: every room the scope allows (work is filtered by its own
	// project); inProject: the rooms linked to the project, for messages.
	var visible, roomIDs []string
	lower := strings.ToLower(query)
	for _, r := range rooms {
		if roomScope != "" && r.ID != roomScope {
			continue
		}
		visible = append(visible, r.ID)
		if projectScope != "" && !contains(r.ProjectIDs, projectScope) {
			continue
		}
		roomIDs = append(roomIDs, r.ID)
		if strings.Contains(strings.ToLower(r.Name+" "+r.Purpose), lower) {
			out = append(out, protocol.SearchResult{Kind: "room", ID: r.ID, Title: r.Name, Snippet: r.Purpose, RoomID: r.ID})
		}
	}
	if byID, err := store.FindJobsByID(ctx, q, query, visible, 5); err == nil {
		for _, j := range byID {
			if projectScope == "" || h.jobInProject(ctx, j.ID, projectScope) {
				out = append(out, j)
			}
		}
	}
	if roomScope == "" && projectScope == "" {
		if engs, err := store.ListEngineers(ctx, q); err == nil {
			for _, e := range engs {
				if strings.Contains(strings.ToLower(e.Name+" "+e.Handle+" "+e.Role+" "+strings.Join(e.CapabilityTags, " ")), lower) {
					out = append(out, protocol.SearchResult{Kind: "engineer", ID: e.ID, Title: e.Name, Snippet: e.Role})
				}
			}
		}
		if ps, err := store.ListProjects(ctx, q); err == nil {
			for _, p := range ps {
				if strings.Contains(strings.ToLower(p.Name+" "+p.Description), lower) {
					out = append(out, protocol.SearchResult{Kind: "project", ID: p.ID, Title: p.Name, Snippet: p.Description})
				}
			}
		}
	}
	fts := ftsQuery(query)
	jobRooms := roomIDs
	if projectScope != "" {
		jobRooms = visible
	}
	if jobs, err := store.SearchJobs(ctx, q, fts, jobRooms, projectScope, 15); err == nil {
		for _, j := range jobs {
			if !containsResult(out, j) {
				out = append(out, j)
			}
		}
	}
	if msgs, err := store.SearchMessages(ctx, q, fts, roomIDs, 30); err == nil {
		names := h.actorNames(ctx, q)
		for i := range msgs {
			if m, err := store.GetMessage(ctx, q, msgs[i].ID); err == nil {
				n := names[m.Author.Kind+":"+m.Author.ID].name
				msgs[i].Title = n
			}
		}
		out = append(out, msgs...)
	}
	if ds, err := h.ListDecisions(ctx, userID, "accepted"); err == nil {
		ids, _ := store.SearchDecisions(ctx, q, fts, roomIDs, 20)
		for _, d := range ds {
			if !contains(ids, d.ID) {
				continue
			}
			if roomScope != "" {
				r, err := store.GetRoom(ctx, q, roomScope)
				if err != nil || !decisionVisibleIn(d, r) {
					continue
				}
			}
			if projectScope != "" && !(d.Scope.Kind == "project" && d.Scope.ID == projectScope) && !anyIn(d.VisibleRoomIDs, roomIDs) {
				continue
			}
			out = append(out, protocol.SearchResult{Kind: "decision", ID: d.ID, Title: d.Title, Snippet: truncate(d.Body, 160), RoomID: firstSourceRoom(d)})
		}
	}
	return out, nil
}

func (h *Hub) jobInProject(ctx context.Context, jobID, projectID string) bool {
	j, err := store.GetJob(ctx, h.st.R(), jobID)
	return err == nil && j.ProjectID == projectID
}

func containsResult(rs []protocol.SearchResult, r protocol.SearchResult) bool {
	for _, x := range rs {
		if x.Kind == r.Kind && x.ID == r.ID {
			return true
		}
	}
	return false
}

// describeEvent renders a human-readable activity line for a job's history.
func describeEvent(ctx context.Context, h *Hub, q store.Q, e store.EventRow) string {
	names := h.actorNames(ctx, q)
	who := names[e.Actor.Kind+":"+e.Actor.ID].name
	switch e.Type {
	case "job.created":
		var j protocol.Job
		_ = json.Unmarshal(e.Payload, &j)
		return who + " created the work"
	case "run.created":
		var r protocol.Run
		_ = json.Unmarshal(e.Payload, &r)
		return fmt.Sprintf("Attempt %d queued for %s", r.Attempt, names["engineer:"+r.EngineerID].name)
	case "revision.published":
		var r protocol.RevisionRecord
		_ = json.Unmarshal(e.Payload, &r)
		return fmt.Sprintf("%s published %s (%d files changed)", who, shortRev(r.Head), r.FilesChanged)
	case "check.recorded":
		var c protocol.Check
		_ = json.Unmarshal(e.Payload, &c)
		status := "passed"
		if !c.Passed {
			status = fmt.Sprintf("failed (exit %d)", c.ExitCode)
		}
		return fmt.Sprintf("`%s` %s on %s", c.Command, status, shortRev(c.Revision))
	case "review.updated":
		var r protocol.Review
		_ = json.Unmarshal(e.Payload, &r)
		if len(r.Rounds) == 0 {
			return ""
		}
		rd := r.Rounds[len(r.Rounds)-1]
		return fmt.Sprintf("Review round %d by %s: %s (%s)", rd.Number, names["engineer:"+r.ReviewerID].name, strings.ReplaceAll(string(rd.State), "_", " "), shortRev(rd.Target.Head))
	case "question.created":
		return who + " asked a question in the conversation"
	case "question.updated":
		return "The question was answered"
	case "approval.created":
		return who + " needed an exceptional permission"
	case "approval.updated":
		var a protocol.Approval
		_ = json.Unmarshal(e.Payload, &a)
		return "Permission " + a.Status + ": " + a.Action.Summary
	case "permission.auto":
		var p struct {
			Action   protocol.ApprovalAction `json:"action"`
			Decision string                  `json:"decision"`
			Reason   string                  `json:"reason"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		return fmt.Sprintf("Policy %sed: %s (%s)", strings.TrimSuffix(p.Decision, "e"), p.Action.Summary, p.Reason)
	case "input.updated":
		var in protocol.JobInput
		_ = json.Unmarshal(e.Payload, &in)
		switch in.Delivery {
		case "immediate":
			return "Owner's update delivered to the running attempt"
		case "queued":
			return "Owner's update queued for the next step"
		}
		return ""
	case "artifact.published":
		var a protocol.Artifact
		_ = json.Unmarshal(e.Payload, &a)
		return who + " published " + a.Name
	case "run.stale_report":
		return "A stale attempt reported late; kept as diagnostic evidence only"
	}
	return ""
}
