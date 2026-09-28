// Package github implements forge.Connector against the GitHub REST API (v3,
// API version 2022-11-28) for github.com and GitHub Enterprise Server.
//
// It uses only net/http and encoding/json. It never retries on its own: rate
// limits surface as *RateLimitError, ambiguous writes as forge.ErrAmbiguous,
// and the caller owns scheduling and reconciliation. See docs/forge/github.md.
package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/forge"
)

// APIVersion is the pinned GitHub REST API version sent on every request.
const APIVersion = "2022-11-28"

const (
	defaultHost         = "github.com"
	defaultTimeout      = 30 * time.Second
	defaultMaxPages     = 10
	defaultMergePolls   = 3
	defaultMergeBackoff = time.Second
	perPage             = 100
	// filesMaxPages covers GitHub's own limit of 3000 files per PR listing.
	filesMaxPages = 30
	// markerMaxPages bounds the reconciliation scan; running out without a
	// definitive answer is reported as an error, never as "not found".
	markerMaxPages = 100
)

// Options configures a Connector.
type Options struct {
	// Host is the web host, e.g. "github.com" (default) or a GitHub
	// Enterprise Server hostname. It decides which URLs ParseURL accepts and
	// which RepoRef.Host values the connector will talk to.
	Host string
	// APIBase overrides the REST base URL. Defaults to https://api.github.com
	// for github.com and https://{Host}/api/v3 otherwise. Tests point it at an
	// httptest server.
	APIBase string
	// Token returns the credential for each request. Review publication and
	// reconciliation keep one credential for their entire operation so their
	// actor cannot change between requests. A nil func or an empty token sends
	// unauthenticated requests.
	Token func(ctx context.Context) (string, error)
	// HTTPClient defaults to a new http.Client. The connector applies its own
	// per-request timeout through the request context.
	HTTPClient *http.Client
	// Timeout bounds each HTTP request including reading the body. Default 30s.
	Timeout time.Duration
	// MaxPages caps pagination for reviews, check runs and statuses (100
	// items per page). Default 10. Files always page up to GitHub's own
	// 3000-file limit.
	MaxPages int
	// MergeablePolls is how many extra times MergeStatus re-fetches the PR
	// while GitHub is still computing mergeability (mergeable == null).
	// Default 3; a negative value disables polling.
	MergeablePolls int
	// MergeablePollInterval is the wait between those re-fetches. Default 1s.
	MergeablePollInterval time.Duration
	// Now is used for rate-limit reset estimates. Defaults to time.Now.
	Now func() time.Time
}

// Connector is the GitHub implementation of forge.Connector.
type Connector struct {
	host         string
	apiBase      *url.URL
	token        func(ctx context.Context) (string, error)
	client       *http.Client
	timeout      time.Duration
	maxPages     int
	mergePolls   int
	mergeBackoff time.Duration
	now          func() time.Time
}

var _ forge.Connector = (*Connector)(nil)

// New returns a GitHub connector. It panics only if opts.APIBase is set and
// is not an absolute http(s) URL, which is a programming error.
func New(opts Options) *Connector {
	host := strings.ToLower(strings.TrimSpace(opts.Host))
	if host == "" {
		host = defaultHost
	}
	base := strings.TrimRight(strings.TrimSpace(opts.APIBase), "/")
	if base == "" {
		if host == defaultHost {
			base = "https://api.github.com"
		} else {
			base = "https://" + host + "/api/v3"
		}
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		panic(fmt.Sprintf("github: invalid APIBase %q", base))
	}
	c := &Connector{
		host:         host,
		apiBase:      u,
		token:        opts.Token,
		client:       opts.HTTPClient,
		timeout:      opts.Timeout,
		maxPages:     opts.MaxPages,
		mergePolls:   opts.MergeablePolls,
		mergeBackoff: opts.MergeablePollInterval,
		now:          opts.Now,
	}
	if c.client == nil {
		c.client = &http.Client{}
	}
	if c.timeout <= 0 {
		c.timeout = defaultTimeout
	}
	if c.maxPages <= 0 {
		c.maxPages = defaultMaxPages
	}
	switch {
	case c.mergePolls == 0:
		c.mergePolls = defaultMergePolls
	case c.mergePolls < 0:
		c.mergePolls = 0
	}
	if c.mergeBackoff <= 0 {
		c.mergeBackoff = defaultMergeBackoff
	}
	if c.now == nil {
		c.now = time.Now
	}
	return c
}

