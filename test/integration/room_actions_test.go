package integration

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"reflect"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

func TestRoomRenameRejectsBlankNames(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	path := "/v1/rooms/" + e.roomID("Engineering")
	var before protocol.Room
	e.c.must("GET", path, nil, &before)
	for _, name := range []string{"", " \t\n", "\u2003\u00a0"} {
		var ae *apiError
		err := e.c.do("PATCH", path, protocol.UpdateRoomRequest{Version: before.Version, Name: &name}, nil)
		if !errors.As(err, &ae) || ae.status != http.StatusBadRequest {
			t.Fatalf("blank name %q: %v; want 400", name, err)
		}
		var after protocol.Room
		e.c.must("GET", path, nil, &after)
		if after.Name != before.Name || after.Version != before.Version {
			t.Fatalf("invalid rename changed the room: %+v", after)
		}
	}
	name := "  Renamed room \t"
	var renamed protocol.Room
	e.c.must("PATCH", path, protocol.UpdateRoomRequest{Version: before.Version, Name: &name}, &renamed)
	if renamed.Name != "Renamed room" || renamed.Version != before.Version+1 {
		t.Fatalf("rename should trim and advance the version: %+v", renamed)
	}
	var ae *apiError
	if err := e.c.do("PATCH", path, protocol.UpdateRoomRequest{Version: before.Version, Name: &name}, nil); !errors.As(err, &ae) || ae.status != http.StatusConflict {
		t.Fatalf("stale rename: %v; want 409", err)
	}
}

func TestRoomActionsUseMembershipAndKeepOngoingWork(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	n := e.connectNode("Room actions fixture")
	n.createWork(e, "Security", "Mira", "room archive test", codeWork("Keep ongoing work"))
	run := n.startJob(e, "Keep ongoing work")
	before, err := store.GetRun(e.ctx, e.hub.Store().R(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	history := e.jobDetail(before.JobID)
	member, err := store.GetUser(e.ctx, e.hub.Store().R(), e.c.boot.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	member.ID, member.Handle = "room-member", "room-member"
	member.CreatedAt = member.CreatedAt.Add(time.Second)
	if err := e.hub.Store().Tx(e.ctx, func(tx *sql.Tx) error { return store.InsertUser(e.ctx, tx, member) }); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: e.browser.URL, hc: &http.Client{Jar: jar}}
	c.must("POST", "/v1/session", map[string]string{"handle": member.Handle, "password": ownerPassword}, nil)
	c.must("GET", "/v1/bootstrap", nil, &c.boot)
	c.csrf = c.boot.CSRFToken
	roomID := e.roomID("Security")
	path := "/v1/rooms/" + roomID
	var room protocol.Room
	e.c.must("GET", path, nil, &room)
	name, archived := "Renamed by a member", true
	for _, req := range []protocol.UpdateRoomRequest{{Version: room.Version, Name: &name}, {Version: room.Version, Archived: &archived}} {
		var ae *apiError
		if err := c.do("PATCH", path, req, nil); !errors.As(err, &ae) || ae.status != http.StatusNotFound {
			t.Fatalf("non-member update: %v; want 404", err)
		}
	}
	if err := e.hub.Store().Tx(e.ctx, func(tx *sql.Tx) error {
		_, err := store.AddMember(e.ctx, tx, roomID, protocol.Member{Kind: protocol.ActorUser, ID: member.ID})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	c.must("GET", path, nil, &room)
	c.must("PATCH", path, protocol.UpdateRoomRequest{Version: room.Version, Name: &name}, &room)
	c.must("PATCH", path, protocol.UpdateRoomRequest{Version: room.Version, Archived: &archived}, &room)
	if room.Name != name || !room.Archived {
		t.Fatalf("member's room actions were not applied: %+v", room)
	}
	after, err := store.GetRun(e.ctx, e.hub.Store().R(), run.RunID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("archive changed ongoing work: %+v %v", after, err)
	}
	if !reflect.DeepEqual(history, e.jobDetail(before.JobID)) {
		t.Fatal("archive changed job history")
	}
	var messages protocol.MessagePage
	c.must("GET", path+"/messages", nil, &messages)
	if len(messages.Messages) == 0 {
		t.Fatal("archive lost room messages")
	}
	var visible []protocol.Room
	c.must("GET", "/v1/rooms", nil, &visible)
	for _, r := range visible {
		if r.ID == roomID {
			t.Fatal("archived room remains in the list")
		}
	}
}
