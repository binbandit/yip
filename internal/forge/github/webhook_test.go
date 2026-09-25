package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/binbandit/yip/internal/forge"
)

var webhookSecret = []byte("It's a Secret to Everybody")

func sign(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func hookHeaders(event, delivery string, body []byte) map[string]string {
	return map[string]string{
		"X-GitHub-Event":                         event,
		"X-GitHub-Delivery":                      delivery,
		"X-GitHub-Hook-ID":                       "292430182",
		"X-Hub-Signature-256":                    sign(webhookSecret, body),
		"User-Agent":                             "GitHub-Hookshot/044aadd",
		"Content-Type":                           "application/json",
		"X-GitHub-Hook-Installation-Target-Type": "repository",
	}
}

const repoPayload = `"repository":{"id":1296269,"node_id":"MDEwOlJlcG9zaXRvcnkxMjk2MjY5","name":"widgets","full_name":"acme/widgets","private":false,"owner":{"login":"acme","id":1,"type":"Organization"}},"sender":{"login":"mira","id":2,"type":"User"}`

func TestWebhookSignatureKnownVector(t *testing.T) {
	// Example from GitHub's "Validating webhook deliveries" documentation.
	body := []byte("Hello, World!")
	want := "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
	if got := sign(webhookSecret, body); got != want {
		t.Fatalf("test signer = %s, want %s", got, want)
	}
	h := map[string]string{"X-Hub-Signature-256": want, "X-GitHub-Delivery": "d-1", "X-GitHub-Event": "ping"}
	_, err := New(Options{}).VerifyWebhook(webhookSecret, h, body)
	// Signature matches; the body is not JSON, so it fails later as malformed.
	if !errors.Is(err, ErrWebhookMalformed) {
		t.Fatalf("err = %v, want ErrWebhookMalformed (signature accepted)", err)
	}
}

func TestWebhookPullRequest(t *testing.T) {
	body := []byte(`{"action":"synchronize","number":42,"before":"` + shaB + `","after":"` + shaA + `","pull_request":{"url":"https://api.github.com/repos/acme/widgets/pulls/42","number":42,"state":"open","head":{"ref":"feature","sha":"` + shaA + `"},"base":{"ref":"main","sha":"` + shaBase + `"}},` + repoPayload + `}`)
	c := New(Options{})
	w, err := c.VerifyWebhook(webhookSecret, hookHeaders("pull_request", "72d3162e-cc78-11e3-81ab-4c9367dc0958", body), body)
	if err != nil {
		t.Fatal(err)
	}
	want := forge.Webhook{
		DeliveryID: "72d3162e-cc78-11e3-81ab-4c9367dc0958",
		Event:      "pull_request",
		Action:     "synchronize",
		Repo:       forge.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"},
		PRNumber:   42,
		HeadSHA:    shaA,
	}
	if w != want {
		t.Fatalf("webhook =\n%+v\nwant\n%+v", w, want)
	}

	// Header names are case-insensitive (e.g. lower-cased by a proxy or HTTP/2).
	lower := map[string]string{}
	for k, v := range hookHeaders("pull_request", "abc", body) {
		lower[strings.ToLower(k)] = v
	}
	w, err = c.VerifyWebhook(webhookSecret, lower, body)
	if err != nil || w.DeliveryID != "abc" || w.PRNumber != 42 {
		t.Fatalf("lower-case headers: %+v %v", w, err)
	}
}

func TestWebhookRejectsBadSignature(t *testing.T) {
	body := []byte(`{"action":"opened","pull_request":{"number":1,"head":{"sha":"` + shaA + `"}},` + repoPayload + `}`)
	c := New(Options{})
	cases := map[string]func(h map[string]string) ([]byte, []byte){
		"wrong secret": func(h map[string]string) ([]byte, []byte) {
			h["X-Hub-Signature-256"] = sign([]byte("other"), body)
			return webhookSecret, body
		},
		"tampered body": func(h map[string]string) ([]byte, []byte) {
			return webhookSecret, []byte(strings.Replace(string(body), `"number":1`, `"number":2`, 1))
		},
		"missing": func(h map[string]string) ([]byte, []byte) {
			delete(h, "X-Hub-Signature-256")
			h["X-Hub-Signature"] = "sha1=0000000000000000000000000000000000000000"
			return webhookSecret, body
		},
		"no prefix": func(h map[string]string) ([]byte, []byte) {
			h["X-Hub-Signature-256"] = strings.TrimPrefix(h["X-Hub-Signature-256"], "sha256=")
			return webhookSecret, body
		},
		"not hex": func(h map[string]string) ([]byte, []byte) {
			h["X-Hub-Signature-256"] = "sha256=zz"
			return webhookSecret, body
		},
		"truncated digest": func(h map[string]string) ([]byte, []byte) {
			h["X-Hub-Signature-256"] = h["X-Hub-Signature-256"][:20]
			return webhookSecret, body
		},
		"empty secret": func(h map[string]string) ([]byte, []byte) {
			h["X-Hub-Signature-256"] = sign(nil, body)
			return nil, body
		},
	}
	for name, mutate := range cases {
		h := hookHeaders("pull_request", "d-1", body)
		secret, b := mutate(h)
		if _, err := c.VerifyWebhook(secret, h, b); !errors.Is(err, ErrWebhookSignature) {
			t.Errorf("%s: err = %v, want ErrWebhookSignature", name, err)
		}
	}
}

func TestWebhookRequiresDeliveryAndEvent(t *testing.T) {
	body := []byte(`{"zen":"Keep it logically awesome.","hook_id":1,` + repoPayload + `}`)
	c := New(Options{})
	for _, drop := range []string{"X-GitHub-Delivery", "X-GitHub-Event"} {
		h := hookHeaders("ping", "d-1", body)
		delete(h, drop)
		if _, err := c.VerifyWebhook(webhookSecret, h, body); !errors.Is(err, ErrWebhookMalformed) {
			t.Errorf("without %s: err = %v, want ErrWebhookMalformed", drop, err)
		}
	}
	w, err := c.VerifyWebhook(webhookSecret, hookHeaders("ping", "d-2", body), body)
	if err != nil || w.Event != "ping" || w.DeliveryID != "d-2" || w.Repo.Name != "widgets" || w.PRNumber != 0 {
		t.Fatalf("ping: %+v %v", w, err)
	}
}

func TestWebhookEvents(t *testing.T) {
	prRef := func(n int, repoID int) string {
		return `{"url":"https://api.github.com/repos/acme/widgets/pulls/` + itoa(int64(n)) + `","id":` + itoa(int64(n*10)) + `,"number":` + itoa(int64(n)) +
			`,"head":{"ref":"feature","sha":"` + shaA + `","repo":{"id":` + itoa(int64(repoID)) + `,"url":"x","name":"widgets"}},"base":{"ref":"main","sha":"` + shaBase + `","repo":{"id":` + itoa(int64(repoID)) + `,"url":"x","name":"widgets"}}}`
	}
	cases := []struct {
		event, body string
		action      string
		pr          int
		head        string
	}{
		{
			event:  "pull_request_review",
			body:   `{"action":"submitted","review":{"id":80,"user":{"login":"kai"},"state":"approved","commit_id":"` + shaB + `"},"pull_request":{"number":7,"head":{"sha":"` + shaA + `"}},` + repoPayload + `}`,
			action: "submitted", pr: 7, head: shaA,
		},
		{
			event:  "check_run",
			body:   `{"action":"completed","check_run":{"id":4,"head_sha":"` + shaA + `","status":"completed","conclusion":"failure","pull_requests":[` + prRef(7, 1296269) + `]},` + repoPayload + `}`,
			action: "completed", pr: 7, head: shaA,
		},
		{
			// Same head in two PRs: ambiguous, caller resolves by HeadSHA.
			event:  "check_suite",
			body:   `{"action":"completed","check_suite":{"id":5,"head_branch":"feature","head_sha":"` + shaA + `","status":"completed","conclusion":"success","pull_requests":[` + prRef(7, 1296269) + `,` + prRef(8, 1296269) + `]},` + repoPayload + `}`,
			action: "completed", pr: 0, head: shaA,
		},
		{
			// A PR whose base is another repository is not this repo's PR.
			event:  "check_run",
			body:   `{"action":"created","check_run":{"id":4,"head_sha":"` + shaA + `","pull_requests":[` + prRef(3, 999) + `]},` + repoPayload + `}`,
			action: "created", pr: 0, head: shaA,
		},
		{
			event:  "status",
			body:   `{"id":1,"sha":"` + shaA + `","name":"acme/widgets","target_url":null,"context":"ci/legacy","description":null,"state":"failure","commit":{"sha":"` + shaA + `"},"branches":[],` + repoPayload + `}`,
			action: "failure", pr: 0, head: shaA,
		},
	}
	c := New(Options{})
	for _, tc := range cases {
		b := []byte(tc.body)
		w, err := c.VerifyWebhook(webhookSecret, hookHeaders(tc.event, "d-"+tc.event, b), b)
		if err != nil {
			t.Fatalf("%s: %v", tc.event, err)
		}
		if w.Event != tc.event || w.Action != tc.action || w.PRNumber != tc.pr || w.HeadSHA != tc.head ||
			w.Repo != (forge.RepoRef{Host: "github.com", Owner: "acme", Name: "widgets"}) || w.DeliveryID != "d-"+tc.event {
			t.Errorf("%s: got %+v", tc.event, w)
		}
	}

	// A signed pull_request event without its pull_request object is malformed.
	b := []byte(`{"action":"opened",` + repoPayload + `}`)
	if _, err := c.VerifyWebhook(webhookSecret, hookHeaders("pull_request", "d", b), b); !errors.Is(err, ErrWebhookMalformed) {
		t.Fatalf("err = %v, want ErrWebhookMalformed", err)
	}
}
