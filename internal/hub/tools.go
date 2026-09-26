package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

// toolEnv is the authority a tool call runs with. It is derived entirely
// from the authenticated run lease; nothing here comes from tool arguments.
type toolEnv struct {
	run  store.RunRow
	job  store.JobRow
	eng  protocol.Engineer
	room protocol.Room
	me   protocol.Actor
}

// HandleToolCall executes one agent tool call for a leased run.
func (h *Hub) HandleToolCall(ctx context.Context, nodeID, runID string, epoch int64, call protocol.ToolCall) protocol.ToolResult {
	fail := func(err error) protocol.ToolResult {
		de := domain.AsError(err)
		if de.Code == "internal" {
			h.log.Error("tool call failed", "tool", call.Tool, "run", runID, "err", err)
		}
		return protocol.ToolResult{CallID: call.CallID, OK: false, Error: de.API(call.CallID)}
	}
	if len(call.Args) > h.lim.MaxToolPayloadBytes {
		return fail(domain.Invalid("Tool arguments are too large (limit %d bytes).", h.lim.MaxToolPayloadBytes))
	}
	run, err := requireLease(ctx, h.st.R(), nodeID, runID, epoch)
	if err != nil {
		h.quarantineIfStale(ctx, nodeID, runID, epoch, "tool_call", call.CallID, call.Tool+" "+truncate(string(call.Args), 300))
		return fail(domain.Forbidden("This run no longer holds a valid lease; the call was not executed."))
	}
	if run.State == protocol.RunStopping || !domain.RunHoldsLease(run.State) {
		return fail(domain.Forbidden("This run is %s; no further tool calls are admitted.", run.State))
	}
	if call.CallID != "" {
		if ok, res, found, err := store.GetToolCall(ctx, h.st.R(), run.ID, call.CallID); err == nil && found {
			if ok {
				return protocol.ToolResult{CallID: call.CallID, OK: true, Result: json.RawMessage(res)}
			}
			var apiErr protocol.APIError
			_ = json.Unmarshal([]byte(res), &apiErr)
			return protocol.ToolResult{CallID: call.CallID, OK: false, Error: &apiErr}
		}
	}
	// Access is re-checked on every call: revoking room membership or a
	// project grant mid-job stops further tools and publication.
	if err := h.checkRunAccess(ctx, h.st.R(), run); err != nil {
		return fail(err)
	}
	if !strings.HasPrefix(call.Tool, "_") {
		t, ok := bridge.Lookup(call.Tool)
		if !ok {
			return fail(domain.Invalid("Unknown tool %q.", call.Tool))
		}
		if !t.Allowed(run.Mode) {
			return fail(domain.Forbidden("%s is not available in a %s run.", call.Tool, run.Mode))
		}
	}
	// The same call failing again and again is a loop, not progress.
	sum := sha256.Sum256(append([]byte(run.ID+"|"+call.Tool+"|"), call.Args...))
	repeatKey := hex.EncodeToString(sum[:])
	h.mu.Lock()
	failures := h.failedCalls[repeatKey]
	h.mu.Unlock()
	if limit := h.lim.RepeatedFailureLimit; limit > 0 && failures >= limit {
		return fail(domain.Invalid("This exact %s call has failed %d times in this run. Change the approach, or ask a colleague or the owner for help.", call.Tool, failures))
	}
	result, err := h.dispatchTool(ctx, run, call)
	if err != nil {
		h.mu.Lock()
		if len(h.failedCalls) > 20000 {
			h.failedCalls = map[string]int{}
		}
		h.failedCalls[repeatKey]++
		h.mu.Unlock()
	}
	var rec protocol.ToolResult
	if err != nil {
		rec = fail(err)
	} else {
		b, merr := json.Marshal(result)
		if merr != nil {
			rec = fail(merr)
		} else {
			rec = protocol.ToolResult{CallID: call.CallID, OK: true, Result: b}
		}
	}
	if call.CallID != "" {
		body := string(rec.Result)
		if !rec.OK {
			b, _ := json.Marshal(rec.Error)
			body = string(b)
		}
		_ = h.st.Tx(ctx, func(tx *sqlTx) error {
			return store.InsertToolCall(ctx, tx, run.ID, call.CallID, call.Tool, rec.OK, body)
		})
	}
	return rec
}

// checkRunAccess verifies the run's engineer still belongs to its
// conversation and still holds the project access its mode needs.
func (h *Hub) checkRunAccess(ctx context.Context, q store.Q, run store.RunRow) error {
	ok, err := store.IsMember(ctx, q, run.Destination.RoomID, protocol.ActorEngineer, run.EngineerID)
	if err != nil {
		return err
	}
	if !ok {
		return domain.Forbidden("Your access to this conversation was removed; no further tool calls are allowed in this run.")
	}
	job, err := store.GetJob(ctx, q, run.JobID)
	if err != nil {
		return err
	}
	if job.ProjectID == "" || job.Kind == protocol.JobKindReply {
		return nil
	}
	g, err := store.GetGrant(ctx, q, job.ProjectID, run.EngineerID)
	if err != nil {
		return domain.Forbidden("Your access to this project was removed; no further tool calls are allowed in this run.")
	}
	if run.Mode == protocol.ModeEdit && job.Kind == protocol.JobKindCode && g.Access != "write" {
		return domain.Forbidden("Your write access to this project was removed; no further changes can be published in this run.")
	}
	return nil
}

