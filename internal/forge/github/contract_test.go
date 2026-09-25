package github

// Contract test against a real GitHub repository. It is skipped unless
//
//	YIP_GITHUB_CONTRACT_REPO=owner/name   a disposable test repository
//	YIP_GITHUB_TOKEN=...                  a token with read access to it
//
// are set. Optional:
//
//	YIP_GITHUB_HOST=ghe.example.com       GitHub Enterprise Server host
//	YIP_GITHUB_CONTRACT_PR=12             PR to use (default: most recently
//	                                      updated open PR)
//	YIP_GITHUB_CONTRACT_WRITE=1           also publish a COMMENT review with a
//	                                      fresh marker (this leaves a visible
//	                                      review on the PR)
//
// By default it only reads. The one "write-shaped" call it always makes is a
// PublishReview against a deliberately wrong commit, which must stop at the
// stale check before any POST.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/forge"
)

func TestContractGitHub(t *testing.T) {
	repoEnv := os.Getenv("YIP_GITHUB_CONTRACT_REPO")
	token := os.Getenv("YIP_GITHUB_TOKEN")
	if repoEnv == "" || token == "" {
		t.Skip("set YIP_GITHUB_CONTRACT_REPO=owner/name and YIP_GITHUB_TOKEN to run the GitHub contract test")
	}
	owner, name, ok := strings.Cut(repoEnv, "/")
	if !ok {
		t.Fatalf("YIP_GITHUB_CONTRACT_REPO must be owner/name, got %q", repoEnv)
	}
	c := New(Options{
		Host:    os.Getenv("YIP_GITHUB_HOST"),
		Token:   func(context.Context) (string, error) { return token, nil },
		Timeout: 30 * time.Second,
	})
	repo := forge.RepoRef{Host: c.Host(), Owner: owner, Name: name}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	viewer, err := c.Viewer(ctx)
	if err != nil {
		t.Fatalf("Viewer: %v", err)
	}
	t.Logf("credential acts as %s (%s)", viewer.Login, viewer.Type)

	number := 0
	if s := os.Getenv("YIP_GITHUB_CONTRACT_PR"); s != "" {
		if number, err = strconv.Atoi(s); err != nil {
			t.Fatalf("YIP_GITHUB_CONTRACT_PR: %v", err)
		}
	} else {
		rp, _ := c.repoPath(repo)
		var open []struct {
			Number int `json:"number"`
		}
		q := url.Values{"state": {"open"}, "sort": {"updated"}, "direction": {"desc"}, "per_page": {"1"}}
		if err := c.getJSON(ctx, rp+"/pulls", q, &open); err != nil {
			t.Fatalf("list open PRs: %v", err)
		}
		if len(open) == 0 {
			t.Skip("test repository has no open pull request; open one or set YIP_GITHUB_CONTRACT_PR")
		}
		number = open[0].Number
	}

	pr, err := c.GetPR(ctx, repo, number)
	if err != nil {
		t.Fatalf("GetPR: %v", err)
	}
	sha := regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
	if !sha.MatchString(pr.Head) || !sha.MatchString(pr.Base) || pr.Author == "" || pr.BaseRef == "" || pr.HeadRef == "" {
		t.Fatalf("GetPR returned incomplete PR: %+v", pr)
	}
	if pr.State != "open" && pr.State != "closed" && pr.State != "merged" {
		t.Fatalf("GetPR state %q", pr.State)
	}
	gotRepo, gotN, err := c.ParseURL(pr.URL)
	if err != nil || gotN != number || !strings.EqualFold(gotRepo.Owner, pr.Repo.Owner) || !strings.EqualFold(gotRepo.Name, pr.Repo.Name) {
		t.Fatalf("ParseURL(%q) = %+v #%d, %v", pr.URL, gotRepo, gotN, err)
	}

	files, err := c.Files(ctx, repo, number)
	if err != nil && !errors.Is(err, forge.ErrStale) {
		t.Fatalf("Files: %v", err)
	}
	for _, f := range files {
		if f.Path == "" || f.Status == "" {
			t.Fatalf("Files returned incomplete entry %+v", f)
		}
	}

	checks, err := c.Checks(ctx, repo, pr.Head)
	if err != nil {
		t.Fatalf("Checks: %v", err)
	}
	switch checks.State {
	case ChecksSuccess, ChecksFailure, ChecksPending, ChecksNone, ChecksUnknown:
	default:
		t.Fatalf("Checks state %q", checks.State)
	}
	if checks.Total != checks.Passed+checks.Failed+checks.Pending {
		t.Fatalf("Checks counts inconsistent: %+v", checks)
	}

	reviews, err := c.Reviews(ctx, repo, number)
	if err != nil {
		t.Fatalf("Reviews: %v", err)
	}
	for _, r := range reviews {
		if r.ExternalID == "" || r.Actor == "" || r.State == "" || r.State == "PENDING" {
			t.Fatalf("Reviews returned %+v", r)
		}
	}

	ms, err := c.MergeStatus(ctx, repo, number)
	if err != nil {
		t.Fatalf("MergeStatus: %v", err)
	}
	switch ms.Mergeable {
	case MergeClean, MergeBlocked, MergeDirty, MergeBehind, MergeUnstable, MergeUnknown:
	default:
		t.Fatalf("MergeStatus mergeable %q", ms.Mergeable)
	}
	t.Logf("PR #%d %s head=%s checks=%+v merge=%+v reviews=%d files=%d", number, pr.State, pr.Head[:12], checks, ms, len(reviews), len(files))

	marker := "contract-" + randHex(t)
	if _, found, err := c.FindReviewByMarker(ctx, repo, number, marker); err != nil || found {
		t.Fatalf("FindReviewByMarker(fresh marker) = found %v, err %v", found, err)
	}

	// Stale publication must be refused before anything is posted.
	wrong := strings.Repeat("0", len(pr.Head))
	_, err = c.PublishReview(ctx, repo, number, forge.PublishReview{
		CommitID: wrong, Event: forge.EventComment, Body: "yip contract test (must not be posted)", Marker: marker,
	})
	if !errors.Is(err, forge.ErrStale) {
		t.Fatalf("PublishReview(stale commit) err = %v, want ErrStale", err)
	}

	if os.Getenv("YIP_GITHUB_CONTRACT_WRITE") != "1" {
		t.Log("read-only run; set YIP_GITHUB_CONTRACT_WRITE=1 to exercise publishing")
		return
	}
	if pr.State != "open" {
		t.Skipf("PR #%d is %s; publishing needs an open PR", number, pr.State)
	}

	if strings.EqualFold(viewer.Login, pr.Author) {
		_, err := c.PublishReview(ctx, repo, number, forge.PublishReview{
			CommitID: pr.Head, Event: forge.EventApprove, Body: "yip contract test (must not be posted)", Marker: marker + "-approve",
		})
		if !errors.Is(err, forge.ErrIneligible) {
			t.Fatalf("author APPROVE err = %v, want ErrIneligible", err)
		}
	}

	req := forge.PublishReview{
		CommitID: pr.Head,
		Event:    forge.EventComment,
		Body:     "yip GitHub connector contract test. This comment-only review is safe to ignore.",
		Marker:   marker,
	}
	id, err := c.PublishReview(ctx, repo, number, req)
	if err != nil {
		t.Fatalf("PublishReview(COMMENT): %v", err)
	}
	t.Logf("published review %s with marker %s", id, marker)

	found, ok, err := c.FindReviewByMarker(ctx, repo, number, marker)
	if err != nil || !ok || found != id {
		t.Fatalf("FindReviewByMarker = %q %v %v, want %s", found, ok, err, id)
	}
	again, err := c.PublishReview(ctx, repo, number, req)
	if err != nil || again != id {
		t.Fatalf("re-publishing the same marker = %q %v, want existing %s (no duplicate)", again, err, id)
	}
}

func randHex(t *testing.T) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
