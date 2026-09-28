package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/forge"
)

func publishReq(event forge.ReviewEvent, commit, marker string) forge.PublishReview {
	return forge.PublishReview{
		CommitID: commit,
		Event:    event,
		Body:     "Oren reviewed this revision.",
		Comments: []forge.ReviewComment{{Path: "widgets/spin.go", Line: 12, Body: "Off-by-one here."}},
		Marker:   marker,
	}
}

func TestPublishReviewSuccess(t *testing.T) {
	f := newFake(t)
	f.addPull(&fakePull{Number: 7, Author: "mira", Head: shaA})
	c := f.connector()
	id, err := c.PublishReview(context.Background(), testRepoRef, 7, publishReq(forge.EventApprove, shaA, "rv_01J8ZK.round-2"))
	if err != nil {
		t.Fatal(err)
	}
	if id != "81" {
		t.Fatalf("external ID = %q, want 81", id)
	}
	p := f.lastPost
	if p["commit_id"] != shaA || p["event"] != "APPROVE" {
		t.Fatalf("POST body = %v", p)
	}
	if body := p["body"].(string); body != "Oren reviewed this revision.\n\n<!-- yip-review:rv_01J8ZK.round-2 -->" {
		t.Fatalf("POST body text = %q", body)
	}
	cm := p["comments"].([]any)
	if len(cm) != 1 {
		t.Fatalf("comments = %v", cm)
	}
	c0 := cm[0].(map[string]any)
	if c0["path"] != "widgets/spin.go" || c0["line"] != float64(12) || c0["side"] != "RIGHT" || c0["body"] != "Off-by-one here." {
		t.Fatalf("comment = %v", c0)
	}

	// Retrying the same publication is idempotent: found by marker, no second POST.
	id2, err := c.PublishReview(context.Background(), testRepoRef, 7, publishReq(forge.EventApprove, shaA, "rv_01J8ZK.round-2"))
	if err != nil || id2 != id || f.postCount() != 1 {
		t.Fatalf("retry: id %q err %v posts %d", id2, err, f.postCount())
	}
}

func TestPublishReviewStale(t *testing.T) {
	f := newFake(t)
	f.addPull(&fakePull{Number: 7, Author: "mira", Head: shaB})
	f.addPull(&fakePull{Number: 8, Author: "mira", Head: shaA, State: "closed", Merged: true})
	f.addPull(&fakePull{Number: 9, Author: "mira", Head: shaA, State: "closed"})
	c := f.connector()
	for _, n := range []int{7, 8, 9} {
		_, err := c.PublishReview(context.Background(), testRepoRef, n, publishReq(forge.EventComment, shaA, "m1"))
		if !errors.Is(err, forge.ErrStale) {
			t.Errorf("PR #%d: err = %v, want ErrStale", n, err)
		}
	}
	if f.postCount() != 0 {
		t.Fatalf("POSTs = %d, want 0", f.postCount())
	}
}

func TestPublishReviewAuthorIneligible(t *testing.T) {
	f := newFake(t)
	f.viewer = "mira"
	f.addPull(&fakePull{Number: 7, Author: "Mira", Head: shaA}) // logins are case-insensitive
	c := f.connector()
	for _, ev := range []forge.ReviewEvent{forge.EventApprove, forge.EventRequestChanges} {
		_, err := c.PublishReview(context.Background(), testRepoRef, 7, publishReq(ev, shaA, "m-"+string(ev)))
		if !errors.Is(err, forge.ErrIneligible) {
			t.Fatalf("%s by author: err = %v, want ErrIneligible", ev, err)
		}
	}
	if f.postCount() != 0 {
		t.Fatalf("POSTs = %d, want 0: nothing may be posted for an ineligible actor", f.postCount())
	}
	// Commenting on your own PR is allowed.
	if _, err := c.PublishReview(context.Background(), testRepoRef, 7, publishReq(forge.EventComment, shaA, "m-comment")); err != nil {
		t.Fatalf("author COMMENT: %v", err)
	}
	if f.postCount() != 1 {
		t.Fatalf("POSTs = %d, want 1", f.postCount())
	}
}