func (h *Hub) loadEnv(ctx context.Context, q store.Q, run store.RunRow) (toolEnv, error) {
	job, err := store.GetJob(ctx, q, run.JobID)
	if err != nil {
		return toolEnv{}, err
	}
	eng, err := store.GetEngineer(ctx, q, run.EngineerID)
	if err != nil {
		return toolEnv{}, err
	}
	room, err := store.GetRoom(ctx, q, run.Destination.RoomID)
	if err != nil {
		return toolEnv{}, err
	}
	return toolEnv{run: run, job: job, eng: eng, room: room, me: protocol.Actor{Kind: protocol.ActorEngineer, ID: eng.ID}}, nil
}

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, domain.Invalid("Invalid arguments: %s", err.Error())
	}
	return v, nil
}

func (h *Hub) dispatchTool(ctx context.Context, run store.RunRow, call protocol.ToolCall) (any, error) {
	switch call.Tool {
	case bridge.RoomRead:
		a, err := decode[bridge.RoomReadArgs](call.Args)
		if err != nil {
			return nil, err
		}
		return h.toolRoomRead(ctx, run, a)
	case bridge.WorkStatus:
		a, err := decode[bridge.WorkStatusArgs](call.Args)
		if err != nil {
			return nil, err
		}
		return h.toolWorkStatus(ctx, run, a)
	case bridge.KnowledgeSearch:
		a, err := decode[bridge.KnowledgeSearchArgs](call.Args)
		if err != nil {
			return nil, err
		}
		return h.toolKnowledgeSearch(ctx, run, a)
	case bridge.ForgeReadPR:
		a, err := decode[bridge.ForgeReadPRArgs](call.Args)
		if err != nil {
			return nil, err
		}
		return h.toolForgeReadPR(ctx, run, a)
	case bridge.ForgeLinkPR:
		// Forge tools do network I/O, so they manage their own transactions
		// instead of holding the single writer during requests.
		a, err := decode[bridge.ForgeReadPRArgs](call.Args)
		if err != nil {
			return nil, err
		}
		env, err := h.loadEnv(ctx, h.st.R(), run)
		if err != nil {
			return nil, err
		}
		return h.toolForgeLinkPR(ctx, env, a)
	case bridge.ForgePublishReview:
		a, err := decode[bridge.ForgePublishReviewArgs](call.Args)
		if err != nil {
			return nil, err
		}
		env, err := h.loadEnv(ctx, h.st.R(), run)
		if err != nil {
			return nil, err
		}
		return h.toolForgePublishReview(ctx, env, a)
	}
	if call.Tool == bridge.WorkReview {
		// Re-read a PR target from the forge before the verdict is recorded.
		// The network call happens outside the writer transaction; a moved
		// head supersedes the round, which the verdict then sees.
		if _, round, err := store.ReviewByReviewJob(ctx, h.st.R(), run.JobID); err == nil && round.Target.Kind == "pr" {
			if pr, err := store.GetPR(ctx, h.st.R(), round.Target.PullRequestID); err == nil {
				if err := h.syncLinkedPR(ctx, pr); err != nil {
					h.log.Warn("could not re-read the pull request before a verdict", "pr", pr.ID, "err", err)
				}
			}
		}
	}
	// Mutating tools run in one transaction with their events.
	var out any
	mutate := func(t *txn) error {
		env, err := h.loadEnv(ctx, t.tx, run)
		if err != nil {
			return err
		}
		switch call.Tool {
		case bridge.RoomPost:
			a, err := decode[bridge.RoomPostArgs](call.Args)
			if err != nil {
				return err
			}
			out, err = h.toolRoomPost(ctx, t, env, a)
			return err
		case bridge.WorkCreate:
			a, err := decode[bridge.WorkCreateArgs](call.Args)
			if err != nil {
				return err
			}
			out, err = h.toolWorkCreate(ctx, t, env, a)
			return err
		case bridge.WorkUpdate:
			a, err := decode[bridge.WorkUpdateArgs](call.Args)
			if err != nil {
				return err
			}
			out, err = h.toolWorkUpdate(ctx, t, env, a)
			return err
		case bridge.WorkRequestHelp:
			a, err := decode[bridge.WorkRequestHelpArgs](call.Args)
			if err != nil {
				return err
			}
			out, err = h.toolRequestHelp(ctx, t, env, a)
			return err
		case bridge.WorkRespond:
			a, err := decode[bridge.WorkRespondArgs](call.Args)
			if err != nil {
				return err
			}
			out, err = h.toolRespond(ctx, t, env, a)
			return err
		case bridge.WorkRequestReview:
			a, err := decode[bridge.WorkRequestReviewArgs](call.Args)
			if err != nil {
				return err
			}
			out, err = h.toolRequestReview(ctx, t, env, a)
			return err
		case bridge.WorkReview:
			a, err := decode[bridge.WorkReviewArgs](call.Args)
			if err != nil {
				return err
			}
			out, err = h.toolReview(ctx, t, env, a)
			return err
		case bridge.WorkRespondReview:
			a, err := decode[bridge.WorkRespondReviewArgs](call.Args)
			if err != nil {
				return err
			}
			out, err = h.toolRespondReview(ctx, t, env, a)
			return err
		case bridge.WorkWait:
			a, err := decode[bridge.WorkWaitArgs](call.Args)
			if err != nil {
				return err
			}
			out, err = h.toolWait(ctx, t, env, a)
			return err
		case bridge.HumanAsk:
			a, err := decode[bridge.HumanAskArgs](call.Args)
			if err != nil {
				return err
			}
			out, err = h.toolHumanAsk(ctx, t, env, a)
			return err
		case bridge.DecisionPropose:
			a, err := decode[bridge.DecisionProposeArgs](call.Args)
			if err != nil {
				return err
			}
			out, err = h.toolDecisionPropose(ctx, t, env, a)
			return err
		case bridge.RecordCheck:
			var c protocol.CheckRecord
			if err := json.Unmarshal(call.Args, &c); err != nil {
				return domain.Invalid("bad check record")
			}
			out, err = h.recordCheck(ctx, t, env, c)
			return err
		case bridge.RecordRevision:
			var r protocol.RevisionRecord
			if err := json.Unmarshal(call.Args, &r); err != nil {
				return domain.Invalid("bad revision record")
			}
			out, err = h.recordRevision(ctx, t, env, r)
			return err
		case bridge.RecordArtifact:
			var a protocol.Artifact
			if err := json.Unmarshal(call.Args, &a); err != nil {
				return domain.Invalid("bad artifact record")
			}
			out, err = h.recordArtifact(ctx, t, env, a)
			return err
		}
		return domain.Invalid("Unknown tool %q.", call.Tool)
	}
	err := h.do(ctx, func(t *txn) error {
		// The lease and run state are re-checked under the writer lock: a
		// cancel or lease expiry that committed after the admission check
		// must stop the mutation.
		cur, err := requireLease(ctx, t.tx, run.NodeID, run.ID, run.LeaseEpoch)
		if err != nil {
			return domain.Forbidden("This run no longer holds a valid lease; the call was not executed.")
		}
		if cur.State == protocol.RunStopping || !domain.RunHoldsLease(cur.State) {
			return domain.Forbidden("This run is %s; no further tool calls are admitted.", cur.State)
		}
		if call.CallID != "" {
			// A retried call (for example after a reconnect) that already
			// committed returns its recorded result instead of running twice.
			if ok, res, found, err := store.GetToolCall(ctx, t.tx, run.ID, call.CallID); err != nil {
				return err
			} else if found && ok {
				out = json.RawMessage(res)
				return nil
			}
		}
		if err := mutate(t); err != nil {
			return err
		}
		if call.CallID == "" {
			return nil
		}
		b, err := json.Marshal(out)
		if err != nil {
			return err
		}
		// Recorded atomically with the mutation it describes.
		return store.InsertToolCall(ctx, t.tx, run.ID, call.CallID, call.Tool, true, string(b))
	})
	return out, err
}

