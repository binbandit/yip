// Package fake is yip's deterministic provider. It never calls a model: it
// executes a script chosen by the hub's director from the run's manifest.
// Scripts run real shell commands and file edits in the run's workspace and
// call yip tools through the real `yip bridge` over MCP, exactly as a provider
// CLI would. It can also inject faults (crash, hang, rate limit, sign-in
// failure) at known points for failure testing.
//
// The fake provider is always labelled as such in the product. It exists for
// automated verification and the demo, not as a stand-in for real providers.
package fake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

// Script is a deterministic sequence of steps.
type Script struct {
	Steps []Step `json:"steps"`
}

// Step is one scripted action. Exactly one action field is set.
type Step struct {
	Say       string                   `json:"say,omitempty"`
	Status    string                   `json:"status,omitempty"`
	Tool      string                   `json:"tool,omitempty"`
	Args      json.RawMessage          `json:"args,omitempty"`
	Save      string                   `json:"save,omitempty"`
	Shell     string                   `json:"shell,omitempty"`
	Write     *WriteFile               `json:"write,omitempty"`
	Replace   *ReplaceText             `json:"replace,omitempty"`
	Approval  *protocol.ApprovalAction `json:"approval,omitempty"`
	If        *Cond                    `json:"if,omitempty"`
	Then      []Step                   `json:"then,omitempty"`
	Else      []Step                   `json:"else,omitempty"`
	Sleep     string                   `json:"sleep,omitempty"`
	Pause     bool                     `json:"pause,omitempty"` // a paced pause (scaled by the runner's fake delay)
	Fault     string                   `json:"fault,omitempty"` // crash | hang | rate_limit | auth
	FaultArg  string                   `json:"faultArg,omitempty"`
	Final     string                   `json:"final,omitempty"`
	Summarize string                   `json:"summarize,omitempty"` // var holding a work_status result → final text
}

type WriteFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type ReplaceText struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

// Cond tests a saved variable.
type Cond struct {
	Var      string `json:"var"`
	Eq       any    `json:"eq,omitempty"`
	Contains string `json:"contains,omitempty"`
	Truthy   bool   `json:"truthy,omitempty"`
}

// Adapter is the fake provider.
type Adapter struct {
	// Delay paces scripted pauses so humans can watch (and steer) a demo.
	Delay time.Duration
}

func New(delay time.Duration) *Adapter { return &Adapter{Delay: delay} }

func (a *Adapter) Name() string  { return "fake" }
func (a *Adapter) Label() string { return "Fake provider (deterministic)" }

func (a *Adapter) Probe(ctx context.Context) protocol.ProviderInstallation {
	return protocol.ProviderInstallation{Provider: "fake", Version: "1", Path: "built-in", AuthState: protocol.AuthReady,
		AuthDetail: "Deterministic scripted provider; no model or account is used.", Account: "local", Billing: protocol.BillingUnknown,
		ProfileID: "fake:local", Tested: true, TestedVersion: "1",
		Capabilities: protocol.ProviderCapabilities{StructuredEvents: true, ToolApprovals: true, SessionResume: false, ActiveSteering: true,
			ReadOnly: true, MCPTools: true},
		Models:      []protocol.Model{{ID: "scripted", Label: "Scripted", Default: true}},
		Limitations: []string{"Follows scripted demo and test workflows only; it cannot handle open-ended requests."},
		UpdatedAt:   time.Now().UTC()}
}

