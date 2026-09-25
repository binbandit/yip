package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/forge"
)

func TestNewAPIBase(t *testing.T) {
	cases := []struct {
		opts Options
		want string
	}{
		{Options{}, "https://api.github.com"},
		{Options{Host: "GitHub.com"}, "https://api.github.com"},
		{Options{Host: "ghe.example.com"}, "https://ghe.example.com/api/v3"},
		{Options{Host: "ghe.example.com", APIBase: "http://127.0.0.1:9/"}, "http://127.0.0.1:9"},
	}
	for _, tc := range cases {
		c := New(tc.opts)
		if got := c.apiBase.String(); got != tc.want {
			t.Errorf("New(%+v).apiBase = %q, want %q", tc.opts, got, tc.want)
		}
	}
	if New(Options{}).Name() != "github" {
		t.Error("Name() != github")
	}
}

func TestParseURL(t *testing.T) {
	gh := New(Options{})
	ghes := New(Options{Host: "ghe.example.com"})
	ok := []struct {
		c     *Connector
		url   string
		owner string
		name  string
		n     int
	}{
		{gh, "https://github.com/o/r/pull/42", "o", "r", 42},
		{gh, "https://github.com/binbandit/yip/pull/7/", "binbandit", "yip", 7},
		{gh, "https://www.github.com/o/r/pull/42", "o", "r", 42},
		{gh, "https://GitHub.com/My-Org/repo.name_x/pull/1/files", "My-Org", "repo.name_x", 1},
		{gh, "https://github.com/o/r/pull/42/commits/" + shaA, "o", "r", 42},
		{gh, "https://github.com/o/r/pull/42?w=1#discussion_r1", "o", "r", 42},
		{gh, "  https://github.com/o/r/pull/42  ", "o", "r", 42},
		{ghes, "https://ghe.example.com/team/svc/pull/1234/checks", "team", "svc", 1234},
	}
	for _, tc := range ok {
		ref, n, err := tc.c.ParseURL(tc.url)
		if err != nil {
			t.Errorf("ParseURL(%q) error: %v", tc.url, err)
			continue
		}
		if ref.Owner != tc.owner || ref.Name != tc.name || n != tc.n || ref.Host != tc.c.host {
			t.Errorf("ParseURL(%q) = %+v #%d", tc.url, ref, n)
		}
	}
	bad := []struct {
		c   *Connector
		url string
	}{
		{gh, ""},
		{gh, "github.com/o/r/pull/42"},
		{gh, "http://github.com/o/r/pull/42"},
		{gh, "https://gitlab.com/o/r/pull/42"},
		{gh, "https://github.com.evil.example/o/r/pull/42"},
		{gh, "https://user:pw@github.com/o/r/pull/42"},
		{gh, "https://github.com/o/r/pulls/42"},
		{gh, "https://github.com/o/r/issues/42"},
		{gh, "https://github.com/o/r/pull"},
		{gh, "https://github.com/o/r/pull/0"},
		{gh, "https://github.com/o/r/pull/-1"},
		{gh, "https://github.com/o/r/pull/+4"},
		{gh, "https://github.com/o/r/pull/42abc"},
		{gh, "https://github.com/o/r/pull/42/merge"},
		{gh, "https://github.com/o/../pull/42"},
		{gh, "https://github.com/-o/r/pull/42"},
		{gh, "https://github.com/o/r"},
		{gh, "https://ghe.example.com/o/r/pull/42"},
		{ghes, "https://github.com/o/r/pull/42"},
	}
	for _, tc := range bad {
		if _, _, err := tc.c.ParseURL(tc.url); !errors.Is(err, ErrInvalidURL) {
			t.Errorf("ParseURL(%q) err = %v, want ErrInvalidURL", tc.url, err)
		}
	}
}

func TestViewer(t *testing.T) {
	f := newFake(t)
	f.viewer = "oren-bot"
	a, err := f.connector().Viewer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if a.Login != "oren-bot" || a.Type != "User" {
		t.Fatalf("Viewer = %+v", a)
	}
}

