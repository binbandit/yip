package hub

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

func userActor(id string) protocol.Actor { return protocol.Actor{Kind: protocol.ActorUser, ID: id} }

// requireRoom loads a room and checks the user is a member.
func (h *Hub) requireRoom(ctx context.Context, q store.Q, userID, roomID string) (protocol.Room, error) {
	r, err := store.GetRoom(ctx, q, roomID)
	if errors.Is(err, store.ErrNotFound) {
		return r, domain.NotFound("That room doesn't exist.")
	}
	if err != nil {
		return r, err
	}
	ok, err := store.IsMember(ctx, q, roomID, protocol.ActorUser, userID)
	if err != nil {
		return r, err
	}
	if !ok {
		return r, domain.NotFound("That room doesn't exist.")
	}
	return r, nil
}

// ListRooms returns rooms visible to the user with unread state.
func (h *Hub) ListRooms(ctx context.Context, userID string) ([]protocol.Room, error) {
	return store.ListRoomsForUser(ctx, h.st.R(), userID)
}

// GetRoom returns one room with the viewer's read state.
func (h *Hub) GetRoom(ctx context.Context, userID, roomID string) (protocol.Room, error) {
	r, err := h.requireRoom(ctx, h.st.R(), userID, roomID)
	if err != nil {
		return r, err
	}
	return r, store.FillReadState(ctx, h.st.R(), &r, userID)
}

// CreateRoom creates a room (or DM) with engineers and linked projects.
func (h *Hub) CreateRoom(ctx context.Context, userID string, req protocol.CreateRoomRequest) (protocol.Room, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Kind == "" {
		req.Kind = protocol.RoomKindRoom
	}
	if req.Kind != protocol.RoomKindRoom && req.Kind != protocol.RoomKindDM {
		return protocol.Room{}, domain.Invalid("A room must be a group room or a direct message.")
	}
	if req.Kind == protocol.RoomKindDM {
		if len(req.EngineerIDs) != 1 {
			return protocol.Room{}, domain.Invalid("A direct message has exactly one engineer.")
		}
		req.Private = true
	}
	if req.Name == "" && req.Kind == protocol.RoomKindRoom {
		return protocol.Room{}, domain.Invalid("Give the room a name.")
	}
	if req.ReplyMode == "" {
		req.ReplyMode = protocol.ReplyModeQuiet
		if req.Kind == protocol.RoomKindDM {
			req.ReplyMode = protocol.ReplyModeSteward
		}
	}
	if req.ReplyMode != protocol.ReplyModeQuiet && req.ReplyMode != protocol.ReplyModeSteward {
		return protocol.Room{}, domain.Invalid("Reply mode must be quiet or steward.")
	}
	var room protocol.Room
	err := h.do(ctx, func(t *txn) error {
		for _, id := range req.EngineerIDs {
			e, err := store.GetEngineer(ctx, t.tx, id)
			if err != nil {
				return domain.Invalid("Unknown engineer %s.", id)
			}
			if e.Archived {
				return domain.Invalid("%s is archived.", e.Name)
			}
			if req.Kind == protocol.RoomKindDM && req.Name == "" {
				req.Name = e.Name
			}
		}
		if req.Kind == protocol.RoomKindDM {
			req.StewardID = req.EngineerIDs[0]
		}
		if req.StewardID != "" && !contains(req.EngineerIDs, req.StewardID) {
			return domain.Invalid("The steward must be one of the room's engineers.")
		}
		if req.ReplyMode == protocol.ReplyModeSteward && req.StewardID == "" {
			return domain.Invalid("Choose a steward for a steward-mode room, or use quiet mode.")
		}
		for _, p := range req.ProjectIDs {
			if _, err := store.GetProject(ctx, t.tx, p); err != nil {
				return domain.Invalid("Unknown project %s.", p)
			}
		}
		room = protocol.Room{ID: domain.NewID(), OrgID: h.Org().ID, Name: req.Name, Kind: req.Kind, Purpose: strings.TrimSpace(req.Purpose),
			Private: req.Private, ReplyMode: req.ReplyMode, StewardID: req.StewardID, CreatedAt: h.now()}
		if err := store.InsertRoom(ctx, t.tx, room); err != nil {
			return err
		}
		if _, err := store.AddMember(ctx, t.tx, room.ID, protocol.Member{Kind: protocol.ActorUser, ID: userID}); err != nil {
			return err
		}
		for _, id := range req.EngineerIDs {
			if _, err := store.AddMember(ctx, t.tx, room.ID, protocol.Member{Kind: protocol.ActorEngineer, ID: id}); err != nil {
				return err
			}
		}
		if err := store.SetRoomProjects(ctx, t.tx, room.ID, req.ProjectIDs); err != nil {
			return err
		}
		var err error
		if room, err = store.GetRoom(ctx, t.tx, room.ID); err != nil {
			return err
		}
		return t.emit(ev{Type: "room.created", Actor: userActor(userID), Room: room.ID, Payload: room})
	})
	return room, err
}

