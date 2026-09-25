package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/binbandit/yip/internal/forge"
)

type whPRRef struct {
	Number int `json:"number"`
	Base   struct {
		Repo struct {
			ID int64 `json:"id"`
		} `json:"repo"`
	} `json:"base"`
}

type whCheck struct {
	HeadSHA      string    `json:"head_sha"`
	PullRequests []whPRRef `json:"pull_requests"`
}

type whPayload struct {
	Action      string  `json:"action"`
	Number      int     `json:"number"`
	Repository  *ghRepo `json:"repository"`
	PullRequest *struct {
		Number int      `json:"number"`
		Head   ghBranch `json:"head"`
	} `json:"pull_request"`
	CheckRun   *whCheck `json:"check_run"`
	CheckSuite *whCheck `json:"check_suite"`
	// status event
	SHA   string `json:"sha"`
	State string `json:"state"`
}

// VerifyWebhook authenticates a GitHub webhook delivery and extracts what yip
// needs to resynchronise a PR.
//
// The X-Hub-Signature-256 HMAC-SHA256 is checked (constant time) before
// anything else is read; a missing or wrong signature, or an empty secret,
// yields ErrWebhookSignature. X-GitHub-Delivery (the deduplication key) and
// X-GitHub-Event are required. Header names are matched case-insensitively.
//
// pull_request, pull_request_review, pull_request_review_comment and
// pull_request_review_thread set PRNumber and HeadSHA. check_run and
// check_suite set HeadSHA, and PRNumber only when the payload links exactly
// one PR in the same repository (fork PRs are not linked by GitHub; resolve
// them by HeadSHA). status sets HeadSHA and reports the status state as
// Action. Other events (e.g. ping) return delivery, event, action and repo.
func (c *Connector) VerifyWebhook(secret []byte, headers map[string]string, body []byte) (forge.Webhook, error) {
	if len(secret) == 0 {
		return forge.Webhook{}, fmt.Errorf("%w: no webhook secret configured", ErrWebhookSignature)
	}
	sig := header(headers, "X-Hub-Signature-256")
	if sig == "" {
		return forge.Webhook{}, fmt.Errorf("%w: X-Hub-Signature-256 header missing", ErrWebhookSignature)
	}
	hexSig, ok := strings.CutPrefix(sig, "sha256=")
	if !ok {
		return forge.Webhook{}, fmt.Errorf("%w: X-Hub-Signature-256 must start with sha256=", ErrWebhookSignature)
	}
	got, err := hex.DecodeString(hexSig)
	if err != nil || len(got) != sha256.Size {
		return forge.Webhook{}, fmt.Errorf("%w: X-Hub-Signature-256 is not a hex SHA-256 digest", ErrWebhookSignature)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return forge.Webhook{}, fmt.Errorf("%w: signature does not match payload", ErrWebhookSignature)
	}

	w := forge.Webhook{
		DeliveryID: header(headers, "X-GitHub-Delivery"),
		Event:      header(headers, "X-GitHub-Event"),
	}
	if w.DeliveryID == "" {
		return forge.Webhook{}, fmt.Errorf("%w: X-GitHub-Delivery header missing", ErrWebhookMalformed)
	}
	if w.Event == "" {
		return forge.Webhook{}, fmt.Errorf("%w: X-GitHub-Event header missing", ErrWebhookMalformed)
	}

	var p whPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return forge.Webhook{}, fmt.Errorf("%w: %s payload: %v", ErrWebhookMalformed, w.Event, err)
	}
	w.Action = p.Action
	var repoID int64
	if r := p.Repository; r != nil {
		repoID = r.ID
		w.Repo = forge.RepoRef{Host: c.host, Name: r.Name}
		if r.Owner != nil {
			w.Repo.Owner = r.Owner.Login
		}
		if (w.Repo.Owner == "" || w.Repo.Name == "") && r.FullName != "" {
			w.Repo.Owner, w.Repo.Name, _ = strings.Cut(r.FullName, "/")
		}
	}

	switch w.Event {
	case "pull_request", "pull_request_review", "pull_request_review_comment", "pull_request_review_thread":
		if p.PullRequest == nil {
			return forge.Webhook{}, fmt.Errorf("%w: %s payload has no pull_request", ErrWebhookMalformed, w.Event)
		}
		w.PRNumber = p.PullRequest.Number
		if w.PRNumber == 0 {
			w.PRNumber = p.Number
		}
		w.HeadSHA = p.PullRequest.Head.SHA
	case "check_run", "check_suite":
		chk := p.CheckRun
		if w.Event == "check_suite" {
			chk = p.CheckSuite
		}
		if chk == nil {
			return forge.Webhook{}, fmt.Errorf("%w: %s payload has no %s", ErrWebhookMalformed, w.Event, w.Event)
		}
		w.HeadSHA = chk.HeadSHA
		var nums []int
		for _, pr := range chk.PullRequests {
			if repoID == 0 || pr.Base.Repo.ID == 0 || pr.Base.Repo.ID == repoID {
				nums = append(nums, pr.Number)
			}
		}
		if len(nums) == 1 {
			w.PRNumber = nums[0]
		}
	case "status":
		w.HeadSHA = p.SHA
		if w.Action == "" {
			w.Action = p.State
		}
	}
	return w, nil
}

// header looks a header up case-insensitively.
func header(h map[string]string, key string) string {
	if v, ok := h[key]; ok {
		return strings.TrimSpace(v)
	}
	if v, ok := h[http.CanonicalHeaderKey(key)]; ok {
		return strings.TrimSpace(v)
	}
	for k, v := range h {
		if strings.EqualFold(k, key) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