func TestGetPR(t *testing.T) {
	f := newFake(t)
	f.addPull(&fakePull{Number: 42, Title: "Add widgets", Author: "mira", Head: shaA, HeadRef: "mira/widgets", Draft: true})
	f.addPull(&fakePull{Number: 43, Author: "mira", Head: shaB, State: "closed", Merged: true})
	f.addPull(&fakePull{Number: 44, Author: "mira", Head: shaB, State: "closed"})
	c := f.connector()
	ctx := context.Background()

	pr, err := c.GetPR(ctx, testRepoRef, 42)
	if err != nil {
		t.Fatal(err)
	}
	want := forge.PR{
		Repo: testRepoRef, Number: 42, URL: "https://github.com/acme/widgets/pull/42",
		Title: "Add widgets", State: "open", Base: shaBase, BaseRef: "main",
		Head: shaA, HeadRef: "mira/widgets", Author: "mira", Draft: true,
		UpdatedAt: time.Date(2026, 9, 20, 11, 30, 0, 0, time.UTC),
	}
	if pr != want {
		t.Fatalf("GetPR =\n%+v\nwant\n%+v", pr, want)
	}

	if pr, err := c.GetPR(ctx, testRepoRef, 43); err != nil || pr.State != "merged" {
		t.Fatalf("merged PR: state %q err %v", pr.State, err)
	}
	if pr, err := c.GetPR(ctx, testRepoRef, 44); err != nil || pr.State != "closed" {
		t.Fatalf("closed PR: state %q err %v", pr.State, err)
	}

	_, err = c.GetPR(ctx, testRepoRef, 999)
	if !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("missing PR err = %v, want ErrNotFound", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 || apiErr.RequestID == "" {
		t.Fatalf("missing PR err = %#v", err)
	}

	// A repository on another host must never be sent to this API.
	before := f.count("GET /repos/acme/widgets/pulls/42")
	if _, err := c.GetPR(ctx, forge.RepoRef{Host: "ghe.example.com", Owner: testOwner, Name: testRepo}, 42); err == nil {
		t.Fatal("expected host mismatch error")
	}
	if f.count("GET /repos/acme/widgets/pulls/42") != before {
		t.Fatal("request was sent for a foreign-host repository")
	}
}

func TestFilesPagination(t *testing.T) {
	f := newFake(t)
	f.addPull(&fakePull{Number: 1, Author: "mira", Head: shaA})
	for i, name := range []string{"a.go", "b.go", "c.go", "d.go", "e.go"} {
		f.files[1] = append(f.files[1], fileJSON(name, "modified", i+1, i, "@@ -1 +1 @@\n-old\n+new "+name))
	}
	c := f.connector()
	files, err := c.Files(context.Background(), testRepoRef, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 5 {
		t.Fatalf("got %d files, want 5", len(files))
	}
	for i, fd := range files {
		if fd.Path != []string{"a.go", "b.go", "c.go", "d.go", "e.go"}[i] || fd.Additions != i+1 || fd.Deletions != i || !strings.HasSuffix(fd.Patch, fd.Path) {
			t.Errorf("file %d = %+v", i, fd)
		}
	}
	if n := f.count("GET /repos/acme/widgets/pulls/1/files"); n != 3 {
		t.Errorf("files pages fetched = %d, want 3", n)
	}

	// Pinned to a head that is no longer current.
	if _, err := c.FilesAt(context.Background(), testRepoRef, 1, shaB); !errors.Is(err, forge.ErrStale) {
		t.Errorf("FilesAt(old head) err = %v, want ErrStale", err)
	}
}

func TestFilesHeadMovesDuringListing(t *testing.T) {
	f := newFake(t)
	f.addPull(&fakePull{Number: 1, Author: "mira", Head: shaA})
	f.files[1] = []map[string]any{fileJSON("a.go", "added", 1, 0, "+x")}
	f.onPull = func(n, calls int, p *fakePull) {
		if calls >= 2 {
			p.Head = shaB // a push lands between the two reads
		}
	}
	if _, err := f.connector().Files(context.Background(), testRepoRef, 1); !errors.Is(err, forge.ErrStale) {
		t.Fatalf("err = %v, want ErrStale", err)
	}
}

func TestFilesTruncated(t *testing.T) {
	f := newFake(t)
	f.addPull(&fakePull{Number: 1, Author: "mira", Head: shaA, ChangedFiles: 3500})
	f.files[1] = []map[string]any{fileJSON("a.go", "added", 1, 0, "+x")}
	files, err := f.connector().Files(context.Background(), testRepoRef, 1)
	if !errors.Is(err, ErrTruncated) || len(files) != 1 {
		t.Fatalf("files=%d err=%v, want partial list with ErrTruncated", len(files), err)
	}
}

func TestReviewsPagination(t *testing.T) {
	f := newFake(t)
	f.addPull(&fakePull{Number: 5, Author: "mira", Head: shaA})
	f.addReview(5, "oren-bot", "COMMENTED", shaB, "first pass")
	f.addReview(5, "kai", "CHANGES_REQUESTED", shaB, "fix it")
	f.addReview(5, "kai", "APPROVED", shaA, "lgtm")
	f.addReview(5, "", "DISMISSED", shaB, "deleted account")
	f.addReview(5, "oren-bot", "PENDING", shaA, "draft")
	c := f.connector()
	got, err := c.Reviews(context.Background(), testRepoRef, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d reviews, want 4 (PENDING omitted): %+v", len(got), got)
	}
	if got[1].Actor != "kai" || got[1].State != "CHANGES_REQUESTED" || got[1].CommitID != shaB || got[1].ExternalID != "82" {
		t.Errorf("review[1] = %+v", got[1])
	}
	if got[2].State != "APPROVED" || got[2].SubmittedAt != time.Date(2026, 9, 20, 10, 15, 0, 0, time.UTC) {
		t.Errorf("review[2] = %+v", got[2])
	}
	if got[3].Actor != "ghost" {
		t.Errorf("deleted user actor = %q, want ghost", got[3].Actor)
	}
	if n := f.count("GET /repos/acme/widgets/pulls/5/reviews"); n != 3 {
		t.Errorf("review pages fetched = %d, want 3", n)
	}

	// Page cap reached: partial list plus ErrTruncated, never silently.
	capped := f.connector(func(o *Options) { o.MaxPages = 1 })
	got, err = capped.Reviews(context.Background(), testRepoRef, 5)
	if !errors.Is(err, ErrTruncated) || len(got) != 2 {
		t.Fatalf("capped: %d reviews, err %v", len(got), err)
	}
}

func TestChecksAggregation(t *testing.T) {
	cases := []struct {
		name      string
		runs      []map[string]any
		statuses  []map[string]any
		runsHook  http.HandlerFunc
		statsHook http.HandlerFunc
		want      string
		p, fl, pe int
	}{
		{
			name: "mixed",
			runs: []map[string]any{
				checkRun("build", "completed", "success"),
				checkRun("lint", "completed", "failure"),
				checkRun("e2e", "in_progress", ""),
				checkRun("docs", "completed", "skipped"),
				checkRun("deploy", "completed", "cancelled"),
			},
			statuses: []map[string]any{commitStatus("ci/legacy", "success"), commitStatus("coverage", "pending"), commitStatus("sec", "error")},
			want:     ChecksFailure, p: 3, fl: 3, pe: 2,
		},
		{
			name:     "pending",
			runs:     []map[string]any{checkRun("build", "completed", "success"), checkRun("e2e", "queued", "")},
			statuses: []map[string]any{commitStatus("ci", "success")},
			want:     ChecksPending, p: 2, pe: 1,
		},
		{
			name:     "success",
			runs:     []map[string]any{checkRun("build", "completed", "success"), checkRun("x", "completed", "neutral"), checkRun("y", "completed", "success")},
			statuses: []map[string]any{commitStatus("ci", "success")},
			want:     ChecksSuccess, p: 4,
		},
		{name: "none", want: ChecksNone},
		{
			name: "check runs forbidden",
			runsHook: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 403, map[string]any{"message": "Resource not accessible by personal access token", "documentation_url": "https://docs.github.com/rest/checks/runs", "status": "403"})
			},
			statuses: []map[string]any{commitStatus("ci", "success")},
			want:     ChecksUnknown, p: 1,
		},
		{
			name: "statuses forbidden",
			runs: []map[string]any{checkRun("build", "completed", "success")},
			statsHook: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 403, map[string]any{"message": "Resource not accessible by integration"})
			},
			want: ChecksUnknown, p: 1,
		},
		{
			name: "forbidden but a known failure",
			runs: []map[string]any{checkRun("build", "completed", "timed_out")},
			statsHook: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 403, map[string]any{"message": "Resource not accessible by integration"})
			},
			want: ChecksFailure, fl: 1,
		},
		{
			name: "total_count exceeds what was listed",
			runsHook: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, map[string]any{"total_count": 3, "check_runs": []any{checkRun("build", "completed", "success")}})
			},
			want: ChecksUnknown, p: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			f.checkRuns[shaA] = tc.runs
			f.statuses[shaA] = tc.statuses
			if tc.runsHook != nil {
				f.hooks["GET /repos/acme/widgets/commits/"+shaA+"/check-runs"] = tc.runsHook
			}
			if tc.statsHook != nil {
				f.hooks["GET /repos/acme/widgets/commits/"+shaA+"/status"] = tc.statsHook
			}
			got, err := f.connector().Checks(context.Background(), testRepoRef, shaA)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != tc.want || got.Passed != tc.p || got.Failed != tc.fl || got.Pending != tc.pe || got.Total != tc.p+tc.fl+tc.pe {
				t.Fatalf("Checks = %+v, want %s %d/%d/%d", got, tc.want, tc.p, tc.fl, tc.pe)
			}
		})
	}
}

