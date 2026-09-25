package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/forge"
)

const (
	testToken = "test-token"
	testOwner = "acme"
	testRepo  = "widgets"
	shaA      = "6dcb09b5b57875f334f61aebed695e2e4193db5e"
	shaB      = "e5bd3914e2e596debea16f433f57875b5b90bcd6"
	shaBase   = "f1e2d3c4b5a697887766554433221100ffeeddcc"
)

// fakePull is the mutable state of one fake pull request.
type fakePull struct {
	Number         int
	Title          string
	State          string // open | closed
	Author         string
	Head, Base     string
	HeadRef        string
	BaseRef        string
	Draft          bool
	Merged         bool
	Mergeable      *bool
	MergeableState string
	ChangedFiles   int // defaults to len(files)
}

// fakeGitHub emulates the subset of the GitHub REST API the connector uses.
// Handlers are keyed "METHOD /path" in hooks and may take over a route.
type fakeGitHub struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	viewer    string
	pulls     map[int]*fakePull
	files     map[int][]map[string]any
	reviews   map[int][]map[string]any
	checkRuns map[string][]map[string]any
	statuses  map[string][]map[string]any
	pageSize  int
	nextID    int64
	posts     int
	lastPost  map[string]any
	counts    map[string]int
	hooks     map[string]http.HandlerFunc
	// onPull runs before a pull is served, e.g. to move the head.
	onPull func(n int, calls int, p *fakePull)
}

func newFake(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{
		t:         t,
		viewer:    "oren-bot",
		pulls:     map[int]*fakePull{},
		files:     map[int][]map[string]any{},
		reviews:   map[int][]map[string]any{},
		checkRuns: map[string][]map[string]any{},
		statuses:  map[string][]map[string]any{},
		pageSize:  2,
		nextID:    80,
		counts:    map[string]int{},
		hooks:     map[string]http.HandlerFunc{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) connector(opts ...func(*Options)) *Connector {
	o := Options{
		APIBase:               f.srv.URL,
		Token:                 func(context.Context) (string, error) { return testToken, nil },
		Timeout:               2 * time.Second,
		MergeablePollInterval: time.Millisecond,
	}
	for _, fn := range opts {
		fn(&o)
	}
	return New(o)
}

func (f *fakeGitHub) addPull(p *fakePull) *fakePull {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p.State == "" {
		p.State = "open"
	}
	if p.HeadRef == "" {
		p.HeadRef = "feature"
	}
	if p.BaseRef == "" {
		p.BaseRef = "main"
	}
	if p.Base == "" {
		p.Base = shaBase
	}
	f.pulls[p.Number] = p
	return p
}

func (f *fakeGitHub) addReview(n int, login, state, commit, body string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addReviewLocked(n, login, state, commit, body)
}

func (f *fakeGitHub) addReviewLocked(n int, login, state, commit, body string) int64 {
	f.nextID++
	id := f.nextID
	r := map[string]any{
		"id":                 id,
		"node_id":            fmt.Sprintf("PRR_kwDO%d", id),
		"user":               userJSON(login),
		"body":               body,
		"state":              state,
		"html_url":           fmt.Sprintf("https://github.com/%s/%s/pull/%d#pullrequestreview-%d", testOwner, testRepo, n, id),
		"pull_request_url":   fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", testOwner, testRepo, n),
		"author_association": "COLLABORATOR",
		"commit_id":          commit,
		"_links":             map[string]any{},
	}
	if state != "PENDING" {
		r["submitted_at"] = "2026-09-20T10:15:00Z"
	}
	if login == "" {
		r["user"] = nil
	}
	f.reviews[n] = append(f.reviews[n], r)
	return id
}

func (f *fakeGitHub) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts[key]
}

func (f *fakeGitHub) postCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.posts
}

func userJSON(login string) map[string]any {
	return map[string]any{
		"login":      login,
		"id":         1000 + len(login),
		"node_id":    "MDQ6VXNlcjE=",
		"avatar_url": "https://avatars.githubusercontent.com/u/1?v=4",
		"html_url":   "https://github.com/" + login,
		"type":       "User",
		"site_admin": false,
	}
}

func repoJSON() map[string]any {
	return map[string]any{
		"id":        1296269,
		"node_id":   "MDEwOlJlcG9zaXRvcnkxMjk2MjY5",
		"name":      testRepo,
		"full_name": testOwner + "/" + testRepo,
		"owner":     userJSON(testOwner),
		"private":   false,
		"html_url":  "https://github.com/" + testOwner + "/" + testRepo,
	}
}