// ---- read tools ----

type toolMessage struct {
	ID     string `json:"id"`
	Seq    int64  `json:"seq"`
	Author string `json:"author"`
	Role   string `json:"role,omitempty"`
	Kind   string `json:"kind"`
	At     string `json:"at"`
	Body   string `json:"body"`
}

func (h *Hub) toolRoomRead(ctx context.Context, run store.RunRow, a bridge.RoomReadArgs) (any, error) {
	q := h.st.R()
	if a.Limit <= 0 || a.Limit > 100 {
		a.Limit = 30
	}
	var msgs []protocol.Message
	var err error
	if run.Destination.ThreadID != "" {
		msgs, err = store.ListThread(ctx, q, run.Destination.ThreadID, "")
	} else {
		msgs, _, err = store.ListRoomMessages(ctx, q, run.Destination.RoomID, a.BeforeSeq, a.Limit, "")
	}
	if err != nil {
		return nil, err
	}
	names := h.actorNames(ctx, q)
	out := []toolMessage{}
	for _, m := range msgs {
		if m.DeletedAt != nil {
			continue
		}
		n := names[m.Author.Kind+":"+m.Author.ID]
		out = append(out, toolMessage{ID: m.ID, Seq: m.Seq, Author: n.name, Role: n.role, Kind: m.Kind, At: m.CreatedAt.Format("2006-01-02 15:04"), Body: m.Body})
	}
	return map[string]any{"room": run.Destination.RoomID, "thread": run.Destination.ThreadID, "messages": out}, nil
}

func (h *Hub) toolWorkStatus(ctx context.Context, run store.RunRow, a bridge.WorkStatusArgs) (any, error) {
	q := h.st.R()
	room, err := store.GetRoom(ctx, q, run.Destination.RoomID)
	if err != nil {
		return nil, err
	}
	rows, err := h.ledgerForRoom(ctx, q, room, a.IncludeCompleted)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, r := range rows {
		if a.Project != "" && !strings.EqualFold(r.ProjectName, a.Project) && r.Job.ProjectID != a.Project {
			continue
		}
		out = append(out, map[string]any{"jobId": r.Job.ID, "title": r.Job.Title, "owner": h.engineerName(ctx, q, r.Job.OwnerID),
			"state": r.Job.State, "waitingReason": r.Job.WaitingReason, "detail": r.Job.StateDetail, "project": r.ProjectName,
			"room": r.RoomName, "machine": r.NodeName, "lastConfirmed": r.LastConfirmed, "lastConfirmedAt": r.LastConfirmedAt})
	}
	return map[string]any{"work": out, "note": "Ledger facts visible from this conversation. Reading status never wakes other engineers."}, nil
}