func TestPublishReview422SelfApproval(t *testing.T) {
	for _, msg := range []string{"Can not approve your own pull request", "Can not request changes on your own pull request", "GitHub Actions is not permitted to approve pull requests."} {
		f := newFake(t)
		f.addPull(&fakePull{Number: 7, Author: "mira", Head: shaA})
		f.hooks["POST /repos/acme/widgets/pulls/7/reviews"] = func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 422, map[string]any{
				"message":           "Unprocessable Entity",
				"errors":            []string{msg},
				"documentation_url": "https://docs.github.com/rest/pulls/reviews#create-a-review-for-a-pull-request",
				"status":            "422",
			})
		}
		_, err := f.connector().PublishReview(context.Background(), testRepoRef, 7, publishReq(forge.EventApprove, shaA, "m1"))
		if !errors.Is(err, forge.ErrIneligible) || errors.Is(err, forge.ErrAmbiguous) {
			t.Fatalf("%q: err = %v, want ErrIneligible", msg, err)
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != 422 || !strings.Contains(err.Error(), msg) {
			t.Fatalf("%q: err = %#v", msg, err)
		}
	}
}

func TestPublishReviewForbiddenAndNotFound(t *testing.T) {
	cases := []struct {
		status int
		msg    string
		want   error
	}{
		{403, "Resource not accessible by personal access token", forge.ErrForbidden},
		{404, "Not Found", forge.ErrNotFound},
	}
	for _, tc := range cases {
		f := newFake(t)
		f.addPull(&fakePull{Number: 7, Author: "mira", Head: shaA})
		f.hooks["POST /repos/acme/widgets/pulls/7/reviews"] = func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, tc.status, map[string]any{"message": tc.msg})
		}
		_, err := f.connector().PublishReview(context.Background(), testRepoRef, 7, publishReq(forge.EventComment, shaA, "m1"))
		if !errors.Is(err, tc.want) || errors.Is(err, forge.ErrAmbiguous) {
			t.Fatalf("HTTP %d: err = %v, want %v", tc.status, err, tc.want)
		}
	}
}

func TestPublishReviewTimeoutIsAmbiguousThenReconciled(t *testing.T) {
	f := newFake(t)
	f.addPull(&fakePull{Number: 7, Author: "mira", Head: shaA})
	var createdID atomic.Int64
	// GitHub accepts the review, but the response never arrives in time.
	f.hooks["POST /repos/acme/widgets/pulls/7/reviews"] = func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Errorf("decode POST: %v", err)
		}
		text := in.Body
		createdID.Store(f.addReview(7, f.viewer, "APPROVED", shaA, text))
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}
	c := f.connector(func(o *Options) { o.Timeout = 150 * time.Millisecond })
	req := publishReq(forge.EventApprove, shaA, "rv_9.r1")
	_, err := c.PublishReview(context.Background(), testRepoRef, 7, req)
	if !errors.Is(err, forge.ErrAmbiguous) {
		t.Fatalf("err = %v, want ErrAmbiguous", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the timeout cause preserved", err)
	}

	id, found, err := c.FindReviewByMarker(context.Background(), testRepoRef, 7, "rv_9.r1")
	if err != nil || !found || id != itoa(createdID.Load()) {
		t.Fatalf("FindReviewByMarker = %q %v %v, want %d", id, found, err, createdID.Load())
	}
	if _, found, err := c.FindReviewByMarker(context.Background(), testRepoRef, 7, "rv_9.r2"); err != nil || found {
		t.Fatalf("unrelated marker: found=%v err=%v", found, err)
	}

	// A retry after the ambiguous failure reuses the existing review.
	id2, err := c.PublishReview(context.Background(), testRepoRef, 7, req)
	if err != nil || id2 != id {
		t.Fatalf("retry: id %q err %v", id2, err)
	}
	if n := f.count("POST /repos/acme/widgets/pulls/7/reviews"); n != 1 {
		t.Fatalf("POSTs = %d, want 1 (no duplicate review)", n)
	}
}

func TestPublishReview5xxIsAmbiguous(t *testing.T) {
	f := newFake(t)
	f.addPull(&fakePull{Number: 7, Author: "mira", Head: shaA})
	f.hooks["POST /repos/acme/widgets/pulls/7/reviews"] = func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 502, map[string]any{"message": "Server Error"})
	}
	_, err := f.connector().PublishReview(context.Background(), testRepoRef, 7, publishReq(forge.EventComment, shaA, "m1"))
	if !errors.Is(err, forge.ErrAmbiguous) {
		t.Fatalf("err = %v, want ErrAmbiguous", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 502 {
		t.Fatalf("err = %#v", err)
	}
}

func TestPublishReviewConnectionDroppedAfterSend(t *testing.T) {
	f := newFake(t)
	f.addPull(&fakePull{Number: 7, Author: "mira", Head: shaA})
	f.hooks["POST /repos/acme/widgets/pulls/7/reviews"] = func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("cannot hijack")
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	}
	_, err := f.connector().PublishReview(context.Background(), testRepoRef, 7, publishReq(forge.EventComment, shaA, "m1"))
	if !errors.Is(err, forge.ErrAmbiguous) {
		t.Fatalf("err = %v, want ErrAmbiguous", err)
	}
}

