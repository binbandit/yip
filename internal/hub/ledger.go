package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
		if j.Source.RoomID != room.ID {
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