func (h *Hub) toolKnowledgeSearch(ctx context.Context, run store.RunRow, a bridge.KnowledgeSearchArgs) (any, error) {
	q := h.st.R()
	room, err := store.GetRoom(ctx, q, run.Destination.RoomID)
	if err != nil {
		return nil, err
	}
	if a.Limit <= 0 || a.Limit > 25 {
		a.Limit = 10
	}
	ds, err := h.visibleDecisions(ctx, q, room, a.Query)
	if err != nil {
		return nil, err
	}
	var decisions []map[string]any
	for i, d := range ds {
		if i >= a.Limit {
			break
		}
		decisions = append(decisions, map[string]any{"id": d.ID, "title": d.Title, "body": d.Body, "scope": d.Scope, "sources": d.Sources})
	}
	// Messages: only the destination room, filtered inside the query.
	hits, err := store.SearchMessages(ctx, q, ftsQuery(a.Query), []string{room.ID}, a.Limit)
	if err != nil {
		hits = nil
	}
	return map[string]any{"decisions": decisions, "messages": hits}, nil
}

// ---- conversation tools ----

var handleRe = regexp.MustCompile(`@([a-z0-9][a-z0-9-]*)`)

// resolveTextMentions converts @handles in an engineer's text into
// structured mentions of room members, for rendering and notification only.
// Engineer-authored mentions never wake anyone.
func (h *Hub) resolveTextMentions(ctx context.Context, q store.Q, roomID, text string) []protocol.Mention {
	var out []protocol.Mention
	seen := map[string]bool{}
	for _, m := range handleRe.FindAllStringSubmatch(strings.ToLower(text), -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		if e, err := store.GetEngineerByHandle(ctx, q, m[1]); err == nil {
			if ok, _ := store.IsMember(ctx, q, roomID, protocol.ActorEngineer, e.ID); ok {
				out = append(out, protocol.Mention{Kind: protocol.ActorEngineer, ID: e.ID})
			}
			continue
		}
		if u, err := store.GetUserByHandle(ctx, q, m[1]); err == nil {
			if ok, _ := store.IsMember(ctx, q, roomID, protocol.ActorUser, u.ID); ok {
				out = append(out, protocol.Mention{Kind: protocol.ActorUser, ID: u.ID})
			}
		}
	}
	return out
}

func (h *Hub) mentionsFromHandles(ctx context.Context, q store.Q, roomID string, handles []string) ([]protocol.Mention, error) {
	var text strings.Builder
	for _, hd := range handles {
		hd = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(hd)), "@")
		text.WriteString(" @" + hd)
	}
	ms := h.resolveTextMentions(ctx, q, roomID, text.String())
	if len(ms) < len(handles) {
		return nil, domain.Invalid("You can only mention members of this conversation.")
	}
	return ms, nil
}

func (h *Hub) toolRoomPost(ctx context.Context, t *txn, env toolEnv, a bridge.RoomPostArgs) (any, error) {
	body := strings.TrimSpace(a.Body)
	if body == "" {
		return nil, domain.Invalid("Write something to post.")
	}
	mentions, err := h.mentionsFromHandles(ctx, t.tx, env.room.ID, a.Mentions)
	if err != nil {
		return nil, err
	}
	for _, m := range h.resolveTextMentions(ctx, t.tx, env.room.ID, body) {
		dup := false
		for _, x := range mentions {
			if x == m {
				dup = true
			}
		}
		if !dup {
			mentions = append(mentions, m)
		}
	}
	msg, err := t.postMessage(newMessage{Room: env.run.Destination.RoomID, Thread: env.run.Destination.ThreadID, Author: env.me, Body: body,
		Mentions: mentions, RunID: env.run.ID, JobID: env.job.ID, Cause: env.run.CauseID, Root: env.run.RootRequestID,
		Refs: jobRefFor(env.job)})
	if err != nil {
		return nil, err
	}
	if err := store.SetRunPostedReply(ctx, t.tx, env.run.ID); err != nil {
		return nil, err
	}
	return map[string]any{"messageId": msg.ID, "note": "Posted. Mentions do not wake anyone."}, nil
}

func jobRefFor(j store.JobRow) []protocol.Ref {
	if j.Kind == protocol.JobKindReply {
		return nil
	}
	return []protocol.Ref{{Kind: "job", ID: j.ID}}
}

// resolveEngineer finds a colleague by handle or ID.
func (h *Hub) resolveEngineer(ctx context.Context, q store.Q, ref string) (protocol.Engineer, error) {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "@")
	if domain.ValidID(ref) {
		if e, err := store.GetEngineer(ctx, q, ref); err == nil {
			return e, nil
		}
	}
	e, err := store.GetEngineerByHandle(ctx, q, strings.ToLower(ref))
	if err != nil {
		return e, domain.Invalid("No colleague with handle @%s.", ref)
	}
	if e.Archived {
		return e, domain.Invalid("%s is archived.", e.Name)
	}
	return e, nil
}