func TestFindReviewByMarkerIgnoresOtherActors(t *testing.T) {
	f := newFake(t)
	f.addPull(&fakePull{Number: 7, Author: "mira", Head: shaA})
	f.addReview(7, "mallory", "APPROVED", shaA, "copied "+MarkerComment("rv_1"))
	id := f.addReview(7, f.viewer, "COMMENTED", shaA, "real\n\n"+MarkerComment("rv_2"))
	c := f.connector()
	if _, found, err := c.FindReviewByMarker(context.Background(), testRepoRef, 7, "rv_1"); err != nil || found {
		t.Fatalf("marker in another actor's review: found=%v err=%v", found, err)
	}
	got, found, err := c.FindReviewByMarker(context.Background(), testRepoRef, 7, "rv_2")
	if err != nil || !found || got != itoa(id) {
		t.Fatalf("got %q %v %v", got, found, err)
	}
	// A marker that is a prefix of another must not match it.
	if _, found, _ := c.FindReviewByMarker(context.Background(), testRepoRef, 7, "rv"); found {
		t.Fatal("prefix marker matched")
	}
}

func TestPendingReviewCannotConfirmPublication(t *testing.T) {
	f := newFake(t)
	f.addPull(&fakePull{Number: 7, Author: "mira", Head: shaA})
	f.addReview(7, f.viewer, "PENDING", shaA, MarkerComment("draft-marker"))
	f.hooks["POST /repos/acme/widgets/pulls/7/reviews"] = func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 422, map[string]any{"message": "Validation Failed", "errors": []string{"User can only have one pending review per pull request"}})
	}
	c := f.connector()
	if id, found, err := c.FindReviewByMarker(context.Background(), testRepoRef, 7, "draft-marker"); err != nil || found || id != "" {
		t.Fatalf("unsubmitted draft confirmed as publication: id=%q found=%v err=%v", id, found, err)
	}
	if id, err := c.PublishReview(context.Background(), testRepoRef, 7, publishReq(forge.EventApprove, shaA, "draft-marker")); err == nil || id != "" || errors.Is(err, forge.ErrAmbiguous) {
		t.Fatalf("pending-review conflict must remain a rejected publication: id=%q err=%v", id, err)
	}
	if got := f.count("POST /repos/acme/widgets/pulls/7/reviews"); got != 1 {
		t.Fatalf("publication requests = %d, want 1", got)
	}
}

func TestDismissedReviewStillConfirmsPastPublication(t *testing.T) {
	f := newFake(t)
	f.addPull(&fakePull{Number: 7, Author: "mira", Head: shaB})
	id := f.addReview(7, f.viewer, "DISMISSED", shaA, MarkerComment("dismissed-marker"))
	c := f.connector()
	got, err := c.PublishReview(context.Background(), testRepoRef, 7, publishReq(forge.EventApprove, shaA, "dismissed-marker"))
	if err != nil || got != itoa(id) || f.postCount() != 0 {
		t.Fatalf("dismissal must not cause duplicate publication: id=%q err=%v posts=%d", got, err, f.postCount())
	}
}

func TestPublishReviewValidation(t *testing.T) {
	f := newFake(t)
	f.addPull(&fakePull{Number: 7, Author: "mira", Head: shaA})
	c := f.connector()
	bad := []forge.PublishReview{
		publishReq(forge.EventApprove, shaA, ""),
		publishReq(forge.EventApprove, shaA, "a--b"),
		publishReq(forge.EventApprove, shaA, "x -->"),
		publishReq("DISMISS", shaA, "m"),
		publishReq(forge.EventApprove, "", "m"),
		{CommitID: shaA, Event: forge.EventComment, Marker: "m", Comments: []forge.ReviewComment{{Path: "a.go", Line: 0, Body: "x"}}},
	}
	for i, req := range bad {
		if _, err := c.PublishReview(context.Background(), testRepoRef, 7, req); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
	if f.postCount() != 0 || f.count("GET /user") != 0 {
		t.Fatal("invalid requests must fail before any API call")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