// UpdateRoom changes room configuration under an optimistic version check.
func (h *Hub) UpdateRoom(ctx context.Context, userID, roomID string, req protocol.UpdateRoomRequest) (protocol.Room, error) {
	var room protocol.Room
	err := h.do(ctx, func(t *txn) error {
		r, err := h.requireRoom(ctx, t.tx, userID, roomID)
		if err != nil {
			return err
		}
		if r.Version != req.Version {
			return domain.Conflict("This room changed since you opened it. Reload and try again.")
		}
		if r.Kind == protocol.RoomKindOverview {
			return domain.Forbidden("The Overview conversation can't be reconfigured.")
		}
		if req.Name != nil {
			r.Name = strings.TrimSpace(*req.Name)
		}
		if req.Purpose != nil {
			r.Purpose = strings.TrimSpace(*req.Purpose)
		}
		if req.Private != nil {
			r.Private = *req.Private
		}
		if req.ReplyMode != nil {
			r.ReplyMode = *req.ReplyMode
		}
		if req.StewardID != nil {
			r.StewardID = *req.StewardID
		}
		if req.Archived != nil {
			r.Archived = *req.Archived
		}
		if r.ReplyMode != protocol.ReplyModeQuiet && r.ReplyMode != protocol.ReplyModeSteward {
			return domain.Invalid("Reply mode must be quiet or steward.")
		}
		if r.StewardID != "" {
			ok, _ := store.IsMember(ctx, t.tx, r.ID, protocol.ActorEngineer, r.StewardID)
			if !ok {
				return domain.Invalid("The steward must be an engineer in this room.")
			}
		}
		if r.ReplyMode == protocol.ReplyModeSteward && r.StewardID == "" {
			return domain.Invalid("Choose a steward for a steward-mode room, or use quiet mode.")
		}
		ok, err := store.UpdateRoom(ctx, t.tx, r, req.Version)
		if err != nil {
			return err
		}
		if !ok {
			return domain.Conflict("This room changed since you opened it. Reload and try again.")
		}
		if req.ProjectIDs != nil {
			for _, p := range *req.ProjectIDs {
				if _, err := store.GetProject(ctx, t.tx, p); err != nil {
					return domain.Invalid("Unknown project %s.", p)
				}
			}
			if err := store.SetRoomProjects(ctx, t.tx, r.ID, *req.ProjectIDs); err != nil {
				return err
			}
			// Linked projects are part of every member's scope fingerprint.
			if err := store.InvalidateProviderSessions(ctx, t.tx, "", "room projects changed"); err != nil {
				return err
			}
		}
		if room, err = store.GetRoom(ctx, t.tx, r.ID); err != nil {
			return err
		}
		return t.emit(ev{Type: "room.updated", Actor: userActor(userID), Room: room.ID, Payload: room})
	})
	return room, err
}