// resolveScope picks the project and repository for new work from the
// conversation's linked projects. Ambiguity is returned, never guessed.
func (h *Hub) resolveScope(ctx context.Context, q store.Q, env toolEnv, projectRef, repoRef, kind string) (protocol.Project, *protocol.Repo, error) {
	candidates := append([]string{}, env.room.ProjectIDs...)
	if env.job.ProjectID != "" && !contains(candidates, env.job.ProjectID) {
		candidates = append(candidates, env.job.ProjectID)
	}
	var projects []protocol.Project
	for _, id := range candidates {
		if p, err := store.GetProject(ctx, q, id); err == nil {
			projects = append(projects, p)
		}
	}
	var chosen []protocol.Project
	for _, p := range projects {
		if projectRef == "" || strings.EqualFold(p.Name, projectRef) || p.ID == projectRef {
			chosen = append(chosen, p)
		}
	}
	if projectRef == "" && repoRef != "" {
		chosen = nil
		for _, p := range projects {
			for _, r := range p.Repos {
				if strings.EqualFold(r.Name, repoRef) || r.ID == repoRef {
					chosen = append(chosen, p)
				}
			}
		}
	}
	names := func(ps []protocol.Project) string {
		var n []string
		for _, p := range ps {
			n = append(n, p.Name)
		}
		return strings.Join(n, ", ")
	}
	if len(chosen) == 0 {
		if kind != protocol.JobKindCode && projectRef == "" && repoRef == "" {
			return protocol.Project{}, nil, nil
		}
		if len(projects) == 0 {
			return protocol.Project{}, nil, domain.Invalid("No projects are linked to this conversation. Ask the owner to link one.")
		}
		return protocol.Project{}, nil, domain.Invalid("No linked project matches. Projects here: %s.", names(projects))
	}
	if len(chosen) > 1 {
		return protocol.Project{}, nil, domain.Invalid("Several projects match (%s). Specify one.", names(chosen))
	}
	p := chosen[0]
	var repos []protocol.Repo
	for _, r := range p.Repos {
		if repoRef == "" || strings.EqualFold(r.Name, repoRef) || r.ID == repoRef {
			repos = append(repos, r)
		}
	}
	if len(repos) == 0 {
		if kind == protocol.JobKindCode {
			return p, nil, domain.Invalid("%s has no matching repository. Code work needs exactly one repository.", p.Name)
		}
		return p, nil, nil
	}
	if len(repos) > 1 {
		var n []string
		for _, r := range repos {
			n = append(n, r.Name)
		}
		if kind == protocol.JobKindCode {
			return p, nil, domain.Invalid("%s has several repositories (%s). Each code job needs exactly one; name it, or split the work into linked jobs.", p.Name, strings.Join(n, ", "))
		}
		return p, nil, nil
	}
	return p, &repos[0], nil
}

func (h *Hub) toolWorkCreate(ctx context.Context, t *txn, env toolEnv, a bridge.WorkCreateArgs) (any, error) {
	if !contains([]string{protocol.JobKindCode, protocol.JobKindInvestigation, protocol.JobKindDocument}, a.Kind) {
		return nil, domain.Invalid("kind must be code, investigation, or document.")
	}
	if strings.TrimSpace(a.Objective) == "" || strings.TrimSpace(a.Title) == "" {
		return nil, domain.Invalid("Work needs a title and an objective.")
	}
	owner := env.eng
	delegated := false
	if a.Owner != "" && !strings.EqualFold(strings.TrimPrefix(a.Owner, "@"), env.eng.Handle) {
		o, err := h.resolveEngineer(ctx, t.tx, a.Owner)
		if err != nil {
			return nil, err
		}
		// A delegate reads this conversation, so they must already belong to it.
		if ok, _ := store.IsMember(ctx, t.tx, env.room.ID, protocol.ActorEngineer, o.ID); !ok {
			return nil, domain.Forbidden("%s isn't in this conversation, so you can't hand them work here. Ask the owner to invite them (they'll see what history becomes visible first).", o.Name)
		}
		owner, delegated = o, true
	}
	project, repo, err := h.resolveScope(ctx, t.tx, env, a.Project, a.Repo, a.Kind)
	if err != nil {
		return nil, err
	}
	if project.ID != "" {
		need := "read"
		if a.Kind == protocol.JobKindCode {
			need = "write"
		}
		g, err := store.GetGrant(ctx, t.tx, project.ID, owner.ID)
		if err != nil {
			return nil, domain.Forbidden("%s has no access to %s. Ask the owner to grant %s access.", owner.Name, project.Name, need)
		}
		if need == "write" && g.Access != "write" {
			return nil, domain.Forbidden("%s can read %s, but this task needs write access.", owner.Name, project.Name)
		}
	}
	spec := jobSpec{Kind: a.Kind, Title: a.Title, Objective: a.Objective, Acceptance: a.Acceptance, Owner: owner.ID,
		Root: env.run.RootRequestID, Source: env.job.Source, ProjectID: project.ID, Actor: env.me,
		RequiresPeerReview:  a.Kind == protocol.JobKindCode || project.Policy.RequirePeerReview,
		RequiresHumanReview: project.Policy.RequireHumanReview || a.RequireHumanReview, Depth: env.job.Depth}
	if repo != nil {
		spec.RepoID = repo.ID
	}
	// Peer review needs a colleague in this conversation. With nobody else
	// here at all (a direct message, or a workspace with one engineer) the
	// owner reviews it instead, visibly, so the work can still finish. A
	// colleague who is here but lacks access is not waived: the author asks
	// (A42) rather than anyone's access being widened.
	waived := false
	if spec.RequiresPeerReview && !h.hasColleagueInRoom(ctx, t.tx, env.job.Source.RoomID, owner.ID) {
		spec.RequiresPeerReview, spec.RequiresHumanReview, waived = false, true, true
	}
	if env.job.Kind != protocol.JobKindReply {
		p := env.job
		spec.Parent = &p
	}
	spec.Follows = h.followTarget(ctx, t.tx, env.job)
	if delegated {
		spec.Depth = env.job.Depth + 1
		if env.job.Kind == protocol.JobKindReply {
			p := env.job
			spec.Parent = nil
			_ = p
		}
	}
	job, err := h.createJob(ctx, t, spec)
	if err != nil {
		return nil, err
	}
	if delegated {
		if _, err := t.postMessage(newMessage{Room: env.run.Destination.RoomID, Thread: env.run.Destination.ThreadID, Author: env.me,
			Body: fmt.Sprintf("@%s I've handed you: %s", owner.Handle, job.Title), Mentions: []protocol.Mention{{Kind: protocol.ActorEngineer, ID: owner.ID}},
			Refs: []protocol.Ref{{Kind: "job", ID: job.ID}}, JobID: job.ID, RunID: env.run.ID, Root: env.run.RootRequestID}); err != nil {
			return nil, err
		}
	}
	if _, err := h.enqueueRun(ctx, t, job, runReason{Purpose: "start", Cause: env.run.ID, Automatic: true}); err != nil {
		return nil, err
	}
	res := map[string]any{"jobId": job.ID, "owner": owner.Name, "state": "queued", "project": project.Name,
		"peerReviewRequired": job.RequiresPeerReview, "humanReviewRequired": job.RequiresHumanReview}
	if waived {
		res["reviewNote"] = "No other engineer is in this conversation to review this, so the owner reviews it instead: finish it with evidence and it waits for their acceptance. Do not request a peer review."
	}
	if repo != nil {
		res["repo"] = repo.Name
	}
	if owner.ID == env.eng.ID && env.job.Kind == protocol.JobKindReply {
		res["note"] = "The job starts after this reply ends. Reply briefly with what you'll do."
	}
	return res, nil
}