func (f *fakeGitHub) pullJSON(p *fakePull) map[string]any {
	changed := p.ChangedFiles
	if changed == 0 {
		changed = len(f.files[p.Number])
	}
	var mergedAt, closedAt any
	if p.Merged {
		mergedAt = "2026-09-21T09:00:00Z"
	}
	if p.State == "closed" {
		closedAt = "2026-09-21T09:00:00Z"
	}
	var mergeable any
	if p.Mergeable != nil {
		mergeable = *p.Mergeable
	}
	ms := p.MergeableState
	if ms == "" {
		ms = "unknown"
	}
	return map[string]any{
		"url":              fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", testOwner, testRepo, p.Number),
		"id":               1000000 + p.Number,
		"node_id":          "MDExOlB1bGxSZXF1ZXN0MQ==",
		"html_url":         fmt.Sprintf("https://github.com/%s/%s/pull/%d", testOwner, testRepo, p.Number),
		"number":           p.Number,
		"state":            p.State,
		"locked":           false,
		"title":            p.Title,
		"user":             userJSON(p.Author),
		"body":             "Please pull these awesome changes in!",
		"created_at":       "2026-09-19T08:00:00Z",
		"updated_at":       "2026-09-20T11:30:00Z",
		"closed_at":        closedAt,
		"merged_at":        mergedAt,
		"merge_commit_sha": nil,
		"draft":            p.Draft,
		"head": map[string]any{
			"label": testOwner + ":" + p.HeadRef, "ref": p.HeadRef, "sha": p.Head,
			"user": userJSON(testOwner), "repo": repoJSON(),
		},
		"base": map[string]any{
			"label": testOwner + ":" + p.BaseRef, "ref": p.BaseRef, "sha": p.Base,
			"user": userJSON(testOwner), "repo": repoJSON(),
		},
		"author_association": "MEMBER",
		"merged":             p.Merged,
		"mergeable":          mergeable,
		"rebaseable":         mergeable,
		"mergeable_state":    ms,
		"merged_by":          nil,
		"comments":           0,
		"review_comments":    0,
		"commits":            1,
		"additions":          10,
		"deletions":          2,
		"changed_files":      changed,
	}
}