func TestChecksErrors(t *testing.T) {
	f := newFake(t)
	nf := func(w http.ResponseWriter, r *http.Request) { notFound(w) }
	f.hooks["GET /repos/acme/widgets/commits/"+shaA+"/check-runs"] = nf
	f.hooks["GET /repos/acme/widgets/commits/"+shaA+"/status"] = nf
	c := f.connector()
	if _, err := c.Checks(context.Background(), testRepoRef, shaA); !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("both 404: err = %v, want ErrNotFound", err)
	}
	if _, err := c.Checks(context.Background(), testRepoRef, "main; rm"); err == nil {
		t.Fatal("expected invalid SHA error")
	}
}

func TestMergeStatus(t *testing.T) {
	cases := []struct {
		name      string
		pull      fakePull
		want      string
		merged    bool
		reasonHas string
	}{
		{"clean", fakePull{Mergeable: boolp(true), MergeableState: "clean"}, MergeClean, false, ""},
		{"blocked", fakePull{Mergeable: boolp(true), MergeableState: "blocked"}, MergeBlocked, false, "not visible"},
		{"behind", fakePull{Mergeable: boolp(true), MergeableState: "behind"}, MergeBehind, false, "out of date"},
		{"dirty", fakePull{Mergeable: boolp(false), MergeableState: "dirty"}, MergeDirty, false, "conflict"},
		{"unstable", fakePull{Mergeable: boolp(true), MergeableState: "unstable"}, MergeUnstable, false, "not passing"},
		{"has_hooks", fakePull{Mergeable: boolp(true), MergeableState: "has_hooks"}, MergeClean, false, "hooks"},
		{"draft", fakePull{Draft: true, Mergeable: boolp(true), MergeableState: "draft"}, MergeBlocked, false, "draft"},
		{"merged", fakePull{State: "closed", Merged: true}, MergeBlocked, true, "already merged"},
		{"closed", fakePull{State: "closed", Mergeable: boolp(true), MergeableState: "clean"}, MergeBlocked, false, "closed"},
		{"false with unknown state", fakePull{Mergeable: boolp(false), MergeableState: "unknown"}, MergeDirty, false, "cannot be merged"},
		{"unrecognized", fakePull{Mergeable: boolp(true), MergeableState: "quantum"}, MergeUnknown, false, "quantum"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			p := tc.pull
			p.Number, p.Author, p.Head = 9, "mira", shaA
			f.addPull(&p)
			st, err := f.connector().MergeStatus(context.Background(), testRepoRef, 9)
			if err != nil {
				t.Fatal(err)
			}
			if st.Mergeable != tc.want || st.Merged != tc.merged || st.Reasons == nil {
				t.Fatalf("MergeStatus = %+v, want %s merged=%v", st, tc.want, tc.merged)
			}
			if tc.reasonHas != "" && !strings.Contains(strings.Join(st.Reasons, " "), tc.reasonHas) {
				t.Fatalf("reasons %q lack %q", st.Reasons, tc.reasonHas)
			}
			if tc.reasonHas == "" && len(st.Reasons) != 0 {
				t.Fatalf("unexpected reasons %q", st.Reasons)
			}
		})
	}
}