func (h *Hub) toolWorkUpdate(ctx context.Context, t *txn, env toolEnv, a bridge.WorkUpdateArgs) (any, error) {
	if env.job.OwnerID != env.eng.ID {
		return nil, domain.Forbidden("Only the job's owner can update it.")
	}
	if a.Progress != "" {
		if err := store.SetJobActivity(ctx, t.tx, env.job.ID, truncate(a.Progress, 160), h.now()); err != nil {
			return nil, err
		}
		if err := store.SetRunActivity(ctx, t.tx, env.run.ID, truncate(a.Progress, 160), h.now()); err != nil {
			return nil, err
		}
		if _, err := t.jobChanged(ctx, env.job.ID); err != nil {
			return nil, err
		}
	}
	switch a.State {
	case "":
		return map[string]any{"ok": true}, nil
	case "completed":
		if env.job.Kind == protocol.JobKindReply {
			return map[string]any{"ok": true, "note": "Conversational replies complete when your turn ends."}, nil
		}
		msg, err := h.requestCompletion(ctx, t, env.job, a.Summary)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "result": msg}, nil
	case "failed":
		if env.job.Kind == protocol.JobKindReply {
			return map[string]any{"ok": true, "note": "A conversational reply can't fail as work; explain the limitation in your reply."}, nil
		}
		if strings.TrimSpace(a.Summary) == "" {
			return nil, domain.Invalid("Explain why the objective can't be met.")
		}
		if err := store.SetJobCompletion(ctx, t.tx, env.job.ID, false, a.Summary); err != nil {
			return nil, err
		}
		if _, err := h.setJobState(ctx, t, env.job.ID, protocol.JobFailed, "", truncate(a.Summary, 300)); err != nil {
			return nil, err
		}
		if _, err := t.postMessage(newMessage{Room: env.job.Source.RoomID, Thread: env.job.Source.ThreadID, Author: env.me,
			Kind: protocol.MessageResult, Body: a.Summary, Refs: []protocol.Ref{{Kind: "job", ID: env.job.ID}}, JobID: env.job.ID}); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "state": "failed"}, nil
	}
	return nil, domain.Invalid("state must be completed or failed.")
}

