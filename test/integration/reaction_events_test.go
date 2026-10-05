package integration

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

// Read through a visible event, or EOF if the server refuses delivery.
func reactionEventsThrough(t *testing.T, c *client, after, through int64) []protocol.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", c.base+"/v1/events", nil)
	req.Header.Set("Last-Event-ID", strconv.FormatInt(after, 10))
	resp, err := c.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event stream status=%d", resp.StatusCode)
	}
	var events []protocol.Event
	scan := bufio.NewScanner(resp.Body)
	for scan.Scan() {
		if !strings.HasPrefix(scan.Text(), "data: ") {
			continue
		}
		var event protocol.Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(scan.Text(), "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		if event.Sequence == 0 {
			continue
		}
		events = append(events, event)
		if event.Sequence >= through {
			return events
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func TestReactionEventsAreViewerCorrectAfterHTTPAndReplay(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	room := e.roomID("Security")
	other, err := store.GetUser(e.ctx, e.hub.Store().R(), e.c.boot.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	other.ID, other.Handle = "second-viewer", "second-viewer"
	other.CreatedAt = other.CreatedAt.Add(time.Second)
	if err := e.hub.Store().Tx(e.ctx, func(tx *sql.Tx) error {
		if err := store.InsertUser(e.ctx, tx, other); err != nil {
			return err
		}
		_, err := store.AddMember(e.ctx, tx, room, protocol.Member{Kind: protocol.ActorUser, ID: other.ID})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	viewer := &client{t: t, base: e.browser.URL, hc: &http.Client{Jar: jar}}
	viewer.must("POST", "/v1/session", protocol.SignInRequest{Handle: other.Handle, Password: ownerPassword}, nil)
	viewer.must("GET", "/v1/bootstrap", nil, &viewer.boot)
	viewer.csrf = viewer.boot.CSRFToken
	cursor := func() int64 {
		t.Helper()
		n, err := store.MaxEventSeq(e.ctx, e.hub.Store().R())
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	check := func(m protocol.Message, count int, mine bool) {
		t.Helper()
		want := []protocol.ReactionSummary{{Emoji: "🎉", Count: count, Mine: mine}}
		if count == 0 {
			want = []protocol.ReactionSummary{}
		}
		if !reflect.DeepEqual(m.Reactions, want) {
			t.Fatalf("reaction snapshot=%+v; want %+v", m.Reactions, want)
		}
	}
	before := cursor()
	m := e.post("Security", "react to me", nil, nil).Message
	messageCursor := func() int64 {
		t.Helper()
		var seq int64
		if err := e.hub.Store().R().QueryRowContext(e.ctx, `SELECT seq FROM events
			WHERE room_id = ? AND type = 'message.updated' AND json_extract(payload, '$.id') = ?
			ORDER BY seq DESC LIMIT 1`, room, m.ID).Scan(&seq); err != nil {
			t.Fatal(err)
		}
		return seq
	}
	path := "/v1/messages/" + m.ID
	var response protocol.Message
	e.c.must("POST", path+"/reactions", protocol.ReactRequest{Emoji: "🎉"}, &response)
	check(response, 1, true)
	// The HTTP response is complete before either viewer receives the SSE.
	for _, tc := range []struct {
		c    *client
		mine bool
	}{{e.c, true}, {viewer, false}} {
		events := reactionEventsThrough(t, tc.c, before, messageCursor())
		if len(events) != 2 {
			t.Fatalf("expected message creation and reaction update, got %d", len(events))
		}
		var got protocol.Message
		if err := json.Unmarshal(events[1].Payload, &got); err != nil {
			t.Fatal(err)
		}
		check(got, 1, tc.mine)
	}
	viewer.must("POST", path+"/reactions", protocol.ReactRequest{Emoji: "🎉"}, &response)
	check(response, 2, true)
	// An edit response can also arrive after SSE and replace the full object.
	e.c.must("PATCH", path, map[string]string{"body": "edited message"}, &response)
	check(response, 2, true)
	if response.Body != "edited message" || response.Revision != 2 {
		t.Fatalf("edit snapshot lost its revision: %+v", response)
	}
	secondTab := e.signIn()
	secondTab.must("POST", path+"/reactions", protocol.ReactRequest{Emoji: "🎉", Remove: true}, &response)
	check(response, 1, false)
	retained, err := store.EventsAfter(e.ctx, e.hub.Store().R(), before, 100)
	if err != nil {
		t.Fatal(err)
	}
	// Replaying old creation/add/edit events must reflect the current removal,
	// while retaining their original bodies, revisions and event metadata.
	for _, tc := range []struct {
		c    *client
		mine bool
	}{{e.c, false}, {viewer, true}, {secondTab, false}} {
		got := reactionEventsThrough(t, tc.c, before, messageCursor())
		if len(got) != len(retained) {
			t.Fatalf("replay length=%d; want %d", len(got), len(retained))
		}
		for i, event := range got {
			var message, original protocol.Message
			if err := json.Unmarshal(event.Payload, &message); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(retained[i].Payload, &original); err != nil {
				t.Fatal(err)
			}
			check(message, 1, tc.mine)
			message.Reactions = original.Reactions
			if !reflect.DeepEqual(message, original) {
				t.Fatal("reaction projection changed historical message fields")
			}
			event.Payload = retained[i].Payload
			if !reflect.DeepEqual(event, retained[i].Event) {
				t.Fatal("reaction projection changed event identity or ordering")
			}
		}
	}
	after, err := store.EventsAfter(e.ctx, e.hub.Store().R(), before, 100)
	if err != nil || !reflect.DeepEqual(after, retained) {
		t.Fatal("viewer projection changed retained events")
	}
	viewer.must("POST", path+"/reactions", protocol.ReactRequest{Emoji: "🎉", Remove: true}, &response)
	check(response, 0, false)
	for _, c := range []*client{e.c, viewer} {
		for _, event := range reactionEventsThrough(t, c, before, messageCursor()) {
			var got protocol.Message
			if err := json.Unmarshal(event.Payload, &got); err != nil {
				t.Fatal(err)
			}
			check(got, 0, false)
		}
	}
	// A visible marker lets us bound replay after a hidden room's events.
	before = cursor()
	hidden := e.post("Engineering", "private reaction", nil, nil).Message
	e.c.must("POST", "/v1/messages/"+hidden.ID+"/reactions", protocol.ReactRequest{Emoji: "🎉"}, &response)
	e.c.must("POST", path+"/reactions", protocol.ReactRequest{Emoji: "🎉"}, &response)
	// A hidden event after the marker must not become the replay target.
	e.c.must("POST", "/v1/messages/"+hidden.ID+"/reactions", protocol.ReactRequest{Emoji: "🎉", Remove: true}, &response)
	visible := reactionEventsThrough(t, viewer, before, messageCursor())
	if len(visible) != 1 || visible[0].RoomID != room {
		t.Fatalf("viewer received events outside their rooms: %+v", visible)
	}
	e.c.must("DELETE", path, nil, nil)
	e.c.must("POST", "/v1/messages/"+hidden.ID+"/reactions", protocol.ReactRequest{Emoji: "🎉"}, &response)
	for _, event := range reactionEventsThrough(t, viewer, 0, messageCursor()) {
		if event.Type != "message.created" && event.Type != "message.updated" {
			continue
		}
		var got protocol.Message
		if err := json.Unmarshal(event.Payload, &got); err != nil {
			t.Fatal(err)
		}
		if got.ID == m.ID && got.Body != "" {
			t.Fatal("viewer projection resurrected redacted message content")
		}
	}
}

func TestReactionProjectionFailureDoesNotDeliverEventID(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	before, err := store.MaxEventSeq(e.ctx, e.hub.Store().R())
	if err != nil {
		t.Fatal(err)
	}
	e.post("Security", "requires a viewer snapshot", nil, nil)
	through, err := store.MaxEventSeq(e.ctx, e.hub.Store().R())
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt only this disposable database to force the projection query to
	// fail after the event itself has been read successfully.
	if err := e.hub.Store().Tx(e.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(e.ctx, "DROP TABLE reactions")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := reactionEventsThrough(t, e.c, before, through); len(got) != 0 {
		t.Fatal("a failed projection delivered an event ID that a reconnect would skip")
	}
}