// Name returns "github".
func (c *Connector) Name() string { return "github" }

// Host returns the configured web host.
func (c *Connector) Host() string { return c.host }

// withCredential keeps actor checks and their follow-up requests under the
// same account even if the stored credential changes while they run.
func (c *Connector) withCredential(ctx context.Context) (*Connector, error) {
	if c.token == nil {
		return c, nil
	}
	token, err := c.token(ctx)
	if err != nil {
		return nil, fmt.Errorf("github: credential unavailable: %w", err)
	}
	operation := *c
	operation.token = func(context.Context) (string, error) { return token, nil }
	return &operation, nil
}

type ghUser struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

func (u *ghUser) login() string {
	if u == nil || u.Login == "" {
		// GitHub substitutes "ghost" for deleted accounts; a null user
		// means the same thing.
		return "ghost"
	}
	return u.Login
}

// Viewer returns the account the configured credential acts as (GET /user).
// Several yip engineers sharing one token are this one remote actor.
func (c *Connector) Viewer(ctx context.Context) (forge.Actor, error) {
	var u ghUser
	if err := c.getJSON(ctx, "/user", nil, &u); err != nil {
		return forge.Actor{}, err
	}
	if u.Login == "" {
		return forge.Actor{}, fmt.Errorf("github: GET /user returned no login")
	}
	return forge.Actor{Login: u.Login, Type: u.Type}, nil
}

var (
	ownerRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9_-]{0,98}[A-Za-z0-9_])?$`)
	nameRe  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	shaRe   = regexp.MustCompile(`^[0-9A-Fa-f]{7,64}$`)
)

func validRepoName(owner, name string) bool {
	return ownerRe.MatchString(owner) && nameRe.MatchString(name) && name != "." && name != ".."
}

// ParseURL resolves https://{host}/{owner}/{repo}/pull/{n} (optionally
// followed by /files, /commits or /checks, a query, or a fragment). Any other
// host, scheme, or path shape is rejected with ErrInvalidURL.
func (c *Connector) ParseURL(raw string) (forge.RepoRef, int, error) {
	fail := func(why string) (forge.RepoRef, int, error) {
		return forge.RepoRef{}, 0, fmt.Errorf("%w: %q: %s", ErrInvalidURL, raw, why)
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fail(err.Error())
	}
	if u.Scheme != "https" {
		return fail("scheme must be https")
	}
	if u.User != nil {
		return fail("credentials in URL are not allowed")
	}
	host := strings.ToLower(u.Host)
	if host != c.host && !(c.host == defaultHost && host == "www.github.com") {
		return fail(fmt.Sprintf("host is not %s", c.host))
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segs) < 4 || segs[2] != "pull" {
		return fail("expected /{owner}/{repo}/pull/{number}")
	}
	if len(segs) > 4 {
		switch segs[4] {
		case "files", "commits", "checks":
		default:
			return fail("unexpected path after pull request number")
		}
	}
	owner, name := segs[0], segs[1]
	if !validRepoName(owner, name) {
		return fail("invalid owner or repository name")
	}
	num := segs[3]
	for _, r := range num {
		if r < '0' || r > '9' {
			return fail("pull request number must be a positive integer")
		}
	}
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 {
		return fail("pull request number must be a positive integer")
	}
	return forge.RepoRef{Host: c.host, Owner: owner, Name: name}, n, nil
}

// repoPath validates repo and returns "/repos/{owner}/{name}". It refuses
// repositories on a different host so a credential is never sent to the
// wrong forge.
func (c *Connector) repoPath(repo forge.RepoRef) (string, error) {
	if repo.Host != "" && !strings.EqualFold(repo.Host, c.host) &&
		!(c.host == defaultHost && strings.EqualFold(repo.Host, "www.github.com")) {
		return "", fmt.Errorf("github: repository host %q does not match connector host %q", repo.Host, c.host)
	}
	if !validRepoName(repo.Owner, repo.Name) {
		return "", fmt.Errorf("github: invalid repository %q/%q", repo.Owner, repo.Name)
	}
	return "/repos/" + url.PathEscape(repo.Owner) + "/" + url.PathEscape(repo.Name), nil
}

func (c *Connector) pullPath(repo forge.RepoRef, number int) (string, error) {
	rp, err := c.repoPath(repo)
	if err != nil {
		return "", err
	}
	if number <= 0 {
		return "", fmt.Errorf("github: invalid pull request number %d", number)
	}
	return rp + "/pulls/" + strconv.Itoa(number), nil
}