// PreviewMembership explains what history a new engineer member would see.
func (h *Hub) PreviewMembership(ctx context.Context, userID, roomID, engineerID string) (protocol.MembershipPreview, error) {
	r, err := h.requireRoom(ctx, h.st.R(), userID, roomID)
	if err != nil {
		return protocol.MembershipPreview{}, err
	}
	e, err := store.GetEngineer(ctx, h.st.R(), engineerID)
	if err != nil {
		return protocol.MembershipPreview{}, domain.NotFound("That engineer doesn't exist.")
	}
	var n int
	_ = h.st.R().QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE room_id = ? AND deleted_at IS NULL`, roomID).Scan(&n)
	exp := e.Name + " will be able to read this room's history when working here."
	if r.Private {
		exp = "This is a private room. " + e.Name + " will be able to read all " + itoa(n) +
			" messages here when working in this room. Private history still won't be carried into other rooms."
	}
	return protocol.MembershipPreview{RoomID: roomID, EngineerID: engineerID, VisibleCount: n, PrivateRoom: r.Private, Explanation: exp}, nil
}

// SetMembership adds or removes an engineer from a room.
func (h *Hub) SetMembership(ctx context.Context, userID, roomID, engineerID string, present bool) (protocol.Room, error) {
	var room protocol.Room
	err := h.do(ctx, func(t *txn) error {
		r, err := h.requireRoom(ctx, t.tx, userID, roomID)
		if err != nil {
			return err
		}
		if r.Kind != protocol.RoomKindRoom {
			return domain.Forbidden("Members can only be changed in group rooms.")
		}
		e, err := store.GetEngineer(ctx, t.tx, engineerID)
		if err != nil {
			return domain.NotFound("That engineer doesn't exist.")
		}
		m := protocol.Member{Kind: protocol.ActorEngineer, ID: engineerID}
		if present {
			if e.Archived {
				return domain.Invalid("%s is archived.", e.Name)
			}
			if _, err := store.AddMember(ctx, t.tx, roomID, m); err != nil {
				return err
			}
		} else {
			if r.StewardID == engineerID {
				return domain.Invalid("%s is this room's steward. Choose another steward or switch to quiet mode first.", e.Name)
			}
			if _, err := store.RemoveMember(ctx, t.tx, roomID, m); err != nil {
				return err
			}
			// Removing access invalidates cached context and session reuse.
			if err := store.InvalidateProviderSessions(ctx, t.tx, engineerID, "removed from room"); err != nil {
				return err
			}
		}
		if room, err = store.GetRoom(ctx, t.tx, roomID); err != nil {
			return err
		}
		typ := "room.member_added"
		if !present {
			typ = "room.member_removed"
		}
		if err := t.emit(ev{Type: typ, Actor: userActor(userID), Room: roomID, Payload: map[string]any{"room": room, "engineerId": engineerID}}); err != nil {
			return err
		}
		return t.emit(ev{Type: "engineer.updated", Payload: mustEngineer(ctx, t, engineerID)})
	})
	return room, err
}

func mustEngineer(ctx context.Context, t *txn, id string) protocol.Engineer {
	e, _ := store.GetEngineer(ctx, t.tx, id)
	return e
}

// MarkRead advances the viewer's read cursor in a room.
func (h *Hub) MarkRead(ctx context.Context, userID, roomID string, seq int64) error {
	return h.do(ctx, func(t *txn) error {
		if _, err := h.requireRoom(ctx, t.tx, userID, roomID); err != nil {
			return err
		}
		if err := store.SetRead(ctx, t.tx, roomID, userID, seq); err != nil {
			return err
		}
		return t.emit(ev{Type: "read.updated", Actor: userActor(userID), Visibility: "owner", Payload: map[string]any{"roomId": roomID, "seq": seq}})
	})
}

func itoa(n int) string { return strconv.Itoa(n) }
