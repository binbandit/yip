package hub

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

const (
	// noteReviewAfter is how long a note stays current before it's due for
	// review (and left out of context until renewed).
	noteReviewAfter = 90 * 24 * time.Hour
	noteMaxChars    = 400
	// notesInContext bounds layer 5 of a run's context.
	notesInContext = 12
	// notesPerEngineer bounds how many current notes one engineer keeps.
	notesPerEngineer = 60
)

// noteVisibleIn reports whether a note may be used in (or shown from) room:
// its sources' visibility, and its scope (this room, or a project linked
// to it).
func noteVisibleIn(n protocol.EngineerNote, room protocol.Room) bool {
	if n.VisibleRoomIDs != nil && !contains(n.VisibleRoomIDs, room.ID) {
		return false
	}
	switch n.Scope.Kind {
	case "room":
		return n.Scope.ID == room.ID
	case "project":
		return contains(room.ProjectIDs, n.Scope.ID)
	}
	return false
}

// toolNoteRecord lets an engineer keep a short, sourced note from its work.
// Notes drawn only from the engineer's own finished work are accepted by
// policy; anything else stays a proposal on its profile (never a queue).
func (h *Hub) toolNoteRecord(ctx context.Context, t *txn, env toolEnv, a bridge.NoteRecordArgs) (any, error) {
	body := strings.TrimSpace(a.Body)
	if body == "" || len(a.Sources) == 0 && env.job.Kind == protocol.JobKindReply {
		return nil, domain.Invalid("A note needs a body and at least one source (a message or job ID) it comes from.")
	}
	if utf8.RuneCountInString(body) > noteMaxChars {
		return nil, domain.Invalid("Keep notes under %d characters: a conclusion and where it came from, not a transcript.", noteMaxChars)
	}
	var sources []protocol.Source
	for _, id := range a.Sources {
		if m, err := store.GetMessage(ctx, t.tx, id); err == nil {
			if m.RoomID != env.room.ID {
				return nil, domain.Forbidden("Source %s isn't in this conversation; cite what you can see here.", id)
			}
			sources = append(sources, protocol.Source{Kind: "message", ID: id, RoomID: m.RoomID})
			continue
		}
		if j, err := store.GetJob(ctx, t.tx, id); err == nil {
			if j.Source.RoomID != env.room.ID {
				return nil, domain.Forbidden("Job %s belongs to another conversation.", id)
			}
			sources = append(sources, protocol.Source{Kind: "job", ID: id, RoomID: j.Source.RoomID})
			continue
		}
		return nil, domain.Invalid("Unknown source %s (use message or job IDs).", id)
	}
	if env.job.Kind != protocol.JobKindReply && !containsSource(sources, env.job.ID) {
		sources = append(sources, protocol.Source{Kind: "job", ID: env.job.ID, RoomID: env.job.Source.RoomID})
	}
	scope := protocol.DecisionScope{Kind: "room", ID: env.room.ID}
	projectID := env.job.ProjectID
	if a.Project != "" {
		project, _, err := h.resolveScope(ctx, t.tx, env, a.Project, "", protocol.JobKindInvestigation)
		if err != nil {
			return nil, err
		}
		projectID = project.ID
	}
	if projectID != "" {
		scope = protocol.DecisionScope{Kind: "project", ID: projectID}
	}
	visible, err := h.decisionVisibility(ctx, t.tx, sources)
	if err != nil {
		return nil, err
	}
	if a.Supersedes != "" {
		prev, err := store.GetNote(ctx, t.tx, a.Supersedes)
		if err != nil || prev.EngineerID != env.eng.ID || !noteVisibleIn(prev, env.room) {
			return nil, domain.Invalid("Note %s isn't one of your notes visible here.", a.Supersedes)
		}
	}
	var current int
	_ = t.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM engineer_notes WHERE engineer_id = ? AND status IN ('proposed','accepted')`, env.eng.ID).Scan(&current)
	if current >= notesPerEngineer {
		return nil, domain.Limit("You already keep %d notes. Supersede an outdated one instead of adding another.", current)
	}
	now := h.now()
	n := protocol.EngineerNote{ID: domain.NewID(), EngineerID: env.eng.ID, Scope: scope, Body: body, Status: "proposed", SupersedesID: a.Supersedes,
		CreatedBy: env.me, Sources: sources, VisibleRoomIDs: visible, ReviewAfter: now.Add(noteReviewAfter), CreatedAt: now}
	if err := store.InsertNote(ctx, t.tx, n); err != nil {
		return nil, err
	}
	status, msg := "proposed", "Kept as a suggestion on your profile. It becomes part of your notes when the owner keeps it, or automatically once the work it comes from is finished."
	if h.ownFinishedWorkOnly(ctx, t.tx, n) {
		if err := h.acceptNote(ctx, t, n, protocol.Actor{Kind: protocol.ActorSystem, ID: "policy:auto-accept"}); err != nil {
			return nil, err
		}
		status, msg = "accepted", "Kept. You'll see it in conversations where its sources are visible."
	} else if err := h.emitNote(ctx, t, n.ID, env.me); err != nil {
		return nil, err
	}
	return map[string]any{"noteId": n.ID, "status": status, "note": msg}, nil
}

// ownFinishedWorkOnly is the narrow auto-accept policy: every source is a
// job this engineer owned that has completed. (A message source, or work
// still open, leaves the note a proposal.)
func (h *Hub) ownFinishedWorkOnly(ctx context.Context, q store.Q, n protocol.EngineerNote) bool {
	if n.CreatedBy.Kind != protocol.ActorEngineer || len(n.Sources) == 0 {
		return false
	}
	if n.SupersedesID != "" {
		if prev, err := store.GetNote(ctx, q, n.SupersedesID); err == nil && prev.CreatedBy.Kind == protocol.ActorUser {
			return false // correcting the owner's note stays a proposal
		}
	}
	for _, s := range n.Sources {
		if s.Kind != "job" {
			return false
		}
		j, err := store.GetJob(ctx, q, s.ID)
		if err != nil || j.OwnerID != n.EngineerID || j.State != protocol.JobCompleted {
			return false
		}
	}
	return true
}

// autoAcceptNotes applies the policy once a job completes.
func (h *Hub) autoAcceptNotes(ctx context.Context, t *txn, job store.JobRow) error {
	ns, err := store.ListNotes(ctx, t.tx, `status = 'proposed' AND id IN (SELECT note_id FROM engineer_note_sources WHERE source_kind = 'job' AND source_id = ?)`, job.ID)
	if err != nil {
		return err
	}
	for _, n := range ns {
		if h.ownFinishedWorkOnly(ctx, t.tx, n) {
			if err := h.acceptNote(ctx, t, n, protocol.Actor{Kind: protocol.ActorSystem, ID: "policy:auto-accept"}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *Hub) acceptNote(ctx context.Context, t *txn, n protocol.EngineerNote, by protocol.Actor) error {
	if _, err := store.SetNoteStatus(ctx, t.tx, n.ID, 0, "accepted", &by, ""); err != nil {
		return err
	}
	if n.SupersedesID != "" {
		if _, err := store.SetNoteStatus(ctx, t.tx, n.SupersedesID, 0, "superseded", nil, n.ID); err != nil {
			return err
		}
		if err := h.emitNote(ctx, t, n.SupersedesID, by); err != nil {
			return err
		}
	}
	return h.emitNote(ctx, t, n.ID, by)
}

func (h *Hub) emitNote(ctx context.Context, t *txn, id string, by protocol.Actor) error {
	n, err := store.GetNote(ctx, t.tx, id)
	if err != nil {
		return err
	}
	return t.emit(ev{Type: "note.updated", Actor: by, Visibility: "owner", Payload: n})
}

// ListNotes returns an engineer's notes for the owner's profile view.
func (h *Hub) ListNotes(ctx context.Context, userID, engineerID string) ([]protocol.EngineerNote, error) {
	if _, err := store.GetEngineer(ctx, h.st.R(), engineerID); err != nil {
		return nil, domain.NotFound("That engineer doesn't exist.")
	}
	return store.ListNotes(ctx, h.st.R(), `engineer_id = ? AND status IN ('proposed', 'accepted')`, engineerID)
}

// CreateNote records an owner-written note for an engineer, accepted at
// once, optionally correcting (superseding) an earlier note.
func (h *Hub) CreateNote(ctx context.Context, userID, engineerID string, req protocol.NoteRequest) (protocol.EngineerNote, error) {
	var out protocol.EngineerNote
	body := strings.TrimSpace(req.Body)
	if body == "" {
		return out, domain.Invalid("Write the note.")
	}
	if utf8.RuneCountInString(body) > noteMaxChars {
		return out, domain.Invalid("Keep notes under %d characters.", noteMaxChars)
	}
	err := h.do(ctx, func(t *txn) error {
		if _, err := store.GetEngineer(ctx, t.tx, engineerID); err != nil {
			return domain.NotFound("That engineer doesn't exist.")
		}
		scope, visible := req.Scope, []string(nil)
		if req.SupersedesID != "" {
			prev, err := store.GetNote(ctx, t.tx, req.SupersedesID)
			if err != nil || prev.EngineerID != engineerID {
				return domain.NotFound("That note doesn't exist.")
			}
			if prev.Status != "accepted" && prev.Status != "proposed" {
				return domain.Conflict("That note is already %s.", prev.Status)
			}
			// A correction keeps the original's scope and visibility.
			scope, visible = prev.Scope, prev.VisibleRoomIDs
		}
		switch scope.Kind {
		case "project":
			if _, err := store.GetProject(ctx, t.tx, scope.ID); err != nil {
				return domain.Invalid("Choose a project for the note.")
			}
		case "room":
			r, err := h.requireRoom(ctx, t.tx, userID, scope.ID)
			if err != nil {
				return err
			}
			if r.Private && visible == nil {
				visible = []string{r.ID}
			}
		default:
			return domain.Invalid("A note belongs to a project or a room.")
		}
		now := h.now()
		n := protocol.EngineerNote{ID: domain.NewID(), EngineerID: engineerID, Scope: scope, Body: body, Status: "proposed",
			SupersedesID: req.SupersedesID, CreatedBy: userActor(userID), VisibleRoomIDs: visible, ReviewAfter: now.Add(noteReviewAfter), CreatedAt: now}
		if err := store.InsertNote(ctx, t.tx, n); err != nil {
			return err
		}
		if err := h.acceptNote(ctx, t, n, userActor(userID)); err != nil {
			return err
		}
		var err error
		out, err = store.GetNote(ctx, t.tx, n.ID)
		return err
	})
	return out, err
}

// DecideNote accepts or rejects a proposed note, renews a note due for
// review, or removes one.
func (h *Hub) DecideNote(ctx context.Context, userID, id string, req protocol.NoteActionRequest) (protocol.EngineerNote, error) {
	var out protocol.EngineerNote
	err := h.do(ctx, func(t *txn) error {
		n, err := store.GetNote(ctx, t.tx, id)
		if err != nil {
			return domain.NotFound("That note doesn't exist.")
		}
		if n.Version != req.Version {
			return domain.Conflict("This note changed since you saw it.")
		}
		by := userActor(userID)
		switch req.Action {
		case "accept":
			if n.Status != "proposed" {
				return domain.Conflict("This note is already %s.", n.Status)
			}
			if err := h.acceptNote(ctx, t, n, by); err != nil {
				return err
			}
		case "reject", "remove":
			if n.Status != "proposed" && n.Status != "accepted" {
				return domain.Conflict("This note is already %s.", n.Status)
			}
			if ok, err := store.SetNoteStatus(ctx, t.tx, n.ID, n.Version, "rejected", nil, ""); err != nil || !ok {
				return firstErr(err, domain.Conflict("This note changed since you saw it."))
			}
			if err := h.emitNote(ctx, t, n.ID, by); err != nil {
				return err
			}
		case "renew":
			if n.Status != "accepted" {
				return domain.Conflict("Only a kept note can be renewed.")
			}
			if ok, err := store.RenewNote(ctx, t.tx, n.ID, n.Version, h.now().Add(noteReviewAfter)); err != nil || !ok {
				return firstErr(err, domain.Conflict("This note changed since you saw it."))
			}
			if err := h.emitNote(ctx, t, n.ID, by); err != nil {
				return err
			}
		default:
			return domain.Invalid("Action must be accept, reject, renew, or remove.")
		}
		if err := t.audit(by, "owner", "note."+req.Action, n.ID, "ok", n.EngineerID); err != nil {
			return err
		}
		out, err = store.GetNote(ctx, t.tx, n.ID)
		return err
	})
	return out, err
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// notesForContext picks an engineer's current notes usable in room: kept,
// not due for review, visible here, and in a project the engineer still has
// access to. Newest first, bounded.
func (h *Hub) notesForContext(ctx context.Context, q store.Q, engineerID string, room protocol.Room) []protocol.EngineerNote {
	ns, err := store.ListNotes(ctx, q, `engineer_id = ? AND status = 'accepted' AND review_after > ?`, engineerID, store.TS(h.now()))
	if err != nil {
		return nil
	}
	var out []protocol.EngineerNote
	for _, n := range ns {
		if !noteVisibleIn(n, room) {
			continue
		}
		if n.Scope.Kind == "project" {
			if g, err := store.GetGrant(ctx, q, n.Scope.ID, engineerID); err != nil || g.Access == "none" {
				continue
			}
		}
		out = append(out, n)
		if len(out) == notesInContext {
			break
		}
	}
	return out
}
