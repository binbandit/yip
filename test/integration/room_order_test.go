package integration

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

func TestRoomOrderPersistsWithoutReplacingPreferencesOrOtherSections(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	ids := []string{e.roomID("Security"), e.roomID("Engineering")}
	var initial protocol.RoomOrder
	e.c.must("GET", "/v1/room-order", nil, &initial)
	if initial.Rooms.Version != 0 || len(initial.Rooms.RoomIDs) != 0 || initial.DMs.Version != 0 {
		t.Fatalf("unexpected initial order: %+v", initial)
	}
	var rooms protocol.RoomOrderSection
	e.c.must("PUT", "/v1/room-order/room", protocol.UpdateRoomOrderRequest{RoomIDs: ids}, &rooms)
	if rooms.Version != 1 || !reflect.DeepEqual(rooms.RoomIDs, ids) {
		t.Fatalf("saved order: %+v", rooms)
	}
	var dm protocol.Room
	e.c.must("POST", "/v1/rooms", protocol.CreateRoomRequest{Kind: "dm", EngineerIDs: []string{e.engineerID("mira")}}, &dm)
	var dms protocol.RoomOrderSection
	e.c.must("PUT", "/v1/room-order/dm", protocol.UpdateRoomOrderRequest{RoomIDs: []string{dm.ID}}, &dms)
	preferences := protocol.Preferences{Theme: "night", Notify: "mentions", MutedRoomIDs: []string{ids[0]}}
	e.c.must("PUT", "/v1/preferences", protocol.PreferencesRequest{Preferences: preferences}, nil)
	var boot protocol.Bootstrap
	e.c.must("GET", "/v1/bootstrap", nil, &boot)
	if boot.RoomOrder == nil || !reflect.DeepEqual(boot.RoomOrder.Rooms, rooms) || !reflect.DeepEqual(boot.RoomOrder.DMs, dms) || !reflect.DeepEqual(boot.Preferences.MutedRoomIDs, preferences.MutedRoomIDs) {
		t.Fatalf("preferences or section save replaced another order: %+v", boot.RoomOrder)
	}
	// Concurrent updates to one section must not both report success.
	errs := make(chan error, 2)
	for _, ordered := range [][]string{ids, {ids[1], ids[0]}} {
		go func() {
			errs <- e.c.do("PUT", "/v1/room-order/room", protocol.UpdateRoomOrderRequest{Version: rooms.Version, RoomIDs: ordered}, nil)
		}()
	}
	successes, conflicts := 0, 0
	for range 2 {
		err := <-errs
		var ae *apiError
		if err == nil {
			successes++
		} else if errors.As(err, &ae) && ae.status == http.StatusConflict {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("CAS outcomes: %d successes, %d conflicts", successes, conflicts)
	}
	var saved protocol.RoomOrder
	e.c.must("GET", "/v1/room-order", nil, &saved)
	if saved.Rooms.Version != 2 || !reflect.DeepEqual(saved.DMs, dms) {
		t.Fatalf("room move affected DM version: %+v", saved)
	}
	e.restartHub()
	var reopened protocol.RoomOrder
	e.c.must("GET", "/v1/room-order", nil, &reopened)
	if !reflect.DeepEqual(saved, reopened) {
		t.Fatalf("order did not survive restart: %+v", reopened)
	}
	// Archive and membership loss filter reads without rewriting personal order.
	var room protocol.Room
	e.c.must("GET", "/v1/rooms/"+ids[0], nil, &room)
	archived := true
	e.c.must("PATCH", "/v1/rooms/"+room.ID, protocol.UpdateRoomRequest{Version: room.Version, Archived: &archived}, nil)
	if err := e.hub.Store().Tx(e.ctx, func(tx *sql.Tx) error {
		_, err := store.RemoveMember(e.ctx, tx, ids[1], protocol.Member{Kind: protocol.ActorUser, ID: e.c.boot.User.ID})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.c.must("GET", "/v1/room-order", nil, &reopened)
	if len(reopened.Rooms.RoomIDs) != 0 || reopened.Rooms.Version != saved.Rooms.Version {
		t.Fatalf("inaccessible rooms were returned: %+v", reopened)
	}
}

func TestRoomOrderValidatesMembershipKindAndRequestBounds(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	id := e.roomID("Security")
	var dm protocol.Room
	e.c.must("POST", "/v1/rooms", protocol.CreateRoomRequest{Kind: "dm", EngineerIDs: []string{e.engineerID("mira")}}, &dm)
	archivedID := e.roomID("Engineering")
	var archived protocol.Room
	e.c.must("GET", "/v1/rooms/"+archivedID, nil, &archived)
	yes := true
	e.c.must("PATCH", "/v1/rooms/"+archivedID, protocol.UpdateRoomRequest{Version: archived.Version, Archived: &yes}, nil)
	for _, test := range []struct {
		kind string
		req  protocol.UpdateRoomOrderRequest
		want int
	}{
		{"unknown", protocol.UpdateRoomOrderRequest{}, 400},
		{"room", protocol.UpdateRoomOrderRequest{Version: -1}, 400},
		{"room", protocol.UpdateRoomOrderRequest{Version: 9_007_199_254_740_991}, 400},
		{"room", protocol.UpdateRoomOrderRequest{RoomIDs: []string{id, id}}, 400},
		{"room", protocol.UpdateRoomOrderRequest{RoomIDs: []string{""}}, 400},
		{"room", protocol.UpdateRoomOrderRequest{RoomIDs: []string{" " + id}}, 400},
		{"room", protocol.UpdateRoomOrderRequest{RoomIDs: []string{strings.Repeat("x", 129)}}, 400},
		{"room", protocol.UpdateRoomOrderRequest{RoomIDs: make([]string, 2001)}, 400},
		{"room", protocol.UpdateRoomOrderRequest{RoomIDs: []string{dm.ID}}, 400},
		{"room", protocol.UpdateRoomOrderRequest{RoomIDs: []string{archivedID}}, 400},
		{"dm", protocol.UpdateRoomOrderRequest{RoomIDs: []string{id}}, 400},
		{"room", protocol.UpdateRoomOrderRequest{RoomIDs: []string{"not-a-visible-room"}}, 404},
	} {
		var ae *apiError
		if err := e.c.do("PUT", "/v1/room-order/"+test.kind, test.req, nil); !errors.As(err, &ae) || ae.status != test.want {
			t.Fatalf("%s %+v: %v; want %d", test.kind, test.req, err, test.want)
		}
	}
	unsigned := *e.c
	unsigned.hc = &http.Client{}
	noCSRF := *e.c
	noCSRF.csrf = ""
	for _, test := range []struct {
		c    *client
		want int
	}{{&unsigned, 401}, {&noCSRF, 403}} {
		var ae *apiError
		if err := test.c.do("PUT", "/v1/room-order/room", protocol.UpdateRoomOrderRequest{RoomIDs: []string{id}}, nil); !errors.As(err, &ae) || ae.status != test.want {
			t.Fatalf("unauthorized order: %v; want %d", err, test.want)
		}
	}
	var order protocol.RoomOrder
	e.c.must("GET", "/v1/room-order", nil, &order)
	if order.Rooms.Version != 0 || order.DMs.Version != 0 {
		t.Fatalf("invalid requests changed order: %+v", order)
	}
}

func TestRoomOrderEventsAndRecordsArePersonal(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	member, err := store.GetUser(e.ctx, e.hub.Store().R(), e.c.boot.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	member.ID, member.Handle = "order-member", "order-member"
	member.CreatedAt = member.CreatedAt.Add(time.Second)
	id := e.roomID("Security")
	if err := e.hub.Store().Tx(e.ctx, func(tx *sql.Tx) error {
		if err := store.InsertUser(e.ctx, tx, member); err != nil {
			return err
		}
		_, err := store.AddMember(e.ctx, tx, id, protocol.Member{Kind: protocol.ActorUser, ID: member.ID})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: e.browser.URL, hc: &http.Client{Jar: jar}}
	c.must("POST", "/v1/session", map[string]string{"handle": member.Handle, "password": ownerPassword}, nil)
	c.must("GET", "/v1/bootstrap", nil, &c.boot)
	c.csrf = c.boot.CSRFToken
	var denied *apiError
	if err := c.do("PUT", "/v1/room-order/room", protocol.UpdateRoomOrderRequest{RoomIDs: []string{e.roomID("Engineering")}}, nil); !errors.As(err, &denied) || denied.status != 404 {
		t.Fatalf("room without membership accepted: %v", err)
	}
	cursor, err := store.MaxEventSeq(e.ctx, e.hub.Store().R())
	if err != nil {
		t.Fatal(err)
	}
	c.must("PUT", "/v1/room-order/room", protocol.UpdateRoomOrderRequest{RoomIDs: []string{id}}, nil)
	var owner protocol.RoomOrder
	e.c.must("GET", "/v1/room-order", nil, &owner)
	if owner.Rooms.Version != 0 || len(owner.Rooms.RoomIDs) != 0 {
		t.Fatalf("member preference changed owner's order: %+v", owner)
	}
	seesOrder := func(viewer *client) bool {
		t.Helper()
		ctx, cancel := context.WithTimeout(e.ctx, 2*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "GET", viewer.base+"/v1/events?cursor="+strconv.FormatInt(cursor, 10), nil)
		resp, err := viewer.hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		ready := false
		for scanner.Scan() {
			if scanner.Text() == "event: ready" {
				ready = true
			}
			if scanner.Text() == "event: room_order.updated" {
				return true
			}
		}
		if !ready || ctx.Err() == nil {
			t.Fatalf("event stream closed unexpectedly: %v", scanner.Err())
		}
		return false
	}
	if !seesOrder(c) || seesOrder(e.c) {
		t.Fatal("personal order event was missing or leaked to the workspace owner")
	}
	if err := e.hub.Store().Tx(e.ctx, func(tx *sql.Tx) error {
		_, err := store.RemoveMember(e.ctx, tx, id, protocol.Member{Kind: protocol.ActorUser, ID: member.ID})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(e.ctx, 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", c.base+"/v1/events?cursor="+strconv.FormatInt(cursor, 10), nil)
	resp, err := c.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	found := false
	for scanner.Scan() {
		if !strings.HasPrefix(scanner.Text(), "data: ") {
			continue
		}
		var event protocol.Event
		if json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "data: ")), &event) != nil || event.Type != "room_order.updated" {
			continue
		}
		var payload protocol.RoomOrderUpdated
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if event.Sequence <= cursor || payload.Order.Version != 1 || len(payload.Order.RoomIDs) != 0 {
			t.Fatalf("replay exposed removed membership or changed revision: %+v %+v", event, payload)
		}
		found = true
		break
	}
	if !found {
		t.Fatalf("missing projected personal event: %v", scanner.Err())
	}
	// Another workspace has a separate record and cannot order this room.
	other := newEnv(t, envOptions{})
	var ae *apiError
	if err := other.c.do("PUT", "/v1/room-order/room", protocol.UpdateRoomOrderRequest{RoomIDs: []string{id}}, nil); !errors.As(err, &ae) || ae.status != 404 {
		t.Fatalf("foreign workspace room accepted: %v", err)
	}
}
