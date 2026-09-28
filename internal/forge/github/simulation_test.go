package github

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/forge"
)

// TestSimulationGitHub checks a deliberately prepared playground PR. It never
// contacts GitHub unless REPO, PR and TOKEN are all supplied. Publishing also
// requires an explicit SIM_EVENT; otherwise every request is read-only.
func TestSimulationGitHub(t *testing.T) {
	repoName := os.Getenv("YIP_GITHUB_REPO")
	prNumber := os.Getenv("YIP_GITHUB_PR")
	token := os.Getenv("YIP_GITHUB_TOKEN")
	if repoName == "" || prNumber == "" || token == "" {
		t.Skip("set YIP_GITHUB_REPO, YIP_GITHUB_PR and YIP_GITHUB_TOKEN for a prepared playground PR")
	}
	owner, name, ok := strings.Cut(repoName, "/")
	number, numberErr := strconv.Atoi(prNumber)
	if !ok || !validRepoName(owner, name) || numberErr != nil || number <= 0 {
		t.Fatal("YIP_GITHUB_REPO must be owner/name and YIP_GITHUB_PR must be a positive integer")
	}
	env := func(key string) string { return os.Getenv("YIP_GITHUB_SIM_" + key) }
	event := forge.ReviewEvent(env("EVENT"))
	expectedError := env("EXPECT_ERROR")
	if event != "" && event != forge.EventComment && event != forge.EventApprove && event != forge.EventRequestChanges {
		t.Fatalf("unsupported simulation event %q", event)
	}
	wantErrors := map[string]error{
		"stale": forge.ErrStale, "ineligible": forge.ErrIneligible,
		"forbidden": forge.ErrForbidden, "not_found": forge.ErrNotFound,
	}
	if expectedError != "" && (event == "" || (wantErrors[expectedError] == nil && expectedError != "validation")) {
		t.Fatalf("EXPECT_ERROR requires EVENT and stale, ineligible, forbidden, not_found or validation; got %q", expectedError)
	}
	if s := env("EXPECT_DRAFT"); s != "" && s != "true" && s != "false" {
		t.Fatalf("EXPECT_DRAFT must be true or false, got %q", s)
	}
	marker := env("MARKER")
	if marker == "" && event != "" {
		marker = "simulation-" + randHex(t)
	}
	if marker != "" {
		if err := validMarker(marker); err != nil {
			t.Fatal(err)
		}
	}
	if s := env("COMMIT"); s != "" && !shaRe.MatchString(s) {
		t.Fatalf("invalid simulation commit %q", s)
	}

	c := New(Options{
		Host:  os.Getenv("YIP_GITHUB_HOST"),
		Token: func(context.Context) (string, error) { return token, nil },
	})
	repo := forge.RepoRef{Host: c.Host(), Owner: owner, Name: name}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	viewer, err := c.Viewer(ctx)
	if err != nil {
		t.Fatalf("Viewer: %v", err)
	}
	pr, err := c.GetPR(ctx, repo, number)
	if err != nil {
		t.Fatalf("GetPR: %v", err)
	}
	checks, err := c.Checks(ctx, repo, pr.Head)
	if err != nil {
		t.Fatalf("Checks: %v", err)
	}
	merge, err := c.MergeStatus(ctx, repo, number)
	if err != nil {
		t.Fatalf("MergeStatus: %v", err)
	}
	files, err := c.FilesAt(ctx, repo, number, pr.Head)
	if err != nil {
		t.Fatalf("FilesAt: %v", err)
	}
	reviews, err := c.Reviews(ctx, repo, number)
	if err != nil {
		t.Fatalf("Reviews: %v", err)
	}
	t.Logf("viewer=%s repo=%s PR=%d author=%s state=%s draft=%v head=%s checks=%+v merge=%+v files=%d reviews=%d",
		viewer.Login, repoName, number, pr.Author, pr.State, pr.Draft, pr.Head, checks, merge, len(files), len(reviews))
	for _, review := range reviews {
		t.Logf("review id=%s actor=%s state=%s commit=%s", review.ExternalID, review.Actor, review.State, review.CommitID)
	}
	for key, got := range map[string]string{
		"EXPECT_CHECKS": checks.State, "EXPECT_MERGE": merge.Mergeable,
		"EXPECT_AUTHOR": pr.Author, "EXPECT_STATE": pr.State,
		"EXPECT_DRAFT": strconv.FormatBool(pr.Draft), "EXPECT_HEAD": pr.Head,
	} {
		if want := env(key); want != "" && got != want {
			t.Errorf("%s: got %q, want %q", key, got, want)
		}
	}
	if checks.Total != checks.Passed+checks.Failed+checks.Pending {
		t.Errorf("inconsistent check counts: %+v", checks)
	}
	if absent := env("EXPECT_MARKER_ABSENT"); absent != "" {
		id, found, err := c.FindReviewByMarker(ctx, repo, number, absent)
		if err != nil || found || id != "" {
			t.Errorf("marker %q must not confirm publication: id=%q found=%v err=%v", absent, id, found, err)
		}
	}
	if event == "" || t.Failed() {
		return
	}
	commit := env("COMMIT")
	if commit == "" {
		commit = pr.Head
	}
	req := forge.PublishReview{
		CommitID: commit, Event: event, Marker: marker,
		Body: "yip playground simulation: " + string(event) + ". This is a controlled connector test, not a human review of production work.",
	}
	id, err := c.PublishReview(ctx, repo, number, req)
	if expectedError != "" {
		matches := errors.Is(err, wantErrors[expectedError])
		if expectedError == "validation" {
			var apiErr *APIError
			matches = errors.As(err, &apiErr) && apiErr.StatusCode == 422
		}
		if !matches || id != "" || errors.Is(err, forge.ErrAmbiguous) {
			t.Fatalf("PublishReview(%s): id=%q err=%v, want definite %s", event, id, err, expectedError)
		}
		t.Logf("publication correctly refused: %s", expectedError)
		return
	}
	if err != nil || id == "" {
		t.Fatalf("PublishReview(%s): id=%q err=%v", event, id, err)
	}
	foundID, found, err := c.FindReviewByMarker(ctx, repo, number, marker)
	if err != nil || !found || foundID != id {
		t.Fatalf("FindReviewByMarker: id=%q found=%v err=%v, want %q", foundID, found, err, id)
	}
	again, err := c.PublishReview(ctx, repo, number, req)
	if err != nil || again != id {
		t.Fatalf("idempotent publication: id=%q err=%v, want existing %q", again, err, id)
	}
	t.Logf("published and reconciled %s review %s with marker %s; retry reused the same review", event, id, marker)
}
