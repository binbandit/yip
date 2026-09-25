package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/forge"
	"github.com/binbandit/yip/protocol"
)

type ghReview struct {
	ID          int64      `json:"id"`
	User        *ghUser    `json:"user"`
	Body        string     `json:"body"`
	State       string     `json:"state"` // APPROVED | CHANGES_REQUESTED | COMMENTED | DISMISSED | PENDING
	CommitID    string     `json:"commit_id"`
	SubmittedAt *time.Time `json:"submitted_at"`
}

func (c *Connector) listReviews(ctx context.Context, repo forge.RepoRef, number, maxPages int) ([]ghReview, bool, error) {
	path, err := c.pullPath(repo, number)
	if err != nil {
		return nil, false, err
	}
	return getList[ghReview](ctx, c, path+"/reviews", nil, maxPages)
}

// Reviews lists submitted remote reviews (GET .../pulls/{n}/reviews,
// paginated, oldest first). Unsubmitted PENDING drafts are omitted. If there
// are more reviews than the page cap, the partial list is returned together
// with ErrTruncated.
func (c *Connector) Reviews(ctx context.Context, repo forge.RepoRef, number int) ([]protocol.RemoteReview, error) {
	list, more, err := c.listReviews(ctx, repo, number, c.maxPages)
	if err != nil {
		return nil, err
	}
	out := make([]protocol.RemoteReview, 0, len(list))
	for _, r := range list {
		if r.State == "PENDING" {
			continue
		}
		rr := protocol.RemoteReview{
			Actor:      r.User.login(),
			State:      r.State,
			CommitID:   r.CommitID,
			ExternalID: strconv.FormatInt(r.ID, 10),
		}
		if r.SubmittedAt != nil {
			rr.SubmittedAt = *r.SubmittedAt
		}
		out = append(out, rr)
	}
	if more {
		return out, fmt.Errorf("%w: more than %d pages of reviews", ErrTruncated, c.maxPages)
	}
	return out, nil
}

var markerRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func validMarker(m string) error {
	if !markerRe.MatchString(m) || strings.Contains(m, "--") {
		return fmt.Errorf("github: invalid review marker %q (want 1-128 of [A-Za-z0-9._:-], no \"--\")", m)
	}
	return nil
}

// MarkerComment is the invisible HTML comment appended to published review
// bodies so a publication can be found again after an ambiguous failure.
func MarkerComment(marker string) string { return "<!-- yip-review:" + marker + " -->" }

type ghReviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"`
	Body string `json:"body"`
}

type ghCreateReview struct {
	CommitID string            `json:"commit_id"`
	Event    string            `json:"event"`
	Body     string            `json:"body"`
	Comments []ghReviewComment `json:"comments,omitempty"`
}