func TestMergeStatusNullMergeable(t *testing.T) {
	f := newFake(t)
	f.addPull(&fakePull{Number: 9, Author: "mira", Head: shaA, MergeableState: "unknown"})
	c := f.connector(func(o *Options) { o.MergeablePolls = 2 })
	st, err := c.MergeStatus(context.Background(), testRepoRef, 9)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mergeable != MergeUnknown || len(st.Reasons) == 0 {
		t.Fatalf("MergeStatus = %+v, want unknown with a reason", st)
	}
	if n := f.count("GET /repos/acme/widgets/pulls/9"); n != 3 {
		t.Fatalf("PR fetched %d times, want 1 + 2 polls", n)
	}

	// GitHub finishes computing on the second read.
	f2 := newFake(t)
	f2.addPull(&fakePull{Number: 9, Author: "mira", Head: shaA})
	f2.onPull = func(n, calls int, p *fakePull) {
		if calls >= 2 {
			p.Mergeable, p.MergeableState = boolp(true), "clean"
		}
	}
	st, err = f2.connector().MergeStatus(context.Background(), testRepoRef, 9)
	if err != nil || st.Mergeable != MergeClean {
		t.Fatalf("MergeStatus = %+v err %v, want clean", st, err)
	}

	// Polling disabled.
	f3 := newFake(t)
	f3.addPull(&fakePull{Number: 9, Author: "mira", Head: shaA})
	st, err = f3.connector(func(o *Options) { o.MergeablePolls = -1 }).MergeStatus(context.Background(), testRepoRef, 9)
	if err != nil || st.Mergeable != MergeUnknown || f3.count("GET /repos/acme/widgets/pulls/9") != 1 {
		t.Fatalf("no-poll MergeStatus = %+v err %v", st, err)
	}
}