func (a *Adapter) Start(ctx context.Context, spec providers.StartSpec) (providers.Session, error) {
	var script Script
	if len(spec.FakeScript) > 0 {
		if err := json.Unmarshal(spec.FakeScript, &script); err != nil {
			return nil, fmt.Errorf("fake script: %w", err)
		}
	}
	if spec.Env == nil {
		spec.Env = providers.BaseEnv(nil)
	}
	env := append([]string{}, spec.Env...)
	for k, v := range spec.MCP.Env {
		env = append(env, k+"="+v)
	}
	cmd := exec.Command(spec.MCP.Command, spec.MCP.Args...)
	cmd.Env = env
	cmd.Dir = spec.Workdir
	cmd.Stderr = os.Stderr
	client, err := bridge.StartMCP(cmd)
	if err != nil {
		return nil, fmt.Errorf("start yip bridge: %w", err)
	}
	sctx, cancel := context.WithCancel(context.Background())
	s := &session{spec: spec, client: client, events: make(chan providers.Event, 256), cancel: cancel, ctx: sctx,
		vars: map[string]any{}, approvals: map[string]chan providers.ApprovalDecision{}, delay: a.Delay, done: make(chan struct{})}
	go s.run(script)
	return s, nil
}

type session struct {
	spec      providers.StartSpec
	client    *bridge.MCPClient
	events    chan providers.Event
	ctx       context.Context
	cancel    context.CancelFunc
	delay     time.Duration
	mu        sync.Mutex
	vars      map[string]any
	inputs    []string
	approvals map[string]chan providers.ApprovalDecision
	final     strings.Builder
	result    providers.Result
	done      chan struct{}
	seq       int
}

func (s *session) Events() <-chan providers.Event { return s.events }

func (s *session) emit(e providers.Event) {
	e.At = time.Now().UTC()
	select {
	case s.events <- e:
	case <-s.ctx.Done():
	}
}

func (s *session) SendInput(ctx context.Context, text string) (string, error) {
	s.mu.Lock()
	s.inputs = append(s.inputs, text)
	s.vars["input"] = text
	s.mu.Unlock()
	s.emit(providers.Event{Kind: providers.EventStatus, Text: "Received your update: " + truncate(text, 120)})
	return "immediate", nil
}

func (s *session) ResolveApproval(ctx context.Context, id string, d providers.ApprovalDecision) error {
	s.mu.Lock()
	ch := s.approvals[id]
	delete(s.approvals, id)
	s.mu.Unlock()
	if ch == nil {
		return errors.New("no pending approval " + id)
	}
	ch <- d
	return nil
}

func (s *session) AnswerQuestion(ctx context.Context, id string, a providers.QuestionAnswer) error {
	return providers.ErrUnsupported
}

