package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

// events streams committed events after the client's cursor (Last-Event-ID),
// filtered to what the user may see, plus transient streaming updates. If the
// cursor has aged out, it sends a reset instead of silently skipping history.
// Slow consumers are disconnected with a recoverable reason.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	user := userFrom(r)
	q := s.hub.Store().R()
	cursor := int64(0)
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		cursor, _ = strconv.ParseInt(v, 10, 64)
	} else if v := r.URL.Query().Get("cursor"); v != "" {
		cursor, _ = strconv.ParseInt(v, 10, 64)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	write := func(id int64, event string, data any) bool {
		b, err := json.Marshal(data)
		if err != nil {
			return true
		}
		if id > 0 {
			fmt.Fprintf(w, "id: %d\n", id)
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
		return ctx.Err() == nil
	}

	max, _ := store.MaxEventSeq(ctx, q)
	min, _ := store.MinEventSeq(ctx, q)
	if cursor > max || (cursor > 0 && min > 0 && cursor < min-1) || (cursor > 0 && max-cursor > s.hub.Limits().ReplayRetention) {
		write(0, "reset", map[string]any{"cursor": max, "reason": "Your replay position is no longer available; reload current state."})
		cursor = max
	}
	rooms := map[string]bool{}
	refreshRooms := func() {
		ids, _ := store.RoomIDsForMember(ctx, q, protocol.ActorUser, user.ID)
		rooms = map[string]bool{}
		for _, id := range ids {
			rooms[id] = true
		}
	}
	refreshRooms()
	visible := func(vis string) bool {
		switch {
		case vis == "owner", vis == "org":
			return true
		case strings.HasPrefix(vis, "room:"):
			return rooms[strings.TrimPrefix(vis, "room:")]
		}
		return false
	}
	sub := s.hub.Bus().Subscribe(s.hub.Limits().SSEClientBuffer)
	defer sub.Close()
	write(0, "ready", map[string]any{"cursor": cursor})
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		changed := s.hub.Bus().Changed()
		for {
			evs, err := store.EventsAfter(ctx, q, cursor, 500)
			if err != nil {
				return
			}
			for _, e := range evs {
				cursor = e.Sequence
				if strings.HasPrefix(e.Type, "room.member") || e.Type == "room.created" {
					refreshRooms()
				}
				if !visible(e.Visibility) {
					continue
				}
				if !write(e.Sequence, e.Type, e.Event) {
					return
				}
			}
			if len(evs) < 500 {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
		case t := <-sub.C:
			if t.RoomID == "" || rooms[t.RoomID] {
				if !write(0, "transient", t) {
					return
				}
			}
		case <-heartbeat.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
		if sub.Slow() {
			write(0, "slow", map[string]any{"reason": "This connection fell behind; reconnect to resume from your cursor."})
			return
		}
	}
}