func (h *Hub) toolRequestHelp(ctx context.Context, t *txn, env toolEnv, a bridge.WorkRequestHelpArgs) (any, error) {
	to, err := h.resolveEngineer(ctx, t.tx, a.To)
	if err != nil {
		return nil, err
	}
	if to.ID == env.eng.ID {
		return nil, domain.Invalid("You can't ask yourself for help.")
	}
	if ok, _ := store.IsMember(ctx, t.tx, env.room.ID, protocol.ActorEngineer, to.ID); !ok {
		return nil, domain.Forbidden("%s isn't in this conversation. Ask the owner to invite them, or ask here.", to.Name)
	}
	if env.job.Kind == protocol.JobKindReply {
		return nil, domain.Invalid("Create work with work_create first, or answer directly.")
	}
	// Reject dependency cycles: the helper must not already be waiting on us.
	ancestors, _ := store.JobAncestors(ctx, t.tx, env.job.ID)
	for _, aid := range append(ancestors, env.job.ID) {
		if j, err := store.GetJob(ctx, t.tx, aid); err == nil && j.OwnerID == to.ID && j.Kind != protocol.JobKindReview {
			return nil, domain.Invalid("%s is already waiting on this work; asking them would create a cycle. Answer from available evidence or ask in the room.", to.Name)
		}
	}
	objective := strings.TrimSpace(a.Question)
	if a.ExpectedOutput != "" {
		objective += "\n\nExpected output: " + a.ExpectedOutput
	}
	if a.Evidence != "" {
		objective += "\n\nContext: " + a.Evidence
	}
	p := env.job
	job, err := h.createJob(ctx, t, jobSpec{Kind: protocol.JobKindInvestigation, Title: "Help " + env.eng.Name + ": " + truncate(firstLine(a.Question), 70),
		Objective: objective, Owner: to.ID, Parent: &p, Source: env.job.Source, ProjectID: env.job.ProjectID, Depth: env.job.Depth + 1, Actor: env.me})
	if err != nil {
		return nil, err
	}
	if err := store.MarkHelpRequest(ctx, t.tx, job.ID); err != nil {
		return nil, err
	}
	if _, err := t.postMessage(newMessage{Room: env.run.Destination.RoomID, Thread: env.run.Destination.ThreadID, Author: env.me,
		Body: "@" + to.Handle + " " + strings.TrimSpace(a.Question), Mentions: []protocol.Mention{{Kind: protocol.ActorEngineer, ID: to.ID}},
		Refs: []protocol.Ref{{Kind: "job", ID: job.ID}}, JobID: job.ID, RunID: env.run.ID, Root: env.run.RootRequestID}); err != nil {
		return nil, err
	}
	if _, err := h.enqueueRun(ctx, t, job, runReason{Purpose: "help", Cause: env.run.ID, Automatic: true}); err != nil {
		return nil, err
	}
	return map[string]any{"requestId": job.ID, "to": to.Name,
		"note": "Asked. Continue independent work; if you can't progress without the answer, call work_wait {reason:'dependency'} and end your turn."}, nil
}

func (h *Hub) toolRespond(ctx context.Context, t *txn, env toolEnv, a bridge.WorkRespondArgs) (any, error) {
	req, err := store.GetJob(ctx, t.tx, a.RequestID)
	if err != nil || req.ParentID == "" || !req.HelpRequest {
		return nil, domain.Invalid("No help request %s. work_respond only answers a colleague's help request; complete other work with work_update.", a.RequestID)
	}
	// Only the helper's own run for that request can answer it.
	if req.OwnerID != env.eng.ID || env.job.ID != req.ID {
		return nil, domain.Forbidden("Answer a help request from its own run.")
	}
	if !domain.JobLive(req.State) {
		return map[string]any{"ok": true, "note": "That request was already resolved."}, nil
	}
	parent, err := store.GetJob(ctx, t.tx, req.ParentID)
	if err != nil {
		return nil, err
	}
	asker, _ := store.GetEngineer(ctx, t.tx, parent.OwnerID)
	body := strings.TrimSpace(a.Body)
	if body == "" {
		return nil, domain.Invalid("Write the answer.")
	}
	if err := store.SetJobCompletion(ctx, t.tx, req.ID, false, body); err != nil {
		return nil, err
	}
	if _, err := t.postMessage(newMessage{Room: req.Source.RoomID, Thread: req.Source.ThreadID, Author: env.me,
		Body: "@" + asker.Handle + " " + body, Mentions: []protocol.Mention{{Kind: protocol.ActorEngineer, ID: asker.ID}},
		Refs: []protocol.Ref{{Kind: "job", ID: req.ID}}, JobID: req.ID, RunID: env.run.ID}); err != nil {
		return nil, err
	}
	if _, err := h.setJobState(ctx, t, req.ID, protocol.JobCompleted, "", ""); err != nil {
		return nil, err
	}
	req, _ = store.GetJob(ctx, t.tx, req.ID)
	if err := h.resolveJobDependency(ctx, t, req); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "note": "Answered; " + asker.Name + " will pick it up."}, nil
}

func (h *Hub) toolWait(ctx context.Context, t *txn, env toolEnv, a bridge.WorkWaitArgs) (any, error) {
	reason := map[string]string{"review": "review", "missing_information": protocol.WaitMissingInfo, "dependency": protocol.WaitDependency,
		"approval": protocol.WaitApproval}[a.Reason]
	if reason == "" {
		return nil, domain.Invalid("reason must be review, missing_information, dependency, or approval.")
	}
	if env.job.Kind == protocol.JobKindReply {
		return map[string]any{"ok": true, "note": "Replies don't wait; just finish your turn."}, nil
	}
	if err := store.SetRunIntent(ctx, t.tx, env.run.ID, reason, truncate(a.Detail, 300)); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "note": "Recorded. End your turn now; your capacity is released and you'll be resumed automatically when it resolves."}, nil
}

// ---- internal records from the runner ----