func TestRateLimit(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	reset := now.Add(17 * time.Minute).Truncate(time.Second)
	cases := []struct {
		name      string
		hook      http.HandlerFunc
		reset     time.Time
		secondary bool
	}{
		{
			name: "primary 403",
			hook: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-RateLimit-Limit", "5000")
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("X-RateLimit-Used", "5000")
				w.Header().Set("X-RateLimit-Resource", "core")
				w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
				writeJSON(w, 403, map[string]any{"message": "API rate limit exceeded for user ID 1.", "documentation_url": "https://docs.github.com/rest/overview/resources-in-the-rest-api#rate-limiting"})
			},
			reset: reset,
		},
		{
			name: "secondary 429 retry-after",
			hook: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "30")
				writeJSON(w, 429, map[string]any{"message": "You have exceeded a secondary rate limit."})
			},
			reset: now.Add(30 * time.Second), secondary: true,
		},
		{
			name: "secondary 403 no headers",
			hook: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 403, map[string]any{"message": "You have exceeded a secondary rate limit. Please wait a few minutes before you try again."})
			},
			reset: now.Add(time.Minute), secondary: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			f.addPull(&fakePull{Number: 1, Author: "mira", Head: shaA})
			f.hooks["GET /repos/acme/widgets/pulls/1"] = tc.hook
			c := f.connector(func(o *Options) { o.Now = func() time.Time { return now } })
			_, err := c.GetPR(context.Background(), testRepoRef, 1)
			var rl *RateLimitError
			if !errors.As(err, &rl) || !errors.Is(err, ErrRateLimited) {
				t.Fatalf("err = %v, want *RateLimitError", err)
			}
			if errors.Is(err, forge.ErrForbidden) {
				t.Fatal("rate limit must not look like a permission denial")
			}
			if !rl.ResetAt.Equal(tc.reset) || rl.Secondary != tc.secondary {
				t.Fatalf("RateLimitError = %+v, want reset %v secondary %v", rl, tc.reset, tc.secondary)
			}
			if n := f.count("GET /repos/acme/widgets/pulls/1"); n != 1 {
				t.Fatalf("request made %d times; the connector must not retry", n)
			}
		})
	}

	// A rate limit on the checks endpoints aborts rather than degrading to unknown.
	f := newFake(t)
	f.hooks["GET /repos/acme/widgets/commits/"+shaA+"/check-runs"] = cases[1].hook
	if _, err := f.connector().Checks(context.Background(), testRepoRef, shaA); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Checks err = %v, want ErrRateLimited", err)
	}
}