var (
	rePull      = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/pulls/(\d+)$`)
	rePullFiles = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/pulls/(\d+)/files$`)
	rePullRevs  = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/pulls/(\d+)/reviews$`)
	reRuns      = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/commits/([0-9a-f]+)/check-runs$`)
	reStatus    = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/commits/([0-9a-f]+)/status$`)
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-GitHub-Request-Id", "CAFE:0001:0002")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func notFound(w http.ResponseWriter) {
	writeJSON(w, 404, map[string]any{
		"message":           "Not Found",
		"documentation_url": "https://docs.github.com/rest",
		"status":            "404",
	})
}

// page slices items for the request's ?page= and sets a Link header.
func (f *fakeGitHub) page(w http.ResponseWriter, r *http.Request, n int) (lo, hi int) {
	pg, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if pg < 1 {
		pg = 1
	}
	size := f.pageSize
	lo = min((pg-1)*size, n)
	hi = min(lo+size, n)
	last := max(1, (n+size-1)/size)
	link := func(p int, rel string) string {
		q := r.URL.Query()
		q.Set("page", strconv.Itoa(p))
		return fmt.Sprintf(`<%s%s?%s>; rel="%s"`, f.srv.URL, r.URL.Path, q.Encode(), rel)
	}
	var links []string
	if pg < last {
		links = append(links, link(pg+1, "next"), link(last, "last"))
	}
	if pg > 1 {
		links = append(links, link(1, "first"), link(pg-1, "prev"))
	}
	if len(links) > 0 {
		w.Header().Set("Link", strings.Join(links, ", "))
	}
	return lo, hi
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	t := f.t
	if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
		t.Errorf("%s %s: Accept = %q", r.Method, r.URL.Path, got)
	}
	if got := r.Header.Get("X-GitHub-Api-Version"); got != APIVersion {
		t.Errorf("%s %s: X-GitHub-Api-Version = %q", r.Method, r.URL.Path, got)
	}
	if got := r.Header.Get("User-Agent"); got != "yip" {
		t.Errorf("%s %s: User-Agent = %q", r.Method, r.URL.Path, got)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
		t.Errorf("%s %s: Authorization = %q", r.Method, r.URL.Path, got)
	}
	key := r.Method + " " + r.URL.Path
	f.mu.Lock()
	f.counts[key]++
	hook := f.hooks[key]
	f.mu.Unlock()
	if hook != nil {
		hook(w, r)
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path
	if r.Method == http.MethodGet && path == "/user" {
		writeJSON(w, 200, userJSON(f.viewer))
		return
	}
	if m := rePull.FindStringSubmatch(path); m != nil && r.Method == http.MethodGet {
		n, _ := strconv.Atoi(m[3])
		p := f.pulls[n]
		if p == nil || m[1] != testOwner || m[2] != testRepo {
			notFound(w)
			return
		}
		if f.onPull != nil {
			f.onPull(n, f.counts[key], p)
		}
		writeJSON(w, 200, f.pullJSON(p))
		return
	}
	if m := rePullFiles.FindStringSubmatch(path); m != nil && r.Method == http.MethodGet {
		n, _ := strconv.Atoi(m[3])
		if f.pulls[n] == nil {
			notFound(w)
			return
		}
		all := f.files[n]
		lo, hi := f.page(w, r, len(all))
		writeJSON(w, 200, all[lo:hi])
		return
	}
	if m := rePullRevs.FindStringSubmatch(path); m != nil {
		n, _ := strconv.Atoi(m[3])
		if f.pulls[n] == nil {
			notFound(w)
			return
		}
		switch r.Method {
		case http.MethodGet:
			all := f.reviews[n]
			lo, hi := f.page(w, r, len(all))
			writeJSON(w, 200, all[lo:hi])
		case http.MethodPost:
			f.posts++
			var in map[string]any
			b, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(b, &in); err != nil {
				writeJSON(w, 400, map[string]any{"message": "Problems parsing JSON"})
				return
			}
			f.lastPost = in
			state := map[string]string{"APPROVE": "APPROVED", "REQUEST_CHANGES": "CHANGES_REQUESTED", "COMMENT": "COMMENTED"}[in["event"].(string)]
			f.addReviewLocked(n, f.viewer, state, in["commit_id"].(string), in["body"].(string))
			writeJSON(w, 200, f.reviews[n][len(f.reviews[n])-1])
		default:
			w.WriteHeader(405)
		}
		return
	}
	if m := reRuns.FindStringSubmatch(path); m != nil && r.Method == http.MethodGet {
		if r.URL.Query().Get("filter") != "latest" {
			t.Errorf("check-runs requested without filter=latest")
		}
		all := f.checkRuns[m[3]]
		lo, hi := f.page(w, r, len(all))
		writeJSON(w, 200, map[string]any{"total_count": len(all), "check_runs": all[lo:hi]})
		return
	}
	if m := reStatus.FindStringSubmatch(path); m != nil && r.Method == http.MethodGet {
		all := f.statuses[m[3]]
		lo, hi := f.page(w, r, len(all))
		writeJSON(w, 200, map[string]any{
			"state":       "pending",
			"statuses":    all[lo:hi],
			"sha":         m[3],
			"total_count": len(all),
			"repository":  repoJSON(),
			"commit_url":  "https://api.github.com/repos/acme/widgets/commits/" + m[3],
		})
		return
	}
	notFound(w)
}

func checkRun(name, status, conclusion string) map[string]any {
	var concl any
	if conclusion != "" {
		concl = conclusion
	}
	return map[string]any{
		"id":            4,
		"head_sha":      shaA,
		"node_id":       "MDg6Q2hlY2tSdW40",
		"external_id":   "",
		"url":           "https://api.github.com/repos/github/hello-world/check-runs/4",
		"html_url":      "https://github.com/github/hello-world/runs/4",
		"details_url":   "https://example.com",
		"status":        status,
		"conclusion":    concl,
		"started_at":    "2026-09-20T10:00:00Z",
		"completed_at":  nil,
		"name":          name,
		"check_suite":   map[string]any{"id": 5},
		"app":           map[string]any{"id": 1, "slug": "octoapp", "name": "Octo App"},
		"pull_requests": []any{},
	}
}

func commitStatus(context, state string) map[string]any {
	return map[string]any{
		"url":         "https://api.github.com/repos/octocat/Hello-World/statuses/" + shaA,
		"id":          1,
		"node_id":     "MDY6U3RhdHVzMQ==",
		"state":       state,
		"description": "Build has completed successfully",
		"target_url":  "https://ci.example.com/1000/output",
		"context":     context,
		"created_at":  "2026-09-20T10:00:00Z",
		"updated_at":  "2026-09-20T10:00:00Z",
	}
}

func fileJSON(name, status string, add, del int, patch string) map[string]any {
	return map[string]any{
		"sha":          "bbcd538c8e72b8c175046e27cc8f907076331401",
		"filename":     name,
		"status":       status,
		"additions":    add,
		"deletions":    del,
		"changes":      add + del,
		"blob_url":     "https://github.com/acme/widgets/blob/" + shaA + "/" + name,
		"raw_url":      "https://github.com/acme/widgets/raw/" + shaA + "/" + name,
		"contents_url": "https://api.github.com/repos/acme/widgets/contents/" + name + "?ref=" + shaA,
		"patch":        patch,
	}
}

var testRepoRef = forge.RepoRef{Host: "github.com", Owner: testOwner, Name: testRepo}

func boolp(b bool) *bool { return &b }
