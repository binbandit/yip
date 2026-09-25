package cursor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/providers/acp"
	"github.com/binbandit/yip/protocol"
)

func TestParseModels(t *testing.T) {
	out := "\x1b[1mAvailable models:\x1b[0m\n\n* auto - Auto (current)\n  gpt-5 - GPT-5\n  claude-sonnet-4-6[thinking=true]  Claude Sonnet\n- grok-4\nTip: use --model <id>\n"
	got := parseModels(out)
	want := []protocol.Model{
		{ID: "auto", Label: "Auto", Default: true},
		{ID: "gpt-5", Label: "GPT-5"},
		{ID: "claude-sonnet-4-6[thinking=true]", Label: "Claude Sonnet"},
		{ID: "grok-4", Label: "grok-4"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("model %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if len(parseModels("No models available for this account.\n")) != 0 {
		t.Error("prose must not parse as models")
	}
}

func TestParseStatusJSON(t *testing.T) {
	cases := []struct {
		in, state, account string
		ok                 bool
	}{
		{`{"isAuthenticated":true,"email":"a@b.c"}`, protocol.AuthReady, "a@b.c", true},
		{`{"status":"not_authenticated"}`, protocol.AuthNeedsSignIn, "", true},
		{`{"auth":{"loggedIn":false}}`, protocol.AuthNeedsSignIn, "", true},
		{`{"endpoint":"x"}`, "", "", false},
		{`Logged in as a@b.c`, "", "", false},
	}
	for _, c := range cases {
		st, acct, ok := parseStatusJSON(c.in)
		if st != c.state || acct != c.account || ok != c.ok {
			t.Errorf("%s => %q %q %v", c.in, st, acct, ok)
		}
	}
	if accountFromText("You are logged in as dev@example.com.") != "dev@example.com" {
		t.Error("accountFromText")
	}
}

func TestRetryAfterOnlyFromVendorData(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		`{"retryAfterMs":1500}`:                          1500 * time.Millisecond,
		`{"retryAfter":"30"}`:                            30 * time.Second,
		`{"details":{"resetAt":"2026-09-25T12:05:00Z"}}`: 5 * time.Minute,
		`{"resetsAt":1790337900}`:                        time.Unix(1790337900, 0).Sub(now),
		`{"message":"slow down"}`:                        0,
		`"not an object"`:                                0,
		``:                                               0,
		`{"resetAt":"2026-09-25T11:00:00Z","retryAfter":0}`: 0,
	}
	for in, want := range cases {
		if got := retryAfterFrom(json.RawMessage(in), now); got != want {
			t.Errorf("%s => %s, want %s", in, got, want)
		}
	}
}

func TestClassifyRPC(t *testing.T) {
	if f := classifyRPC(&acp.Error{Code: -32000, Message: "Authentication required"}); f.outcome != protocol.OutcomeAuthRequired {
		t.Errorf("auth code => %+v", f)
	}
	if f := classifyRPC(&acp.Error{Code: -32603, Message: "Too many requests", Data: json.RawMessage(`{"retryAfter":12}`)}); f.outcome != protocol.OutcomeRateLimited || f.retryAfter != 12*time.Second {
		t.Errorf("rate => %+v", f)
	}
	if f := classifyRPC(&acp.Error{Code: -32603, Message: "You've hit your usage limit"}); f.outcome != protocol.OutcomeRateLimited || f.retryAfter != 0 {
		t.Errorf("usage limit without data => %+v", f)
	}
	if f := classifyRPC(&acp.Error{Code: -32603, Message: "boom"}); f.outcome != protocol.OutcomeFailed {
		t.Errorf("other => %+v", f)
	}
	if f := classifyText("exited", "Error: not logged in. Run agent login"); f.outcome != protocol.OutcomeAuthRequired {
		t.Errorf("stderr auth => %+v", f)
	}
}

func TestAnswerAskMultiQuestion(t *testing.T) {
	p := askQuestionParams{Questions: []cursorQuestion{
		{ID: "db", Prompt: "Database?", Options: []cursorOption{{ID: "pg", Label: "Postgres"}, {ID: "lite", Label: "SQLite"}}},
		{ID: "feat", Prompt: "Features?", AllowMultiple: true, Options: []cursorOption{{ID: "a", Label: "Auth"}, {ID: "b", Label: "Billing"}, {ID: "c", Label: "Chat"}}},
	}}
	text, opts, flat := askQuestionView(p)
	if len(opts) != 5 || opts[0] != "Database? — Postgres" || text == "" {
		t.Fatalf("view = %q %v", text, opts)
	}
	got, _ := json.Marshal(answerAsk(p, flat, answerInput{Text: "postgres, Auth, chat", Selected: -1}))
	want := `{"outcome":{"outcome":"answered","answers":[{"questionId":"db","selectedOptionIds":["pg"]},{"questionId":"feat","selectedOptionIds":["a","c"]}]}}`
	if string(got) != want {
		t.Errorf("text answer = %s\nwant %s", got, want)
	}
	got, _ = json.Marshal(answerAsk(p, flat, answerInput{Selected: 3}))
	if string(got) != `{"outcome":{"outcome":"answered","answers":[{"questionId":"feat","selectedOptionIds":["b"]}]}}` {
		t.Errorf("selected answer = %s", got)
	}
	got, _ = json.Marshal(answerAsk(p, flat, answerInput{Text: "use whatever is cheapest", Selected: -1}))
	if string(got) != `{"outcome":{"outcome":"skipped","reason":"The user replied in free text instead of choosing an option: use whatever is cheapest"}}` {
		t.Errorf("free text = %s", got)
	}
	got, _ = json.Marshal(answerAsk(p, flat, answerInput{Selected: 99}))
	if string(got) != `{"outcome":{"outcome":"skipped","reason":"The user did not choose an option."}}` {
		t.Errorf("out of range = %s", got)
	}
}

func TestAnswerPlan(t *testing.T) {
	cases := map[answerInput]string{
		{Selected: 0}:                         `{"outcome":{"outcome":"accepted"}}`,
		{Selected: 1, Text: "too risky"}:      `{"outcome":{"outcome":"rejected","reason":"too risky"}}`,
		{Selected: -1, Text: "LGTM"}:          `{"outcome":{"outcome":"accepted"}}`,
		{Selected: -1, Text: "split step 2"}:  `{"outcome":{"outcome":"rejected","reason":"split step 2"}}`,
		{Selected: -1, Declined: true}:        `{"outcome":{"outcome":"rejected","reason":"` + declinedPlanReason + `"}}`,
		{Selected: 0, Declined: true}:         `{"outcome":{"outcome":"rejected","reason":"` + declinedPlanReason + `"}}`,
		{Selected: -1}:                        `{"outcome":{"outcome":"rejected","reason":"The user did not approve the plan."}}`,
		{Selected: -1, Text: "Approve plan."}: `{"outcome":{"outcome":"rejected","reason":"Approve plan."}}`,
	}
	for in, want := range cases {
		got, _ := json.Marshal(answerPlan(in))
		if string(got) != want {
			t.Errorf("%+v => %s\nwant %s", in, got, want)
		}
	}
}

func TestOutsideWorkdir(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.EvalSymlinks(link)
	cases := map[string]bool{
		filepath.Join(link, "src", "new.go"):        false, // through the symlink, new file
		filepath.Join(real, "src", "a.go"):          false,
		"src/rel.go":                                false,
		"../escape.go":                              true,
		filepath.Join(real, "..", "other", "x"):     true,
		"/etc/hosts":                                true,
		filepath.Join(link, "src", "..", "..", "x"): true,
	}
	for p, want := range cases {
		if _, got := outsideWorkdir(root, []string{p}); got != want {
			t.Errorf("%s outside=%v, want %v", p, got, want)
		}
	}
}

func TestApprovalActionKinds(t *testing.T) {
	a := approvalAction(acp.ToolCall{Kind: acp.KindFetch, RawInput: json.RawMessage(`{"url":"https://docs.example.com"}`)})
	if a.Kind != "network" || a.Target != "https://docs.example.com" || a.Summary != "Fetch https://docs.example.com" {
		t.Errorf("fetch = %+v", a)
	}
	a = approvalAction(acp.ToolCall{Kind: acp.KindOther, Title: "MCP: yip.post_message"})
	if a.Kind != "mcp" {
		t.Errorf("mcp = %+v", a)
	}
	a = approvalAction(acp.ToolCall{Kind: acp.KindExecute, RawInput: json.RawMessage(`{"command":["go","test","./..."]}`)})
	if a.Kind != "exec" || a.Command != "go test ./..." || a.Summary != "Run: go test ./..." {
		t.Errorf("exec = %+v", a)
	}
}

func TestBuildFirstPrompt(t *testing.T) {
	if got := buildFirstPrompt(providers.StartSpec{Mode: protocol.ModeEdit, Prompt: "hi"}); got != "hi" {
		t.Errorf("no instructions => %q", got)
	}
	got := buildFirstPrompt(providers.StartSpec{Mode: protocol.ModeEdit, Instructions: "Rules.", Prompt: "hi"})
	if got[:len("<yip_instructions>")] != "<yip_instructions>" || got[len(got)-2:] != "hi" {
		t.Errorf("with instructions => %q", got)
	}
}
