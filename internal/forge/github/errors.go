package github

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/forge"
)

var (
	// ErrRateLimited matches every *RateLimitError via errors.Is.
	ErrRateLimited = errors.New("github: rate limited")
	// ErrBadCredentials means GitHub rejected the credential (HTTP 401).
	// Errors carrying it also match forge.ErrForbidden.
	ErrBadCredentials = errors.New("github: credential rejected")
	// ErrInvalidURL is returned by ParseURL for anything that is not a pull
	// request URL on the configured host.
	ErrInvalidURL = errors.New("github: not a pull request URL for this host")
	// ErrTruncated means GitHub had more items than the connector was
	// willing (or GitHub was able) to page through. The accompanying result
	// is partial and must not be presented as complete.
	ErrTruncated = errors.New("github: result truncated")
	// ErrWebhookSignature means a webhook delivery's X-Hub-Signature-256 is
	// missing, malformed, or does not match the shared secret.
	ErrWebhookSignature = errors.New("github: webhook signature missing or invalid")
	// ErrWebhookMalformed means a correctly signed delivery lacks required
	// headers or carries an unparseable payload.
	ErrWebhookMalformed = errors.New("github: malformed webhook delivery")
)

// APIError is a non-success response from the GitHub REST API. It unwraps to
// the matching forge sentinel errors (forge.ErrNotFound, forge.ErrForbidden,
// forge.ErrIneligible, forge.ErrStale) so callers can use errors.Is.
type APIError struct {
	StatusCode       int
	Method           string
	Path             string
	Message          string
	Details          []string
	DocumentationURL string
	RequestID        string

	kinds []error
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "github: %s %s: HTTP %d", e.Method, e.Path, e.StatusCode)
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if len(e.Details) > 0 {
		b.WriteString(" (")
		b.WriteString(strings.Join(e.Details, "; "))
		b.WriteString(")")
	}
	if e.RequestID != "" {
		b.WriteString(" [request ")
		b.WriteString(e.RequestID)
		b.WriteString("]")
	}
	return b.String()
}

// Unwrap exposes the forge sentinels this response maps to.
func (e *APIError) Unwrap() []error { return e.kinds }

// text returns the message and details joined, lower-cased, for matching.
func (e *APIError) text() string {
	return strings.ToLower(e.Message + " " + strings.Join(e.Details, " "))
}

// RateLimitError reports that GitHub refused a request because a primary or
// secondary rate limit was hit. The connector never retries on its own; the
// caller should wait until ResetAt before issuing more requests.
type RateLimitError struct {
	StatusCode int
	Method     string
	Path       string
	// ResetAt is when GitHub says requests may resume. For secondary limits
	// without a Retry-After header it is a conservative one-minute estimate.
	ResetAt time.Time
	// Secondary is true for secondary (burst/abuse) limits, false when the
	// primary hourly quota is exhausted.
	Secondary bool
	Limit     int
	Remaining int
	Resource  string
	Message   string
}

func (e *RateLimitError) Error() string {
	kind := "primary"
	if e.Secondary {
		kind = "secondary"
	}
	msg := fmt.Sprintf("github: %s %s: %s rate limit exceeded (HTTP %d); retry after %s",
		e.Method, e.Path, kind, e.StatusCode, e.ResetAt.UTC().Format(time.RFC3339))
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

// Is reports whether target is ErrRateLimited.
func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

// classify maps a GitHub status code and message to forge sentinels.
func classify(e *APIError) []error {
	switch e.StatusCode {
	case 401:
		return []error{ErrBadCredentials, forge.ErrForbidden}
	case 403:
		return []error{forge.ErrForbidden}
	case 404, 410:
		return []error{forge.ErrNotFound}
	case 422:
		t := e.text()
		switch {
		case isIneligibleText(t):
			return []error{forge.ErrIneligible}
		case strings.Contains(t, "not part of the pull request"),
			strings.Contains(t, "pull request is closed"),
			strings.Contains(t, "pull request is merged"),
			strings.Contains(t, "merged pull request"),
			strings.Contains(t, "closed pull request"):
			return []error{forge.ErrStale}
		case strings.Contains(t, "no commit found"):
			return []error{forge.ErrNotFound}
		}
	}
	return nil
}

// isIneligibleText recognises GitHub's refusals of a review action because of
// who the actor is, e.g. "Can not approve your own pull request",
// "Can not request changes on your own pull request", or "GitHub Actions is
// not permitted to approve pull requests".
func isIneligibleText(t string) bool {
	return strings.Contains(t, "your own pull request") ||
		strings.Contains(t, "not permitted to approve")
}
