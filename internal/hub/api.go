package hub

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

// AllowSetupAttempt rate-limits owner setup attempts per client.
func (h *Hub) AllowSetupAttempt(client string) bool { return h.logins.Allow("setup|" + client) }

// Bootstrap returns everything the client needs to render and a replay cursor.
func (h *Hub) Bootstrap(ctx context.Context, u store.UserRow, s store.Session) (protocol.Bootstrap, error) {
	q := h.st.R()
	b := protocol.Bootstrap{User: u.User, Org: h.Org(), CSRFToken: s.CSRFToken, Preferences: u.Preferences, ServerTime: h.now(),
		Version: h.cfg.Version, Demo: h.cfg.Demo}
	var err error
	// The cursor is read first so no event committed after the snapshot is missed.
	if b.Cursor, err = store.MaxEventSeq(ctx, q); err != nil {
		return b, err
	}
	if b.Rooms, err = store.ListRoomsForUser(ctx, q, u.ID); err != nil {
		return b, err
	}
	if b.Engineers, err = store.ListEngineers(ctx, q); err != nil {
		return b, err
	}
	if b.Projects, err = store.ListProjects(ctx, q); err != nil {
		return b, err
	}
	if b.Nodes, err = h.ListNodes(ctx); err != nil {
		return b, err
	}
	b.Providers = h.providerSummary(b.Nodes)
	if b.Preferences.Theme == "" {
		b.Preferences.Theme = "system"
	}
	if b.Preferences.SendKey == "" {
		b.Preferences.SendKey = "enter"
	}
	return b, nil
}

func (h *Hub) providerSummary(nodes []protocol.Node) []protocol.ProviderSummary {
	order := []string{"codex", "claude", "cursor", "fake"}
	by := map[string]*protocol.ProviderSummary{}
	for _, p := range order {
		by[p] = &protocol.ProviderSummary{Provider: p, Label: ProviderLabel(p), ReadyNodes: []string{}, Billing: protocol.BillingUnknown, Fake: p == "fake"}
	}
	for _, n := range nodes {
		for _, inst := range n.Providers {
			s := by[inst.Provider]
			if s == nil {
				continue
			}
			if inst.AuthState == protocol.AuthReady && n.RevokedAt == nil {
				s.ReadyNodes = append(s.ReadyNodes, n.ID)
				if inst.Billing != "" {
					s.Billing = inst.Billing
				}
			}
		}
	}
	var out []protocol.ProviderSummary
	for _, p := range order {
		if p == "fake" && len(by[p].ReadyNodes) == 0 && !h.cfg.Demo {
			continue
		}
		out = append(out, *by[p])
	}
	return out
}

// RoomWork returns live work attached to a room (for its work strip).
// includeReplies adds conversational replies still queued or running.
func (h *Hub) RoomWork(ctx context.Context, userID, roomID string, includeReplies bool) ([]protocol.WorkRow, error) {
	room, err := h.requireRoom(ctx, h.st.R(), userID, roomID)
	if err != nil {
		return nil, err
	}
	rows, err := h.ledgerForRoom(ctx, h.st.R(), room, true)
	if err != nil {
		return nil, err
	}
	if includeReplies {
		replies, err := store.ListJobs(ctx, h.st.R(), store.JobFilter{IncludeReply: true, RoomIDs: []string{roomID},
			States: []protocol.JobState{protocol.JobQueued, protocol.JobRunning, protocol.JobWaiting}})
		if err != nil {
			return nil, err
		}
		for _, j := range replies {
			if j.Kind == protocol.JobKindReply {
				rows = append(rows, h.workRow(ctx, h.st.R(), j))
			}
		}
	}
	var out []protocol.WorkRow
	for _, r := range rows {
		if r.Job.Source.RoomID != roomID {
			continue
		}
		if r.Job.State == protocol.JobCompleted && r.Job.CompletedAt != nil && h.now().Sub(*r.Job.CompletedAt) > 24*time.Hour {
			continue
		}
		out = append(out, r)
	}
	return nonNil(out), nil
}

// RunActivity returns a run's recorded tool and progress log.
func (h *Hub) RunActivity(ctx context.Context, userID, jobID, runID string) ([]protocol.RunActivity, error) {
	job, err := store.GetJob(ctx, h.st.R(), jobID)
	if err != nil {
		return nil, domain.NotFound("That work doesn't exist.")
	}
	if _, err := h.requireRoom(ctx, h.st.R(), userID, job.Source.RoomID); err != nil {
		return nil, err
	}
	run, err := store.GetRun(ctx, h.st.R(), runID)
	if err != nil || run.JobID != jobID {
		return nil, domain.NotFound("That run doesn't exist.")
	}
	return store.ListRunEvents(ctx, h.st.R(), runID, 2000)
}