// PublishReview publishes a revision-bound review:
//
//  1. It looks for an existing review by the viewer carrying req.Marker and,
//     if found, returns its ID without posting again (idempotent retry).
//  2. It re-fetches the PR and returns forge.ErrStale if the PR is not open
//     or its head is not req.CommitID.
//  3. For APPROVE and REQUEST_CHANGES it returns forge.ErrIneligible,
//     without posting, when the viewer is the PR author.
//  4. It POSTs .../pulls/{n}/reviews with the marker appended to the body.
//
// GitHub's 422 self-review refusals map to forge.ErrIneligible, 403/404 to
// forge.ErrForbidden/forge.ErrNotFound, and a timeout, a connection failure
// after the request was sent, or a 5xx to forge.ErrAmbiguous: the caller must
// then reconcile with FindReviewByMarker before retrying. The returned
// external ID is the GitHub review ID.
func (c *Connector) PublishReview(ctx context.Context, repo forge.RepoRef, number int, req forge.PublishReview) (string, error) {
	path, err := c.pullPath(repo, number)
	if err != nil {
		return "", err
	}
	if err := validMarker(req.Marker); err != nil {
		return "", err
	}
	switch req.Event {
	case forge.EventApprove, forge.EventRequestChanges, forge.EventComment:
	default:
		return "", fmt.Errorf("github: unsupported review event %q", req.Event)
	}
	if !shaRe.MatchString(req.CommitID) {
		return "", fmt.Errorf("github: invalid review commit %q", req.CommitID)
	}
	comments := make([]ghReviewComment, 0, len(req.Comments))
	for i, cm := range req.Comments {
		if cm.Path == "" || cm.Line <= 0 || strings.TrimSpace(cm.Body) == "" {
			return "", fmt.Errorf("github: review comment %d needs a path, a positive line and a body", i)
		}
		comments = append(comments, ghReviewComment{Path: cm.Path, Line: cm.Line, Side: "RIGHT", Body: cm.Body})
	}

	viewer, err := c.Viewer(ctx)
	if err != nil {
		return "", err
	}
	if id, found, err := c.findMarker(ctx, repo, number, req.Marker, viewer.Login); err != nil {
		return "", err
	} else if found {
		return id, nil
	}

	pr, err := c.getPull(ctx, repo, number)
	if err != nil {
		return "", err
	}
	if st := pr.state(); st != "open" {
		return "", fmt.Errorf("%w: pull request #%d is %s", forge.ErrStale, number, st)
	}
	if !strings.EqualFold(pr.Head.SHA, req.CommitID) {
		return "", fmt.Errorf("%w: pull request #%d head is %s, review was for %s",
			forge.ErrStale, number, pr.Head.SHA, req.CommitID)
	}
	if (req.Event == forge.EventApprove || req.Event == forge.EventRequestChanges) &&
		strings.EqualFold(viewer.Login, pr.User.login()) {
		return "", fmt.Errorf("%w: %s is the author of pull request #%d and cannot %s it on GitHub",
			forge.ErrIneligible, viewer.Login, number, eventVerb(req.Event))
	}

	body := strings.TrimRight(req.Body, "\n")
	if body != "" {
		body += "\n\n"
	}
	body += MarkerComment(req.Marker)

	r, err := c.do(ctx, http.MethodPost, c.endpoint(path+"/reviews", nil), ghCreateReview{
		CommitID: pr.Head.SHA,
		Event:    string(req.Event),
		Body:     body,
		Comments: comments,
	}, true)
	if err != nil {
		return "", err
	}
	var created ghReview
	if err := json.Unmarshal(r.body, &created); err != nil || created.ID == 0 {
		return "", fmt.Errorf("github: POST %s/reviews: %w: HTTP %d but no review id in response",
			path, forge.ErrAmbiguous, r.status)
	}
	return strconv.FormatInt(created.ID, 10), nil
}

func eventVerb(e forge.ReviewEvent) string {
	if e == forge.EventApprove {
		return "approve"
	}
	return "request changes on"
}

// FindReviewByMarker scans the PR's reviews for one written by the
// credential's own account whose body carries MarkerComment(marker). Reviews
// by other accounts are ignored, so copying the marker cannot fake a
// publication. If the scan cannot be completed it returns an error rather
// than found == false.
func (c *Connector) FindReviewByMarker(ctx context.Context, repo forge.RepoRef, number int, marker string) (string, bool, error) {
	if err := validMarker(marker); err != nil {
		return "", false, err
	}
	viewer, err := c.Viewer(ctx)
	if err != nil {
		return "", false, err
	}
	return c.findMarker(ctx, repo, number, marker, viewer.Login)
}

func (c *Connector) findMarker(ctx context.Context, repo forge.RepoRef, number int, marker, login string) (string, bool, error) {
	list, more, err := c.listReviews(ctx, repo, number, markerMaxPages)
	if err != nil {
		return "", false, err
	}
	needle := MarkerComment(marker)
	for _, r := range list {
		if r.User == nil || !strings.EqualFold(r.User.Login, login) {
			continue
		}
		if strings.Contains(r.Body, needle) {
			return strconv.FormatInt(r.ID, 10), true, nil
		}
	}
	if more {
		return "", false, fmt.Errorf("%w: marker not found in the first %d pages of reviews", ErrTruncated, markerMaxPages)
	}
	return "", false, nil
}
