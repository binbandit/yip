package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/binbandit/yip/internal/forge"
)

const (
	maxBodyBytes      = 64 << 20
	maxErrorBodyBytes = 1 << 20
	userAgent         = "yip"
)

type response struct {
	status int
	header http.Header
	body   []byte
}

// endpoint joins the API base with path and query.
func (c *Connector) endpoint(path string, q url.Values) string {
	u := *c.apiBase
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawPath = ""
	if len(q) > 0 {
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// do performs one HTTP request. For writes (write == true) any failure that
// may have happened after the request reached GitHub — a timeout, a broken
// connection after the headers were written, a 5xx, or an unreadable success
// body — is reported as forge.ErrAmbiguous so the caller reconciles instead
// of retrying blindly.
func (c *Connector) do(ctx context.Context, method, rawURL string, in any, write bool) (*response, error) {
	path := rawURL
	if u, err := url.Parse(rawURL); err == nil {
		path = u.Path
	}
	var token string
	if c.token != nil {
		t, err := c.token(ctx)
		if err != nil {
			return nil, fmt.Errorf("github: credential unavailable: %w", err)
		}
		token = strings.TrimSpace(t)
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("github: encode %s %s: %w", method, path, err)
		}
		body = bytes.NewReader(b)
	}

	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var sent atomic.Bool
	trace := &httptrace.ClientTrace{WroteHeaders: func() { sent.Store(true) }}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(reqCtx, trace), method, rawURL, body)
	if err != nil {
		return nil, fmt.Errorf("github: build %s %s: %w", method, path, err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", APIVersion)
	req.Header.Set("User-Agent", userAgent)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		if write && (sent.Load() || isTimeout(err)) {
			return nil, fmt.Errorf("github: %s %s: %w: %w", method, path, forge.ErrAmbiguous, err)
		}
		return nil, fmt.Errorf("github: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	limit := int64(maxBodyBytes)
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	if !ok {
		limit = maxErrorBodyBytes
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = fmt.Errorf("response body exceeds %d bytes", limit)
	}
	if err != nil {
		if write && (ok || resp.StatusCode >= 500) {
			return nil, fmt.Errorf("github: %s %s: HTTP %d: %w: reading response: %w",
				method, path, resp.StatusCode, forge.ErrAmbiguous, err)
		}
		if ok {
			return nil, fmt.Errorf("github: %s %s: reading response: %w", method, path, err)
		}
		data = nil // fall through with whatever status we have
	}
	r := &response{status: resp.StatusCode, header: resp.Header, body: data}
	if ok {
		return r, nil
	}
	return nil, c.statusError(method, path, r, write)
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

type ghErrorBody struct {
	Message          string            `json:"message"`
	Errors           []json.RawMessage `json:"errors"`
	DocumentationURL string            `json:"documentation_url"`
}

// statusError turns a non-2xx response into *RateLimitError, *APIError, or
// (for writes that hit a 5xx) an error wrapping forge.ErrAmbiguous.
func (c *Connector) statusError(method, path string, r *response, write bool) error {
	var eb ghErrorBody
	_ = json.Unmarshal(r.body, &eb)
	apiErr := &APIError{
		StatusCode:       r.status,
		Method:           method,
		Path:             path,
		Message:          eb.Message,
		DocumentationURL: eb.DocumentationURL,
		RequestID:        r.header.Get("X-GitHub-Request-Id"),
	}
	if apiErr.Message == "" && len(r.body) > 0 && len(r.body) < 512 && !json.Valid(r.body) {
		apiErr.Message = strings.TrimSpace(string(r.body))
	}
	for _, raw := range eb.Errors {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			if s != "" {
				apiErr.Details = append(apiErr.Details, s)
			}
			continue
		}
		var obj struct {
			Message string `json:"message"`
			Code    string `json:"code"`
			Field   string `json:"field"`
		}
		if json.Unmarshal(raw, &obj) == nil {
			switch {
			case obj.Message != "":
				apiErr.Details = append(apiErr.Details, obj.Message)
			case obj.Code != "":
				apiErr.Details = append(apiErr.Details, strings.TrimSpace(obj.Field+" "+obj.Code))
			}
		}
	}
	if rl := c.rateLimit(r, apiErr); rl != nil {
		return rl
	}
	apiErr.kinds = classify(apiErr)
	if write && r.status >= 500 {
		return fmt.Errorf("%w: %w", forge.ErrAmbiguous, apiErr)
	}
	return apiErr
}

// rateLimit recognises GitHub's primary and secondary rate-limit responses:
// 429, or 403 with X-RateLimit-Remaining: 0, Retry-After, or a rate-limit
// message.
func (c *Connector) rateLimit(r *response, apiErr *APIError) *RateLimitError {
	if r.status != http.StatusForbidden && r.status != http.StatusTooManyRequests {
		return nil
	}
	h := r.header
	primary := strings.TrimSpace(h.Get("X-RateLimit-Remaining")) == "0"
	retryAfter := strings.TrimSpace(h.Get("Retry-After"))
	mentions := strings.Contains(apiErr.text(), "rate limit")
	if r.status == http.StatusForbidden && !primary && retryAfter == "" && !mentions {
		return nil
	}
	now := c.now()
	e := &RateLimitError{
		StatusCode: r.status,
		Method:     apiErr.Method,
		Path:       apiErr.Path,
		Secondary:  !primary,
		Limit:      atoi(h.Get("X-RateLimit-Limit"), -1),
		Remaining:  atoi(h.Get("X-RateLimit-Remaining"), -1),
		Resource:   h.Get("X-RateLimit-Resource"),
		Message:    apiErr.Message,
	}
	switch {
	case retryAfter != "":
		if secs, err := strconv.Atoi(retryAfter); err == nil && secs >= 0 {
			e.ResetAt = now.Add(time.Duration(secs) * time.Second)
		} else if t, err := http.ParseTime(retryAfter); err == nil {
			e.ResetAt = t
		}
	case primary:
		if reset, err := strconv.ParseInt(strings.TrimSpace(h.Get("X-RateLimit-Reset")), 10, 64); err == nil {
			e.ResetAt = time.Unix(reset, 0)
		}
	}
	if e.ResetAt.IsZero() {
		// GitHub's guidance for secondary limits without headers: wait at
		// least one minute.
		e.ResetAt = now.Add(time.Minute)
	}
	return e
}

func atoi(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

// getJSON GETs path (relative to the API base) and decodes the body into out.
func (c *Connector) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	r, err := c.do(ctx, http.MethodGet, c.endpoint(path, q), nil, false)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(r.body, out); err != nil {
		return fmt.Errorf("github: decode GET %s: %w", path, err)
	}
	return nil
}

// paginate GETs first and follows rel="next" Link headers for up to maxPages
// pages, calling page with each body. It reports whether more pages remained.
func (c *Connector) paginate(ctx context.Context, first string, maxPages int, page func(body []byte) error) (more bool, err error) {
	next := first
	for i := 0; i < maxPages && next != ""; i++ {
		r, err := c.do(ctx, http.MethodGet, next, nil, false)
		if err != nil {
			return false, err
		}
		if err := page(r.body); err != nil {
			return false, err
		}
		next, err = c.nextLink(r.header.Get("Link"))
		if err != nil {
			return false, err
		}
	}
	return next != "", nil
}

// nextLink extracts rel="next" from an RFC 8288 Link header. It refuses to
// follow links to a different origin so the credential stays with GitHub.
func (c *Connector) nextLink(header string) (string, error) {
	for _, part := range strings.Split(header, ",") {
		segs := strings.Split(part, ";")
		if len(segs) < 2 {
			continue
		}
		target := strings.TrimSpace(segs[0])
		if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
			continue
		}
		isNext := false
		for _, p := range segs[1:] {
			k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
			if strings.EqualFold(strings.TrimSpace(k), "rel") {
				for _, rel := range strings.Fields(strings.Trim(strings.TrimSpace(v), `"`)) {
					if strings.EqualFold(rel, "next") {
						isNext = true
					}
				}
			}
		}
		if !isNext {
			continue
		}
		u, err := c.apiBase.Parse(target[1 : len(target)-1])
		if err != nil {
			return "", fmt.Errorf("github: invalid pagination link: %w", err)
		}
		if !strings.EqualFold(u.Scheme, c.apiBase.Scheme) || !strings.EqualFold(u.Host, c.apiBase.Host) {
			return "", fmt.Errorf("github: refusing pagination link to foreign origin %s://%s", u.Scheme, u.Host)
		}
		return u.String(), nil
	}
	return "", nil
}

// getList pages through an endpoint that returns a JSON array.
func getList[T any](ctx context.Context, c *Connector, path string, q url.Values, maxPages int) ([]T, bool, error) {
	if q == nil {
		q = url.Values{}
	}
	q.Set("per_page", strconv.Itoa(perPage))
	var out []T
	more, err := c.paginate(ctx, c.endpoint(path, q), maxPages, func(body []byte) error {
		var page []T
		if err := json.Unmarshal(body, &page); err != nil {
			return fmt.Errorf("github: decode GET %s: %w", path, err)
		}
		out = append(out, page...)
		return nil
	})
	return out, more, err
}

// sleep waits d or until ctx is done.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