// OpenArtifact checks current access before serving an artifact, even if a
// message used to contain a valid link.
func (h *Hub) OpenArtifact(ctx context.Context, userID, id string) (protocol.Artifact, *os.File, error) {
	a, err := store.GetArtifact(ctx, h.st.R(), id)
	if err != nil {
		return a, nil, domain.NotFound("That artifact doesn't exist.")
	}
	if a.RoomID != "" {
		if _, err := h.requireRoom(ctx, h.st.R(), userID, a.RoomID); err != nil {
			return a, nil, domain.NotFound("That artifact doesn't exist.")
		}
	}
	f, err := h.artifacts.Open(a.Hash)
	if err != nil {
		return a, nil, domain.Unavailable("artifact_store", "The artifact's content is missing from the hub's store.")
	}
	return a, f, nil
}

// AuthorizeNode verifies a machine certificate against the hub's current
// record on every runner HTTP request: a revoked or superseded credential
// is refused even though its certificate still chains to the hub CA.
func (h *Hub) AuthorizeNode(ctx context.Context, nodeID, serial string) error {
	node, err := store.GetNode(ctx, h.st.R(), nodeID)
	if err != nil {
		return domain.Unauthorized("Unknown machine.")
	}
	if node.RevokedAt != nil {
		return domain.Unauthorized("This machine's credential was revoked. Pair it again to reconnect.")
	}
	if node.CertSerial != serial {
		return domain.Unauthorized("This machine presented a superseded certificate.")
	}
	return nil
}

// leaseStatesSQL lists the run states in which a machine holds a run's lease.
const leaseStatesSQL = `('offered','preparing','running','awaiting_input','stopping')`

// OpenArtifactForNode serves a code bundle or checkpoint to a runner, but
// only one named in the manifest of a run currently assigned to it.
func (h *Hub) OpenArtifactForNode(ctx context.Context, nodeID, id string) (protocol.Artifact, *os.File, error) {
	a, err := store.GetArtifact(ctx, h.st.R(), id)
	if err != nil {
		return a, nil, domain.NotFound("unknown artifact")
	}
	if a.Kind != "bundle" && a.Kind != "checkpoint" && a.Kind != "source_bundle" && a.Kind != "document" && a.Kind != "file" {
		return a, nil, domain.Forbidden("runners may only fetch bundles, checkpoints and assigned review artifacts")
	}
	var n int
	query, arg := `SELECT COUNT(*) FROM runs WHERE node_id = ? AND state IN `+leaseStatesSQL+` AND instr(manifest, ?) > 0`, `"`+a.ID+`"`
	if a.Kind == "document" || a.Kind == "file" {
		query = `SELECT COUNT(*) FROM runs r JOIN review_rounds rr ON rr.review_job_id = r.job_id
			WHERE r.node_id = ? AND r.state IN ` + leaseStatesSQL + ` AND rr.artifact_id = ? AND rr.superseded_by IS NULL`
		arg = a.ID
	}
	if a.Kind == "source_bundle" {
		// An imported repository's bundle: only while holding a run for work in that repository.
		query = `SELECT COUNT(*) FROM runs r JOIN jobs j ON j.id = r.job_id JOIN repos rp ON rp.id = j.repo_id
			WHERE r.node_id = ? AND r.state IN ` + leaseStatesSQL + ` AND rp.source_bundle_id = ?`
		arg = a.ID
	}
	if err := h.st.R().QueryRowContext(ctx, query, nodeID, arg).Scan(&n); err != nil {
		return a, nil, err
	}
	if n == 0 {
		return a, nil, domain.NotFound("unknown artifact")
	}
	f, err := h.artifacts.Open(a.Hash)
	return a, f, err
}