func (s *session) Cancel(ctx context.Context) error {
	s.cancel()
	select {
	case <-s.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (s *session) Wait() providers.Result {
	<-s.done
	return s.result
}

var errStop = errors.New("stopped")

type faultError struct{ r providers.Result }

func (f faultError) Error() string { return f.r.Error }

func (s *session) run(script Script) {
	defer close(s.done)
	defer close(s.events)
	err := s.steps(script.Steps)
	_ = s.client.Close()
	var fe faultError
	switch {
	case errors.As(err, &fe):
		s.result = fe.r
	case s.ctx.Err() != nil:
		s.result = providers.Result{Outcome: protocol.OutcomeCancelled, ExitConfirmed: true, FinalText: s.final.String()}
	case err != nil:
		s.result = providers.Result{Outcome: protocol.OutcomeFailed, Error: err.Error(), ExitConfirmed: true, FinalText: s.final.String()}
	default:
		text := strings.TrimSpace(s.final.String())
		if text != "" {
			s.emit(providers.Event{Kind: providers.EventMessage, Text: text})
		}
		s.result = providers.Result{Outcome: protocol.OutcomeSucceeded, FinalText: text, ExitConfirmed: true,
			Usage: &protocol.Usage{Source: "unknown", Billing: protocol.BillingUnknown}}
	}
}

func (s *session) steps(steps []Step) error {
	for _, st := range steps {
		if s.ctx.Err() != nil {
			return errStop
		}
		if err := s.step(st); err != nil {
			return err
		}
	}
	return nil
}

func (s *session) step(st Step) error {
	switch {
	case st.Say != "":
		text := s.render(st.Say)
		for _, chunk := range chunks(text, 24) {
			s.emit(providers.Event{Kind: providers.EventMessageDelta, Text: chunk})
		}
		s.emit(providers.Event{Kind: providers.EventMessage, Text: text})
	case st.Status != "":
		s.emit(providers.Event{Kind: providers.EventStatus, Text: s.render(st.Status)})
	case st.Final != "":
		if s.final.Len() > 0 {
			s.final.WriteString("\n\n")
		}
		s.final.WriteString(s.render(st.Final))
	case st.Summarize != "":
		s.final.WriteString(s.summarize(st.Summarize))
	case st.Pause:
		return s.sleep(s.delay)
	case st.Sleep != "":
		d, err := time.ParseDuration(st.Sleep)
		if err != nil {
			return err
		}
		return s.sleep(d)
	case st.Tool != "":
		return s.tool(st)
	case st.Shell != "":
		return s.shell(st)
	case st.Write != nil:
		p, err := s.path(st.Write.Path)
		if err != nil {
			return err
		}
		s.emit(providers.Event{Kind: providers.EventToolStarted, Tool: "write", Text: "Writing " + st.Write.Path})
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(s.render(st.Write.Content)), 0o644); err != nil {
			return err
		}
		s.emit(providers.Event{Kind: providers.EventToolFinished, Tool: "write", Text: "Wrote " + st.Write.Path})
	case st.Replace != nil:
		p, err := s.path(st.Replace.Path)
		if err != nil {
			return err
		}
		s.emit(providers.Event{Kind: providers.EventToolStarted, Tool: "edit", Text: "Editing " + st.Replace.Path})
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		old := s.render(st.Replace.Old)
		if !strings.Contains(string(b), old) {
			return fmt.Errorf("fake script: %s does not contain the expected text", st.Replace.Path)
		}
		if err := os.WriteFile(p, []byte(strings.Replace(string(b), old, s.render(st.Replace.New), 1)), 0o644); err != nil {
			return err
		}
		s.emit(providers.Event{Kind: providers.EventToolFinished, Tool: "edit", Text: "Edited " + st.Replace.Path})
	case st.Approval != nil:
		s.seq++
		id := fmt.Sprintf("fake-approval-%d", s.seq)
		ch := make(chan providers.ApprovalDecision, 1)
		s.mu.Lock()
		s.approvals[id] = ch
		s.mu.Unlock()
		a := *st.Approval
		a.Command, a.Summary = s.render(a.Command), s.render(a.Summary)
		s.emit(providers.Event{Kind: providers.EventApprovalRequest, Approval: &providers.ApprovalRequest{ID: id, Action: a}})
		select {
		case d := <-ch:
			s.set(st.Save, map[string]any{"allowed": d.Allow, "reason": d.Reason})
		case <-s.ctx.Done():
			return errStop
		}
	case st.If != nil:
		if s.test(*st.If) {
			return s.steps(st.Then)
		}
		return s.steps(st.Else)
	case st.Fault != "":
		return s.fault(st)
	}
	return nil
}

func (s *session) sleep(d time.Duration) error {
	if d <= 0 {
		return nil
	}
	select {
	case <-time.After(d):
		return nil
	case <-s.ctx.Done():
		return errStop
	}
}

func (s *session) fault(st Step) error {
	switch st.Fault {
	case "crash":
		return faultError{providers.Result{Outcome: protocol.OutcomeFailed, Error: "fake provider crashed (scripted fault)", ExitConfirmed: true}}
	case "hang":
		<-s.ctx.Done()
		return errStop
	case "rate_limit":
		var retry time.Duration
		if st.FaultArg != "" {
			retry, _ = time.ParseDuration(st.FaultArg)
		}
		s.emit(providers.Event{Kind: providers.EventRateLimited, Text: "Usage limit reached (scripted fault)", RetryAfter: retry})
		return faultError{providers.Result{Outcome: protocol.OutcomeRateLimited, Error: "usage limit reached (scripted fault)", RetryAfter: retry, ExitConfirmed: true}}
	case "auth":
		s.emit(providers.Event{Kind: providers.EventAuthRequired, Text: "Sign-in required (scripted fault)"})
		return faultError{providers.Result{Outcome: protocol.OutcomeAuthRequired, Error: "sign-in expired (scripted fault)", ExitConfirmed: true}}
	}
	return fmt.Errorf("unknown fault %q", st.Fault)
}

func (s *session) tool(st Step) error {
	var args any
	if len(st.Args) > 0 {
		if err := json.Unmarshal(st.Args, &args); err != nil {
			return fmt.Errorf("fake script args for %s: %w", st.Tool, err)
		}
	} else {
		args = map[string]any{}
	}
	args = s.renderValue(args)
	s.emit(providers.Event{Kind: providers.EventToolStarted, Tool: "mcp__yip__" + st.Tool, Text: toolLabel(st.Tool)})
	text, isErr, err := s.client.CallTool(st.Tool, args)
	if err != nil {
		return err
	}
	var v any
	if json.Unmarshal([]byte(text), &v) != nil {
		v = text
	}
	if isErr {
		s.set(st.Save, map[string]any{"error": v, "ok": false})
		s.emit(providers.Event{Kind: providers.EventToolFinished, Tool: "mcp__yip__" + st.Tool, Text: toolLabel(st.Tool) + " failed"})
		s.emit(providers.Event{Kind: providers.EventWarning, Text: st.Tool + ": " + truncate(text, 400)})
		if st.Save == "" {
			return fmt.Errorf("%s failed: %s", st.Tool, truncate(text, 300))
		}
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		m["ok"] = true
	}
	s.set(st.Save, v)
	s.emit(providers.Event{Kind: providers.EventToolFinished, Tool: "mcp__yip__" + st.Tool, Text: toolLabel(st.Tool)})
	return nil
}

func toolLabel(t string) string {
	return map[string]string{
		bridge.RoomPost: "Posting to the conversation", bridge.WorkCreate: "Creating work", bridge.WorkRunCheck: "Running checks",
		bridge.WorkPublishRev: "Publishing the revision", bridge.WorkRequestReview: "Requesting review", bridge.WorkReview: "Recording the review",
		bridge.WorkRespondReview: "Responding to review", bridge.WorkUpdate: "Updating the job", bridge.HumanAsk: "Asking in the conversation",
		bridge.WorkWait: "Waiting", bridge.ArtifactPublish: "Publishing an artifact", bridge.DecisionPropose: "Recording a decision",
		bridge.WorkStatus: "Reading the work ledger", bridge.KnowledgeSearch: "Searching knowledge",
	}[t]
}

func (s *session) shell(st Step) error {
	command := s.render(st.Shell)
	s.emit(providers.Event{Kind: providers.EventToolStarted, Tool: "shell", Text: "Running " + truncate(command, 100)})
	cmd := exec.CommandContext(s.ctx, "/bin/sh", "-c", command)
	cmd.Dir = s.spec.Workdir
	cmd.Env = s.spec.Env
	out, err := cmd.CombinedOutput()
	exit := providers.ExitCode(err)
	if s.ctx.Err() != nil {
		return errStop
	}
	res := map[string]any{"out": strings.TrimSpace(string(out)), "exit": exit}
	// Convenience: "file:line" of the first grep-style match.
	if m := regexp.MustCompile(`(?m)^([^:\n]+):(\d+):`).FindStringSubmatch(string(out)); m != nil {
		res["file"], res["line"] = m[1], atoi(m[2])
	} else if m := regexp.MustCompile(`(?m)^(\d+):`).FindStringSubmatch(string(out)); m != nil {
		res["line"] = atoi(m[1])
	}
	s.set(st.Save, res)
	s.emit(providers.Event{Kind: providers.EventToolFinished, Tool: "shell", Text: fmt.Sprintf("Ran %s (exit %d)", truncate(command, 80), exit)})
	return nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// path resolves a script path inside the workspace.
func (s *session) path(rel string) (string, error) {
	rel = s.render(rel)
	if filepath.IsAbs(rel) || strings.HasPrefix(filepath.Clean(rel), "..") {
		return "", fmt.Errorf("fake script path %q escapes the workspace", rel)
	}
	if s.spec.Mode != protocol.ModeEdit {
		return "", fmt.Errorf("fake script tried to write in a %s run", s.spec.Mode)
	}
	return filepath.Join(s.spec.Workdir, rel), nil
}

func (s *session) set(name string, v any) {
	if name == "" {
		return
	}
	s.mu.Lock()
	s.vars[name] = v
	s.mu.Unlock()
}

func (s *session) lookup(path string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	parts := strings.Split(path, ".")
	var cur any = s.vars
	for _, p := range parts {
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[p]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(c) {
				return nil, false
			}
			cur = c[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

var tmplRe = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.]+)\s*\}\}`)

func (s *session) render(text string) string {
	return tmplRe.ReplaceAllStringFunc(text, func(m string) string {
		key := tmplRe.FindStringSubmatch(m)[1]
		v, ok := s.lookup(key)
		if !ok {
			return ""
		}
		switch x := v.(type) {
		case string:
			return x
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64)
		case int:
			return strconv.Itoa(x)
		default:
			b, _ := json.Marshal(x)
			return string(b)
		}
	})
}

// renderValue substitutes templates in strings; a string that is exactly one
// template keeps the variable's JSON type.
func (s *session) renderValue(v any) any {
	switch x := v.(type) {
	case string:
		if m := tmplRe.FindStringSubmatch(x); m != nil && m[0] == strings.TrimSpace(x) {
			if val, ok := s.lookup(m[1]); ok {
				return val
			}
			return ""
		}
		return s.render(x)
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			out[k] = s.renderValue(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = s.renderValue(val)
		}
		return out
	}
	return v
}

func (s *session) test(c Cond) bool {
	v, ok := s.lookup(c.Var)
	switch {
	case c.Contains != "":
		return ok && strings.Contains(fmt.Sprint(v), s.render(c.Contains))
	case c.Truthy:
		if !ok || v == nil {
			return false
		}
		switch x := v.(type) {
		case bool:
			return x
		case string:
			return x != ""
		case float64:
			return x != 0
		case []any:
			return len(x) > 0
		}
		return true
	default:
		if !ok {
			return false
		}
		return fmt.Sprint(v) == fmt.Sprint(c.Eq)
	}
}

// summarize renders a work_status result as a factual reply.
func (s *session) summarize(varName string) string {
	v, ok := s.lookup(varName + ".work")
	items, _ := v.([]any)
	if !ok || len(items) == 0 {
		return "Nothing is in flight from this conversation's point of view."
	}
	var b strings.Builder
	b.WriteString("Here's what the work ledger shows from here:\n")
	for _, it := range items {
		m, _ := it.(map[string]any)
		line := fmt.Sprintf("- %v: %v — %v", m["project"], m["title"], strings.ReplaceAll(fmt.Sprint(m["state"]), "_", " "))
		if d := fmt.Sprint(m["detail"]); d != "" && d != "<nil>" {
			line += " (" + d + ")"
		}
		if o := fmt.Sprint(m["owner"]); o != "" && o != "<nil>" {
			line += ", owner " + o
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimSpace(b.String())
}

func chunks(s string, n int) []string {
	var out []string
	r := []rune(s)
	for i := 0; i < len(r); i += n {
		j := i + n
		if j > len(r) {
			j = len(r)
		}
		out = append(out, string(r[i:j]))
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
