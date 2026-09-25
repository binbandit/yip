// Package forge defines the forge-neutral pull-request and review contract.
//
// yip records two separate facts about a pull request: what yip's engineers
// concluded internally (reviews bound to exact revisions), and what the forge
// itself reports (remote reviews, checks, merge eligibility). A connector must
// never blur them: an internal approval is not a remote approval, several
// engineers sharing one forge credential are one remote actor, and a PR author
// cannot approve their own PR on GitHub.
package forge

import (
	"context"
	"errors"
	"time"

	"github.com/binbandit/yip/protocol"
)

var (
	// ErrNotFound means the PR or repository does not exist or is not visible
	// to the configured credential.
	ErrNotFound = errors.New("forge: not found")
	// ErrForbidden means the credential lacks permission for the action.
	ErrForbidden = errors.New("forge: forbidden")
	// ErrStale means the PR head moved away from the expected revision.
	ErrStale = errors.New("forge: target revision changed")
	// ErrIneligible means the remote actor cannot perform this review action
	// (e.g. the PR author trying to approve their own PR).
	ErrIneligible = errors.New("forge: remote actor is not eligible")
	// ErrAmbiguous means a write may or may not have happened (timeout,
	// connection reset). Callers must reconcile before retrying.
	ErrAmbiguous = errors.New("forge: outcome unknown")
)

// RepoRef identifies a repository on a forge host.
type RepoRef struct {
	Host  string // e.g. github.com
	Owner string
	Name  string
}

// PR is the forge's current view of a pull request.
type PR struct {
	Repo      RepoRef
	Number    int
	URL       string // canonical web URL
	Title     string
	State     string // open | closed | merged
	Base      string // base commit SHA
	BaseRef   string // base branch name
	Head      string // head commit SHA
	HeadRef   string
	Author    string // remote login of the PR author
	Draft     bool
	UpdatedAt time.Time
}

// FileDiff is one file's patch at an exact base/head pair.
type FileDiff struct {
	Path      string
	Status    string // added | modified | removed | renamed
	Additions int
	Deletions int
	Patch     string
}

// Actor is the authenticated remote identity behind a credential.
type Actor struct {
	Login string
	Type  string // User | Bot
}

// ReviewEvent is the kind of remote review to publish.
type ReviewEvent string

const (
	EventApprove        ReviewEvent = "APPROVE"
	EventRequestChanges ReviewEvent = "REQUEST_CHANGES"
	EventComment        ReviewEvent = "COMMENT"
)

// ReviewComment is a line comment bound to the review's commit.
type ReviewComment struct {
	Path string
	Line int
	Body string
}

// PublishReview is a revision-bound review publication request.
type PublishReview struct {
	// CommitID must equal the PR head the internal review verified.
	CommitID string
	Event    ReviewEvent
	Body     string
	Comments []ReviewComment
	// Marker is an idempotency marker embedded invisibly in the body so a
	// publication can be found again after an ambiguous failure.
	Marker string
}

// Connector is implemented by each forge (GitHub first).
type Connector interface {
	Name() string
	// Viewer returns the remote actor the configured credential acts as.
	Viewer(ctx context.Context) (Actor, error)
	// ParseURL resolves a web URL to a repository and PR number.
	ParseURL(url string) (RepoRef, int, error)
	GetPR(ctx context.Context, repo RepoRef, number int) (PR, error)
	// Files returns the diff between the exact base and head of the PR.
	Files(ctx context.Context, repo RepoRef, number int) ([]FileDiff, error)
	// Checks summarizes check runs and commit statuses for a commit.
	Checks(ctx context.Context, repo RepoRef, sha string) (protocol.ChecksSummary, error)
	Reviews(ctx context.Context, repo RepoRef, number int) ([]protocol.RemoteReview, error)
	// MergeStatus reads the forge's own mergeability and protection state.
	MergeStatus(ctx context.Context, repo RepoRef, number int) (protocol.MergeStatus, error)
	// PublishReview re-fetches the PR, verifies the head still equals
	// req.CommitID (ErrStale otherwise), checks actor eligibility
	// (ErrIneligible), and publishes. Timeouts return ErrAmbiguous.
	PublishReview(ctx context.Context, repo RepoRef, number int, req PublishReview) (externalID string, err error)
	// FindReviewByMarker reconciles an ambiguous publication.
	FindReviewByMarker(ctx context.Context, repo RepoRef, number int, marker string) (externalID string, found bool, err error)
	// VerifyWebhook validates a signed webhook delivery and returns its
	// delivery ID (for deduplication), event name, and PR identity if any.
	VerifyWebhook(secret []byte, headers map[string]string, body []byte) (Webhook, error)
}

// Webhook is a verified forge delivery.
type Webhook struct {
	DeliveryID string
	Event      string
	Action     string
	Repo       RepoRef
	PRNumber   int
	HeadSHA    string
}