// NodeHoldsRun reports whether the machine currently holds any run lease
// (artifact uploads are only accepted on behalf of a live run).
func (h *Hub) NodeHoldsRun(ctx context.Context, nodeID string) bool {
	var n int
	_ = h.st.R().QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE node_id = ? AND state IN `+leaseStatesSQL, nodeID).Scan(&n)
	return n > 0
}

// Export writes the user's visible data in documented JSON formats.
func (h *Hub) Export(ctx context.Context, userID string, writeJSON func(string, any) error, writeFile func(string, io.Reader) error) error {
	q := h.st.R()
	rooms, err := store.ListRoomsForUser(ctx, q, userID)
	if err != nil {
		return err
	}
	if err := writeJSON("README.json", map[string]any{
		"format": "yip export v1", "exportedAt": h.now(), "org": h.Org(),
		"files": map[string]string{
			"rooms.json": "rooms visible to the owner", "messages/<room>.json": "full message history per room",
			"jobs.json": "work ledger with runs, checks, reviews", "decisions.json": "decisions with sources",
			"artifacts.json": "artifact manifest (sha256, size, kind)", "artifacts/<sha256>": "artifact content",
		}}); err != nil {
		return err
	}
	if err := writeJSON("rooms.json", rooms); err != nil {
		return err
	}
	var roomIDs []string
	for _, r := range rooms {
		roomIDs = append(roomIDs, r.ID)
		var all []protocol.Message
		before := int64(0)
		for {
			page, more, err := store.ListRoomMessages(ctx, q, r.ID, before, 500, userID)
			if err != nil {
				return err
			}
			for _, m := range page {
				if m.Thread != nil {
					replies, _ := store.ListThread(ctx, q, m.ID, userID)
					if len(replies) > 1 {
						all = append(all, replies[1:]...)
					}
				}
			}
			all = append(page, all...)
			if !more || len(page) == 0 {
				break
			}
			before = page[0].Seq
		}
		if err := writeJSON("messages/"+r.ID+".json", all); err != nil {
			return err
		}
	}
	jobs, _ := store.ListJobs(ctx, q, store.JobFilter{RoomIDs: roomIDs, IncludeReply: true})
	var details []protocol.JobDetail
	for _, j := range jobs {
		if d, err := h.JobDetail(ctx, userID, j.ID); err == nil {
			details = append(details, d)
		}
	}
	if err := writeJSON("jobs.json", details); err != nil {
		return err
	}
	ds, _ := h.ListDecisions(ctx, userID, "")
	if err := writeJSON("decisions.json", ds); err != nil {
		return err
	}
	arts, _ := store.ListArtifacts(ctx, q)
	var visible []protocol.Artifact
	for _, a := range arts {
		if a.RoomID != "" && !contains(roomIDs, a.RoomID) {
			continue
		}
		visible = append(visible, a)
		if f, err := h.artifacts.Open(a.Hash); err == nil {
			_ = writeFile("artifacts/"+a.Hash, f)
			f.Close()
		}
	}
	return writeJSON("artifacts.json", visible)
}

// ActiveRuns lists attempts that are queued or executing in rooms the user
// belongs to (for "working" indicators).
func (h *Hub) ActiveRuns(ctx context.Context, userID string) ([]protocol.Run, error) {
	rooms, err := store.RoomIDsForMember(ctx, h.st.R(), protocol.ActorUser, userID)
	if err != nil {
		return nil, err
	}
	runs, err := store.RunsInStates(ctx, h.st.R(), protocol.RunCreated, protocol.RunOffered, protocol.RunPreparing, protocol.RunRunning,
		protocol.RunAwaitingInput, protocol.RunStopping, protocol.RunUnknown)
	if err != nil {
		return nil, err
	}
	out := []protocol.Run{}
	for _, r := range runs {
		if contains(rooms, r.Destination.RoomID) {
			out = append(out, r.Run)
		}
	}
	return out, nil
}

// GetQuestion returns one question in a room the user belongs to.
func (h *Hub) GetQuestion(ctx context.Context, userID, id string) (protocol.Question, error) {
	q, err := store.GetQuestion(ctx, h.st.R(), id)
	if err != nil {
		return q, domain.NotFound("That question doesn't exist.")
	}
	if _, err := h.requireRoom(ctx, h.st.R(), userID, q.Source.RoomID); err != nil {
		return q, domain.NotFound("That question doesn't exist.")
	}
	return q, nil
}

// GetDecision returns one decision the user may see.
func (h *Hub) GetDecision(ctx context.Context, userID, id string) (protocol.Decision, error) {
	d, err := store.GetDecision(ctx, h.st.R(), id)
	if err != nil {
		return d, domain.NotFound("That decision doesn't exist.")
	}
	if d.VisibleRoomIDs != nil {
		rooms, _ := store.RoomIDsForMember(ctx, h.st.R(), protocol.ActorUser, userID)
		if !anyIn(d.VisibleRoomIDs, rooms) {
			return d, domain.NotFound("That decision doesn't exist.")
		}
	}
	return d, nil
}

// MarkOverviewSeen records the owner's visit for "Since you were here".
func (h *Hub) MarkOverviewSeen(ctx context.Context, userID string) error {
	u, err := store.GetUser(ctx, h.st.R(), userID)
	if err != nil {
		return err
	}
	u.Preferences.LastSeenAt = h.now().Format(time.RFC3339)
	return h.SetPreferences(ctx, userID, u.Preferences)
}
