package hub

import (
	"context"
	"strings"

	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

// decisionVisibility computes where a decision may be seen: the intersection
// of its sources' visibility. Anything derived from a private room stays in
// that room; it never becomes organisation-wide because an engineer wrote it.
func (h *Hub) decisionVisibility(ctx context.Context, q store.Q, sources []protocol.Source) ([]string, error) {
	var private []string
	for _, s := range sources {
		if s.RoomID == "" {
			continue
		}
		r, err := store.GetRoom(ctx, q, s.RoomID)
		if err != nil {
			return nil, err
		}
		if r.Private && !contains(private, r.ID) {
			private = append(private, r.ID)
		}
	}
	if len(private) > 1 {
		return nil, domain.Forbidden("These sources come from different private conversations; a decision can't combine them. Record it separately in each.")
	}
	return private, nil
}

func (h *Hub) toolDecisionPropose(ctx context.Context, t *txn, env toolEnv, a bridge.DecisionProposeArgs) (any, error) {
	if strings.TrimSpace(a.Title) == "" || strings.TrimSpace(a.Body) == "" || len(a.Sources) == 0 {
		return nil, domain.Invalid("A decision needs a title, body, and at least one source.")
	}
	var sources []protocol.Source
	for _, id := range a.Sources {
		if m, err := store.GetMessage(ctx, t.tx, id); err == nil {
			// Sources must be visible from where the engineer is working.
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
	d := protocol.Decision{ID: domain.NewID(), Scope: scope, Title: strings.TrimSpace(a.Title), Body: strings.TrimSpace(a.Body),
		Status: "proposed", SupersedesID: a.Supersedes, CreatedBy: env.me, Sources: sources, VisibleRoomIDs: visible, CreatedAt: h.now()}
	if a.Supersedes != "" {
		prev, err := store.GetDecision(ctx, t.tx, a.Supersedes)
		if err != nil || !decisionVisibleIn(prev, env.room) {
			return nil, domain.Invalid("Decision %s isn't visible here.", a.Supersedes)
		}
	}
	if err := store.InsertDecision(ctx, t.tx, h.Org().ID, d); err != nil {
		return nil, err
	}
	if err := t.emit(ev{Type: "decision.created", Actor: env.me, Room: env.room.ID, Visibility: decisionEventVisibility(d, env.room.ID), Payload: d}); err != nil {
		return nil, err
	}
	return map[string]any{"decisionId": d.ID, "status": "proposed",
		"note": "Proposed. Sourced implementation decisions within your authority are accepted automatically once the work's checks and peer review pass."}, nil
}

func containsSource(ss []protocol.Source, id string) bool {
	for _, s := range ss {
		if s.ID == id {
			return true
		}
	}
	return false
}

func decisionEventVisibility(d protocol.Decision, room string) string {
	if d.VisibleRoomIDs != nil {
		return "room:" + d.VisibleRoomIDs[0]
	}
	return "room:" + room
}

// autoAcceptDecisions applies the narrow auto-accept policy: decisions an
// engineer proposed from this job, within its project authority, become
// accepted when the job completes through its checks and peer review. It
// never alters user instructions or widens access.
func (h *Hub) autoAcceptDecisions(ctx context.Context, t *txn, job store.JobRow) error {
	ds, err := store.ListDecisions(ctx, t.tx, `status = 'proposed' AND id IN (SELECT decision_id FROM decision_sources WHERE source_kind = 'job' AND source_id = ?)`, job.ID)
	if err != nil {
		return err
	}
	for _, d := range ds {
		if d.CreatedBy.Kind != protocol.ActorEngineer || d.Scope.Kind != "project" || d.Scope.ID != job.ProjectID {
			continue
		}
		if g, err := store.GetGrant(ctx, t.tx, d.Scope.ID, d.CreatedBy.ID); err != nil || g.Access == "none" {
			continue
		}
		// Policy may accept an engineer's decision, but never one that would
		// supersede something the owner decided: that stays a proposal.
		if d.SupersedesID != "" {
			if prev, err := store.GetDecision(ctx, t.tx, d.SupersedesID); err == nil && prev.CreatedBy.Kind == protocol.ActorUser {
				continue
			}
		}
		if err := h.acceptDecision(ctx, t, d, protocol.Actor{Kind: protocol.ActorSystem, ID: "policy:auto-accept"}); err != nil {
			return err
		}
	}
	return nil
}

func (h *Hub) acceptDecision(ctx context.Context, t *txn, d protocol.Decision, by protocol.Actor) error {
	if _, err := store.SetDecisionStatus(ctx, t.tx, d.ID, 0, "accepted", &by, ""); err != nil {
		return err
	}
	if d.SupersedesID != "" {
		if _, err := store.SetDecisionStatus(ctx, t.tx, d.SupersedesID, 0, "superseded", nil, d.ID); err != nil {
			return err
		}
		if prev, err := store.GetDecision(ctx, t.tx, d.SupersedesID); err == nil {
			_ = t.emit(ev{Type: "decision.updated", Actor: by, Visibility: decisionEventVisibility(prev, firstSourceRoom(prev)), Payload: prev})
		}
	}
	d2, err := store.GetDecision(ctx, t.tx, d.ID)
	if err != nil {
		return err
	}
	return t.emit(ev{Type: "decision.updated", Actor: by, Visibility: decisionEventVisibility(d2, firstSourceRoom(d2)), Payload: d2})
}

func firstSourceRoom(d protocol.Decision) string {
	for _, s := range d.Sources {
		if s.RoomID != "" {
			return s.RoomID
		}
	}
	return ""
}

// ListDecisions returns decisions visible to the owner.
func (h *Hub) ListDecisions(ctx context.Context, userID string, status string) ([]protocol.Decision, error) {
	rooms, err := store.RoomIDsForMember(ctx, h.st.R(), protocol.ActorUser, userID)
	if err != nil {
		return nil, err
	}
	where := ""
	var args []any
	if status != "" {
		where, args = "status = ?", []any{status}
	}
	all, err := store.ListDecisions(ctx, h.st.R(), where, args...)
	if err != nil {
		return nil, err
	}
	var out []protocol.Decision
	for _, d := range all {
		if d.VisibleRoomIDs != nil && !anyIn(d.VisibleRoomIDs, rooms) {
			continue
		}
		out = append(out, d)
	}
	if out == nil {
		out = []protocol.Decision{}
	}
	return out, nil
}

func anyIn(a, b []string) bool {
	for _, x := range a {
		if contains(b, x) {
			return true
		}
	}
	return false
}

// CreateDecision records an owner-authored decision (accepted immediately
// when requested), optionally superseding an earlier one.
func (h *Hub) CreateDecision(ctx context.Context, userID string, req protocol.DecisionRequest) (protocol.Decision, error) {
	if strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.Body) == "" {
		return protocol.Decision{}, domain.Invalid("A decision needs a title and a body.")
	}
	if !contains([]string{"project", "room", "org"}, req.Scope.Kind) {
		return protocol.Decision{}, domain.Invalid("Scope must be project, room, or org.")
	}
	var out protocol.Decision
	err := h.do(ctx, func(t *txn) error {
		for i, s := range req.Sources {
			if s.Kind == "message" {
				m, err := store.GetMessage(ctx, t.tx, s.ID)
				if err != nil {
					return domain.Invalid("Unknown source message.")
				}
				if _, err := h.requireRoom(ctx, t.tx, userID, m.RoomID); err != nil {
					return err
				}
				req.Sources[i].RoomID = m.RoomID
			}
		}
		visible, err := h.decisionVisibility(ctx, t.tx, req.Sources)
		if err != nil {
			return err
		}
		d := protocol.Decision{ID: domain.NewID(), Scope: req.Scope, Title: strings.TrimSpace(req.Title), Body: strings.TrimSpace(req.Body),
			Status: "proposed", SupersedesID: req.SupersedesID, CreatedBy: userActor(userID), Sources: append([]protocol.Source{}, req.Sources...),
			VisibleRoomIDs: visible, CreatedAt: h.now()}
		if err := store.InsertDecision(ctx, t.tx, h.Org().ID, d); err != nil {
			return err
		}
		if req.Accept {
			if err := h.acceptDecision(ctx, t, d, userActor(userID)); err != nil {
				return err
			}
		}
		out, err = store.GetDecision(ctx, t.tx, d.ID)
		if err != nil {
			return err
		}
		return t.emit(ev{Type: "decision.created", Actor: userActor(userID), Visibility: decisionEventVisibility(out, firstSourceRoom(out)), Payload: out})
	})
	return out, err
}

// DecideDecision accepts or rejects a proposed decision.
func (h *Hub) DecideDecision(ctx context.Context, userID, id string, req protocol.DecisionActionRequest) (protocol.Decision, error) {
	var out protocol.Decision
	err := h.do(ctx, func(t *txn) error {
		d, err := store.GetDecision(ctx, t.tx, id)
		if err != nil {
			return domain.NotFound("That decision doesn't exist.")
		}
		if d.Version != req.Version {
			return domain.Conflict("This decision changed since you saw it.")
		}
		if d.Status != "proposed" {
			return domain.Conflict("This decision is already %s.", d.Status)
		}
		switch req.Action {
		case "accept":
			if err := h.acceptDecision(ctx, t, d, userActor(userID)); err != nil {
				return err
			}
		case "reject":
			if _, err := store.SetDecisionStatus(ctx, t.tx, d.ID, req.Version, "rejected", nil, ""); err != nil {
				return err
			}
		default:
			return domain.Invalid("Action must be accept or reject.")
		}
		out, err = store.GetDecision(ctx, t.tx, id)
		if err != nil {
			return err
		}
		return t.emit(ev{Type: "decision.updated", Actor: userActor(userID), Visibility: decisionEventVisibility(out, firstSourceRoom(out)), Payload: out})
	})
	return out, err
}