func (h *Hub) recordCheck(ctx context.Context, t *txn, env toolEnv, c protocol.CheckRecord) (any, error) {
	if strings.TrimSpace(c.Command) == "" {
		return nil, domain.Invalid("A check needs its command.")
	}
	check := protocol.Check{ID: domain.NewID(), JobID: env.job.ID, RunID: env.run.ID, NodeID: env.run.NodeID, Name: firstNonEmpty(c.Name, c.Command),
		Command: c.Command, ExitCode: c.ExitCode, Passed: c.ExitCode == 0, Revision: c.Revision, LogArtifactID: c.LogArtifactID,
		DurationMs: c.DurationMs, Summary: truncate(c.Summary, 500), CreatedAt: h.now()}
	if c.LogArtifactID != "" {
		if _, err := store.GetArtifact(ctx, t.tx, c.LogArtifactID); err != nil {
			check.LogArtifactID = ""
		}
	}
	if err := store.InsertCheck(ctx, t.tx, check); err != nil {
		return nil, err
	}
	status := "passed"
	if !check.Passed {
		status = fmt.Sprintf("failed (exit %d)", c.ExitCode)
	}
	text := "Ran " + check.Name + ": " + status
	if err := store.SetJobActivity(ctx, t.tx, env.job.ID, truncate(text, 160), h.now()); err != nil {
		return nil, err
	}
	if err := t.emit(ev{Type: "check.recorded", Actor: env.me, Room: env.job.Source.RoomID, Job: env.job.ID, Run: env.run.ID, Payload: check}); err != nil {
		return nil, err
	}
	if _, err := t.jobChanged(ctx, env.job.ID); err != nil {
		return nil, err
	}
	return check, nil
}

func (h *Hub) recordRevision(ctx context.Context, t *txn, env toolEnv, r protocol.RevisionRecord) (any, error) {
	if env.run.Mode != protocol.ModeEdit || env.job.RepoID == "" || r.RepoID != env.job.RepoID {
		return nil, domain.Forbidden("This run can't publish revisions for that repository.")
	}
	if r.Head == "" || r.Base == "" {
		return nil, domain.Invalid("A revision needs base and head.")
	}
	if r.DiffArtifactID != "" {
		if _, err := store.GetArtifact(ctx, t.tx, r.DiffArtifactID); err != nil {
			return nil, domain.Invalid("The diff artifact was not verified by the hub.")
		}
	}
	if err := store.SetJobRevision(ctx, t.tx, env.job.ID, r.Branch, r.Base, r.Head, r.DiffArtifactID); err != nil {
		return nil, err
	}
	if err := store.SetRunRevision(ctx, t.tx, env.run.ID, r.Branch, r.Base, r.Head); err != nil {
		return nil, err
	}
	text := fmt.Sprintf("Published %s (%d files, +%d −%d)", shortRev(r.Head), r.FilesChanged, r.Insertions, r.Deletions)
	if err := store.SetJobActivity(ctx, t.tx, env.job.ID, text, h.now()); err != nil {
		return nil, err
	}
	if err := t.emit(ev{Type: "revision.published", Actor: env.me, Room: env.job.Source.RoomID, Job: env.job.ID, Run: env.run.ID, Payload: r}); err != nil {
		return nil, err
	}
	// A published revision changes what "current" means: earlier approvals
	// no longer apply to the new head.
	if _, err := t.jobChanged(ctx, env.job.ID); err != nil {
		return nil, err
	}
	return map[string]any{"head": r.Head, "base": r.Base, "branch": r.Branch, "filesChanged": r.FilesChanged,
		"note": "Published. Reviews and checks apply to this exact revision."}, nil
}

func (h *Hub) recordArtifact(ctx context.Context, t *txn, env toolEnv, a protocol.Artifact) (any, error) {
	if !h.artifacts.Has(a.Hash) {
		return nil, domain.Invalid("The artifact content was not uploaded or failed verification.")
	}
	a.ID = domain.NewID()
	a.RunID, a.JobID, a.RoomID, a.ProjectID = env.run.ID, env.job.ID, env.job.Source.RoomID, env.job.ProjectID
	a.CreatedAt = h.now()
	if a.Kind == "" {
		a.Kind = "file"
	}
	if !contains([]string{"diff", "log", "file", "checkpoint", "document", "bundle"}, a.Kind) {
		return nil, domain.Invalid("Unknown artifact kind.")
	}
	a.Name = truncate(a.Name, 200)
	if err := store.InsertArtifact(ctx, t.tx, h.Org().ID, a); err != nil {
		return nil, err
	}
	if a.Kind == "document" || a.Kind == "file" {
		if err := t.emit(ev{Type: "artifact.published", Actor: env.me, Room: env.job.Source.RoomID, Job: env.job.ID, Run: env.run.ID, Payload: a}); err != nil {
			return nil, err
		}
		if _, err := t.jobChanged(ctx, env.job.ID); err != nil {
			return nil, err
		}
	}
	return a, nil
}

var errNoForge = errors.New("no forge")

// hasColleagueInRoom reports whether another active engineer belongs to the room.
func (h *Hub) hasColleagueInRoom(ctx context.Context, q store.Q, roomID, ownerID string) bool {
	room, err := store.GetRoom(ctx, q, roomID)
	if err != nil {
		return true // unknown: keep the stricter default
	}
	for _, m := range room.Members {
		if m.Kind != protocol.ActorEngineer || m.ID == ownerID {
			continue
		}
		if e, err := store.GetEngineer(ctx, q, m.ID); err == nil && !e.Archived {
			return true
		}
	}
	return false
}
