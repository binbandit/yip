package hub

import (
	"context"
	"strings"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

func (h *Hub) GetRoomOrder(ctx context.Context, userID string) (protocol.RoomOrder, error) {
	rooms, err := h.ListRooms(ctx, userID)
	if err != nil {
		return protocol.RoomOrder{}, err
	}
	return h.roomOrderForRooms(ctx, h.st.R(), userID, rooms)
}

func (h *Hub) roomOrderForRooms(ctx context.Context, q store.Q, userID string, rooms []protocol.Room) (protocol.RoomOrder, error) {
	var out protocol.RoomOrder
	visible := make(map[string]string, len(rooms))
	for _, room := range rooms {
		if !room.Archived {
			visible[room.ID] = room.Kind
		}
	}
	for kind, dest := range map[string]*protocol.RoomOrderSection{protocol.RoomKindRoom: &out.Rooms, protocol.RoomKindDM: &out.DMs} {
		section, err := store.GetRoomOrderSection(ctx, q, userID, kind)
		if err != nil {
			return out, err
		}
		ids := make([]string, 0, len(section.RoomIDs))
		for _, id := range section.RoomIDs {
			if visible[id] == kind {
				ids = append(ids, id)
			}
		}
		*dest = protocol.RoomOrderSection{Version: section.Version, RoomIDs: ids}
	}
	return out, nil
}

func (h *Hub) SetRoomOrder(ctx context.Context, userID, kind string, req protocol.UpdateRoomOrderRequest) (protocol.RoomOrderSection, error) {
	if kind != protocol.RoomKindRoom && kind != protocol.RoomKindDM {
		return protocol.RoomOrderSection{}, domain.Invalid("Choose rooms or direct messages to order.")
	}
	if req.Version < 0 || req.Version >= 9_007_199_254_740_991 || len(req.RoomIDs) > 2000 {
		return protocol.RoomOrderSection{}, domain.Invalid("The room order is too large or has an invalid version.")
	}
	seen := make(map[string]bool, len(req.RoomIDs))
	for _, id := range req.RoomIDs {
		if id == "" || len(id) > 128 || strings.TrimSpace(id) != id || seen[id] {
			return protocol.RoomOrderSection{}, domain.Invalid("Each room must appear at most once with a valid ID.")
		}
		seen[id] = true
	}
	out := protocol.RoomOrderSection{Version: req.Version + 1, RoomIDs: append([]string{}, req.RoomIDs...)}
	err := h.do(ctx, func(t *txn) error {
		for _, id := range req.RoomIDs {
			room, err := h.requireRoom(ctx, t.tx, userID, id)
			if err != nil {
				return err
			}
			if room.Kind != kind || room.Archived {
				return domain.Invalid("Only current rooms from this section can be ordered.")
			}
		}
		ok, err := store.SetRoomOrderSection(ctx, t.tx, userID, kind, req.Version, out.RoomIDs)
		if err != nil {
			return err
		}
		if !ok {
			return domain.Conflict("Your room order changed in another view. Reload it and try again.")
		}
		return t.emit(ev{Type: "room_order.updated", Actor: userActor(userID), Visibility: "user:" + userID,
			Payload: protocol.RoomOrderUpdated{Kind: kind, Order: out}})
	})
	return out, err
}