func TestErrorMapping(t *testing.T) {
	f := newFake(t)
	f.hooks["GET /user"] = func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 401, map[string]any{"message": "Bad credentials", "documentation_url": "https://docs.github.com/rest"})
	}
	_, err := f.connector().Viewer(context.Background())
	if !errors.Is(err, ErrBadCredentials) || !errors.Is(err, forge.ErrForbidden) {
		t.Fatalf("401 err = %v", err)
	}

	// Credential source failure: no request at all.
	f2 := newFake(t)
	c := New(Options{APIBase: f2.srv.URL, Token: func(context.Context) (string, error) { return "", errors.New("vault sealed") }})
	if _, err := c.Viewer(context.Background()); err == nil || !strings.Contains(err.Error(), "vault sealed") {
		t.Fatalf("token error = %v", err)
	}
	if f2.count("GET /user") != 0 {
		t.Fatal("request sent without a credential")
	}
}

func TestPaginationRefusesForeignOrigin(t *testing.T) {
	f := newFake(t)
	f.addPull(&fakePull{Number: 5, Author: "mira", Head: shaA})
	f.hooks["GET /repos/acme/widgets/pulls/5/reviews"] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<https://evil.example/repos/acme/widgets/pulls/5/reviews?page=2>; rel="next"`)
		writeJSON(w, 200, []any{})
	}
	if _, err := f.connector().Reviews(context.Background(), testRepoRef, 5); err == nil || !strings.Contains(err.Error(), "foreign origin") {
		t.Fatalf("err = %v, want foreign-origin refusal", err)
	}
}

func TestDialFailureIsNotAmbiguous(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	c := New(Options{APIBase: base, Timeout: time.Second})
	_, err := c.do(context.Background(), http.MethodPost, c.endpoint("/x", nil), map[string]string{}, true)
	if err == nil || errors.Is(err, forge.ErrAmbiguous) {
		t.Fatalf("connection refused before sending: err = %v, want a non-ambiguous error", err)
	}
}
