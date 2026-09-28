package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/binbandit/yip/internal/forge"
)

func TestPublishReviewKeepsItsActorDuringCredentialRotation(t *testing.T) {
	var credentialReads atomic.Int32
	var postedActor atomic.Value
	postedActor.Store("")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		switch {
		case r.URL.Path == "/user":
			writeJSON(w, 200, ghUser{Login: actor})
		case r.URL.Path == "/repos/acme/widgets/pulls/7":
			writeJSON(w, 200, ghPull{Number: 7, State: "open", User: &ghUser{Login: "author"}, Head: ghBranch{SHA: shaA}, Base: ghBranch{SHA: shaBase}})
		case strings.HasSuffix(r.URL.Path, "/reviews") && r.Method == http.MethodGet:
			writeJSON(w, 200, []ghReview{})
		case strings.HasSuffix(r.URL.Path, "/reviews") && r.Method == http.MethodPost:
			var req ghCreateReview
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode publication: %v", err)
			}
			postedActor.Store(actor)
			writeJSON(w, 200, ghReview{ID: 81, User: &ghUser{Login: actor}, State: "APPROVED", CommitID: req.CommitID})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := New(Options{APIBase: server.URL, Token: func(context.Context) (string, error) {
		if credentialReads.Add(1) == 1 {
			return "reviewer-one", nil
		}
		return "reviewer-two", nil
	}})

	for i, wantActor := range []string{"reviewer-one", "reviewer-two"} {
		_, err := c.PublishReview(context.Background(), testRepoRef, 7, publishReq(forge.EventApprove, shaA, "rotation-"+wantActor))
		if err != nil {
			t.Fatal(err)
		}
		if actor := postedActor.Load().(string); actor != wantActor {
			t.Fatalf("credential changed after eligibility check: published as %s, checked %s", actor, wantActor)
		}
		if reads := credentialReads.Load(); reads != int32(i+1) {
			t.Fatalf("credential reads = %d, want one per publication (%d)", reads, i+1)
		}
	}
}

func TestMarkerReconciliationKeepsItsActorDuringCredentialRotation(t *testing.T) {
	var credentialReads atomic.Int32
	var listingActor atomic.Value
	listingActor.Store("")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if r.URL.Path == "/user" {
			writeJSON(w, 200, ghUser{Login: actor})
			return
		}
		listingActor.Store(actor)
		writeJSON(w, 200, []ghReview{{ID: 81, User: &ghUser{Login: "reviewer-one"}, State: "APPROVED", Body: MarkerComment("rotation")}})
	}))
	defer server.Close()
	c := New(Options{APIBase: server.URL, Token: func(context.Context) (string, error) {
		if credentialReads.Add(1) == 1 {
			return "reviewer-one", nil
		}
		return "reviewer-two", nil
	}})
	id, found, err := c.FindReviewByMarker(context.Background(), testRepoRef, 7, "rotation")
	if err != nil || !found || id != "81" {
		t.Fatalf("reconciliation: id=%q found=%v err=%v", id, found, err)
	}
	if actor := listingActor.Load().(string); actor != "reviewer-one" || credentialReads.Load() != 1 {
		t.Fatalf("reconciliation used a different actor from Viewer: actor=%s credential reads=%d", actor, credentialReads.Load())
	}
}
