package integration

import (
	"database/sql"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

func TestOverviewRefreshPreservesVisitWindow(t *testing.T) {
	e := newEnv(t, envOptions{noRunner: true})
	var first protocol.Overview
	e.c.must("GET", "/v1/overview", nil, &first)
	if first.Since == nil {
		t.Fatal("first visit needs a concrete baseline for subsequent refreshes")
	}
	// A persisted assignment from before this visit must stay in catch-up
	// after seen advances, with its newest state on every refresh.
	e.post("Engineering", "@Mira check Atlas", []string{"mira"}, nil)
	job := e.jobsWithReplies()[0]
	if err := e.hub.Store().Tx(e.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(e.ctx, "UPDATE jobs SET kind = 'investigation', state = 'running' WHERE id = ?", job.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.c.must("POST", "/v1/overview/seen", nil, nil)
	path := "/v1/overview?since=" + url.QueryEscape(first.Since.Format(time.RFC3339Nano))
	var refreshed protocol.Overview
	e.c.must("GET", path, nil, &refreshed)
	if refreshed.Since == nil || !refreshed.Since.Equal(*first.Since) {
		t.Fatalf("refresh moved this visit's window: %v -> %v", first.Since, refreshed.Since)
	}
	if len(refreshed.Catchup) != 1 || refreshed.Catchup[0].Kind != "active" {
		t.Fatalf("refresh erased or replayed stale work: %+v", refreshed.Catchup)
	}
	if err := e.hub.Store().Tx(e.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(e.ctx, "UPDATE jobs SET state = 'completed', summary = 'Checked the cache', completed_at = ? WHERE id = ?", store.TS(time.Now()), job.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.c.must("GET", path, nil, &refreshed)
	if len(refreshed.Catchup) != 1 || refreshed.Catchup[0].Kind != "completed" || refreshed.Catchup[0].Detail != "Checked the cache" {
		t.Fatalf("catch-up did not reconcile to completion: %+v", refreshed.Catchup)
	}
	for _, since := range []string{"invalid", time.Now().Add(time.Hour).Format(time.RFC3339Nano)} {
		if err := e.c.do("GET", "/v1/overview?since="+url.QueryEscape(since), nil, nil); err == nil {
			t.Fatalf("accepted invalid catch-up window %q", since)
		} else if ae, ok := err.(*apiError); !ok || ae.status != 400 {
			t.Fatalf("invalid window returned unexpected error: %v", err)
		}
	}
}

func TestOverviewDoesNotPretendToAnswerArbitraryQuestions(t *testing.T) {
	e := newEnv(t, envOptions{noRunner: true})
	room := e.roomID("Overview")
	err := e.c.do("POST", "/v1/rooms/"+room+"/messages", protocol.PostMessageRequest{
		Body: "What decisions have we made about Atlas?", ClientKey: "specific-question",
	}, nil)
	if ae, ok := err.(*apiError); !ok || ae.status != 400 || !strings.Contains(ae.Message, "message an engineer") {
		t.Fatalf("arbitrary question got an unrelated status answer: %v", err)
	}
	var page protocol.MessagePage
	e.c.must("GET", "/v1/rooms/"+room+"/messages", nil, &page)
	if len(page.Messages) != 0 {
		t.Fatalf("rejected question left a misleading transcript: %+v", page.Messages)
	}
	e.c.must("POST", "/v1/rooms/"+room+"/messages", protocol.PostMessageRequest{
		Body: "Where are we with everything?", ClientKey: "explicit-summary",
	}, nil)
	e.c.must("GET", "/v1/rooms/"+room+"/messages", nil, &page)
	if len(page.Messages) != 2 {
		t.Fatalf("summary action did not retain request and sourced result: %+v", page.Messages)
	}
}
