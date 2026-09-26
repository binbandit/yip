package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

func apiErr(code, format string, args ...any) *protocol.APIError {
	return &protocol.APIError{Code: code, Message: fmt.Sprintf(format, args...), Recoverable: code != "forbidden"}
}

// handleBridge serves tool calls from the provider's bridge process. The
// per-run token binds each call to its run; the lease decides admission.
func (r *Runner) handleBridge(req bridge.LocalRequest) bridge.LocalResponse {
	r.mu.Lock()
	runID := r.tokens[req.Token]
	ar := r.runs[runID]
	r.mu.Unlock()
	if runID == "" || ar == nil {
		return bridge.LocalResponse{Error: apiErr("forbidden", "This tool session is not bound to an active run.")}
	}
	if !ar.admit.Load() {
		return bridge.LocalResponse{Error: apiErr("forbidden", "This run is stopping (its lease ended or it was cancelled); tool calls are no longer admitted.")}
	}
	t, ok := bridge.Lookup(req.Tool)
	if !ok || !t.Allowed(ar.m.Mode) {
		return bridge.LocalResponse{Error: apiErr("forbidden", "%s is not available in this run.", req.Tool)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	var (
		res any
		err *protocol.APIError
	)
	switch req.Tool {
	case bridge.WorkRunCheck:
		res, err = r.toolRunCheck(ctx, ar, req.Args)
	case bridge.WorkPublishRev:
		res, err = r.toolPublishRevision(ctx, ar, req.Args)
	case bridge.ArtifactPublish:
		res, err = r.toolArtifactPublish(ctx, ar, req.Args)
	case bridge.PermissionPrompt:
		res, err = r.toolPermissionPrompt(ar, req.Args)
	default:
		raw, e := r.callHub(ctx, ar, req.Tool, req.Args)
		if e != nil {
			return bridge.LocalResponse{Error: e}
		}
		return bridge.LocalResponse{OK: true, Result: raw}
	}
	if err != nil {
		return bridge.LocalResponse{Error: err}
	}
	b, _ := json.Marshal(res)
	return bridge.LocalResponse{OK: true, Result: b}
}

// callHub forwards a tool call to the hub under the run's lease and waits
// for its result.
func (r *Runner) callHub(ctx context.Context, ar *activeRun, tool string, args json.RawMessage) (json.RawMessage, *protocol.APIError) {
	callID := domain.NewID()
	payload, _ := json.Marshal(protocol.ToolCall{CallID: callID, Tool: tool, Args: args})
	p := &pendingCall{frame: protocol.Frame{Type: protocol.EvToolCall, ID: callID, RunID: ar.m.RunID, LeaseEpoch: ar.epoch, Payload: payload},
		ch: make(chan protocol.ToolResult, 1)}
	r.mu.Lock()
	r.tools[callID] = p
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.tools, callID)
		r.mu.Unlock()
	}()
	// A failed send is not fatal: the call stays pending and is re-sent when
	// the connection returns, while the lease still admits it.
	_ = r.send(p.frame)
	deadline := time.After(5 * time.Minute)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case res := <-p.ch:
			if !res.OK {
				if res.Error == nil {
					res.Error = apiErr("internal", "The hub rejected the call.")
				}
				return nil, res.Error
			}
			return res.Result, nil
		case <-tick.C:
			if !ar.admit.Load() {
				return nil, apiErr("forbidden", "This run is stopping (its lease ended or it was cancelled); the call's outcome is unknown.")
			}
		case <-ctx.Done():
			return nil, apiErr("unavailable", "The hub did not answer in time.")
		case <-deadline:
			return nil, apiErr("unavailable", "The hub did not answer in time.")
		}
	}
}

// checkEnv is the environment for check commands. Workspace code under test
// runs with a scratch HOME and TMPDIR, no SSH agent, no user or system git
// configuration and no credential helper, so it can't reach the machine
// owner's keys or tokens. Toolchain caches stay shared so builds stay fast.
func (r *Runner) checkEnv(runID string) ([]string, error) {
	scratch := r.scratchDir(runID)
	home, tmp := filepath.Join(scratch, "home"), filepath.Join(scratch, "tmp")
	for _, d := range []string{home, tmp} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	realHome, _ := os.UserHomeDir()
	over := []string{"CI=1", "NO_COLOR=1", "HOME=" + home, "TMPDIR=" + tmp,
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=true", "SSH_ASKPASS=true", "GCM_INTERACTIVE=never",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0="}
	keep := func(key, fallback string) {
		if v := os.Getenv(key); v != "" {
			over = append(over, key+"="+v)
		} else if fallback != "" {
			if _, err := os.Stat(fallback); err == nil {
				over = append(over, key+"="+fallback)
			}
		}
	}
	goPath := os.Getenv("GOPATH")
	if goPath == "" && realHome != "" {
		goPath = filepath.Join(realHome, "go")
	}
	if goPath != "" {
		over = append(over, "GOPATH="+goPath)
		if os.Getenv("GOMODCACHE") == "" {
			over = append(over, "GOMODCACHE="+filepath.Join(goPath, "pkg", "mod"))
		}
	}
	keep("GOMODCACHE", "")
	if uc, err := os.UserCacheDir(); err == nil {
		keep("GOCACHE", filepath.Join(uc, "go-build"))
	}
	if uc, err := os.UserConfigDir(); err == nil {
		keep("GOENV", filepath.Join(uc, "go", "env"))
	}
	if realHome != "" {
		keep("npm_config_cache", filepath.Join(realHome, ".npm"))
		keep("CARGO_HOME", filepath.Join(realHome, ".cargo"))
		keep("RUSTUP_HOME", filepath.Join(realHome, ".rustup"))
	}
	keep("XDG_CACHE_HOME", "")
	var env []string
	for _, kv := range providers.BaseEnv(nil, over...) {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "SSH_AUTH_SOCK", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_RUNTIME_DIR":
			continue // agent sockets and per-user config (gh, git credentials)
		}
		env = append(env, kv)
	}
	return env, nil
}

// scratchDir holds a run's check HOME and TMPDIR; it is removed when the
// run ends.
func (r *Runner) scratchDir(runID string) string { return filepath.Join(r.paths.Dir, "scratch", runID) }

func decodeArgs[T any](raw json.RawMessage) (T, *protocol.APIError) {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, apiErr("invalid", "Invalid arguments: %s", err.Error())
	}
	return v, nil
}

// toolRunCheck executes a check command in the workspace and records the
// command, exit status, exact revision, and log as evidence.
func (r *Runner) toolRunCheck(ctx context.Context, ar *activeRun, raw json.RawMessage) (any, *protocol.APIError) {
	a, e := decodeArgs[bridge.WorkRunCheckArgs](raw)
	if e != nil {
		return nil, e
	}
	if strings.TrimSpace(a.Command) == "" {
		return nil, apiErr("invalid", "Give the command to run.")
	}
	timeout := 15 * time.Minute
	if a.TimeoutSeconds > 0 {
		timeout = time.Duration(a.TimeoutSeconds) * time.Second
	}
	head, dirty, _, _ := ar.ws.Head(ctx)
	revision := head
	if dirty {
		revision = head + "+uncommitted"
	}
	// The command goes through the same permission policy as the provider's
	// own shell: routine checks are allowed by the run's grants; a push,
	// publication, network access, or anything yip can't inspect needs the
	// owner's exact-action approval.
	d := r.requestApproval(ar, "check:"+domain.NewID(), protocol.ApprovalAction{Kind: "exec", Command: a.Command,
		Summary: "Run check: " + truncate(a.Command, 200), Target: revision}, raw)
	if d.Decision != "allow" {
		return nil, apiErr("forbidden", "Not permitted to run this check: %s", firstNonEmpty(d.Reason, "the request was declined"))
	}
	env, err := r.checkEnv(ar.m.RunID)
	if err != nil {
		return nil, apiErr("internal", "Could not prepare the check environment: %s", err.Error())
	}
	r.emit(ar.m.RunID, ar.epoch, protocol.RunEvent{Kind: protocol.RunEvToolStarted, Tool: bridge.WorkRunCheck, Text: "Running " + truncate(a.Command, 120)})
	cmd := exec.Command("/bin/sh", "-c", a.Command)
	cmd.Dir = ar.ws.Dir
	cmd.Env = env
	var out bytes.Buffer
	lw := &limitWriter{w: &out, n: 4 << 20}
	cmd.Stdout, cmd.Stderr = lw, lw
	start := time.Now()
	proc, err := providers.StartProcess(cmd)
	if err != nil {
		return nil, apiErr("internal", "Could not start the check: %s", err.Error())
	}
	// The check runs in its own process group. It is stopped as a group on
	// timeout, when the call's context ends, or as soon as the run stops
	// admitting work (cancelled, stopping, or its lease was lost).
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	timedOut, stopped := false, ""
wait:
	for {
		select {
		case <-proc.Done():
			break wait
		case <-timer.C:
			timedOut = true
			go proc.Terminate(5 * time.Second)
		case <-ctx.Done():
			stopped = "the call ended"
			go proc.Terminate(5 * time.Second)
		case <-tick.C:
			if !ar.admit.Load() && stopped == "" {
				stopped = "the run stopped"
				go proc.Terminate(5 * time.Second)
			}
		}
	}
	runErr := proc.Err()
	dur := time.Since(start)
	exit := providers.ExitCode(runErr)
	if timedOut {
		exit = 124
		out.WriteString("\n[yip] check timed out after " + timeout.String() + "; its process group was stopped\n")
	} else if stopped != "" {
		out.WriteString("\n[yip] check stopped because " + stopped + "\n")
	}
	name := firstNonEmpty(a.Name, a.Command)
	logText := Redact(out.String())
	var logID string
	if logFile, err := writeTemp([]byte(logText)); err == nil {
		defer os.Remove(logFile)
		if art, err := r.uploadAndRecord(ctx, ar, logFile, "log", "check: "+truncate(name, 80)+".log", "text/plain; charset=utf-8", revision); err == nil {
			logID = art.ID
		}
	}
	tail := tailLines(logText, 40)
	rec := protocol.CheckRecord{Name: name, Command: a.Command, ExitCode: exit, Revision: revision, DurationMs: dur.Milliseconds(),
		LogArtifactID: logID, Summary: tailLines(logText, 5)}
	recRaw, _ := json.Marshal(rec)
	if _, e := r.callHub(ctx, ar, bridge.RecordCheck, recRaw); e != nil {
		return nil, e
	}
	status := "passed"
	if exit != 0 {
		status = fmt.Sprintf("failed (exit %d)", exit)
	}
	r.emit(ar.m.RunID, ar.epoch, protocol.RunEvent{Kind: protocol.RunEvToolFinished, Tool: bridge.WorkRunCheck, Text: name + ": " + status})
	res := map[string]any{"exitCode": exit, "passed": exit == 0, "revision": revision, "durationMs": dur.Milliseconds(), "outputTail": tail}
	if dirty {
		res["note"] = "This ran on uncommitted changes, so it does not count as evidence for a published revision. Publish with work_publish_revision, then run checks again."
	}
	return res, nil
}

// toolPublishRevision commits outstanding changes and publishes the result
// revision with its diff and a portable bundle.
func (r *Runner) toolPublishRevision(ctx context.Context, ar *activeRun, raw json.RawMessage) (any, *protocol.APIError) {
	a, e := decodeArgs[bridge.WorkPublishRevArgs](raw)
	if e != nil {
		return nil, e
	}
	if ar.ws.Scratch || ar.ws.ReadOnly || ar.m.Repo == nil {
		return nil, apiErr("forbidden", "This run has no editable repository workspace.")
	}
	head, dirty, _, err := ar.ws.Head(ctx)
	if err != nil {
		return nil, apiErr("internal", "Reading the workspace failed: %s", err.Error())
	}
	if dirty {
		summary := strings.TrimSpace(a.Summary)
		if summary == "" {
			summary = "Update from " + ar.m.EngineerName
		}
		email := domain.Handle(ar.m.EngineerName) + "@engineers.yip.local"
		if head, err = ar.ws.Commit(ctx, ar.m.EngineerName+" (yip)", email, summary); err != nil {
			return nil, apiErr("internal", "Commit failed: %s", err.Error())
		}
	}
	if head == ar.ws.Base {
		return nil, apiErr("invalid", "There are no changes relative to the base %s to publish.", shortRev(ar.ws.Base))
	}
	diff, files, ins, del, err := ar.ws.Diff(ctx, ar.ws.Base, head)
	if err != nil {
		return nil, apiErr("internal", "Computing the diff failed: %s", err.Error())
	}
	diffFile, err := writeTemp([]byte(diff))
	if err != nil {
		return nil, apiErr("internal", "%s", err.Error())
	}
	defer os.Remove(diffFile)
	diffArt, err := r.uploadAndRecord(ctx, ar, diffFile, "diff", fmt.Sprintf("%s..%s.diff", shortRev(ar.ws.Base), shortRev(head)), "text/x-diff; charset=utf-8", head)
	if err != nil {
		return nil, apiErr("unavailable", "Uploading the diff failed: %s", err.Error())
	}
	bundle, err := ar.ws.Bundle(ctx, ar.ws.Base, head)
	if err != nil {
		return nil, apiErr("internal", "Creating the bundle failed: %s", err.Error())
	}
	defer os.Remove(bundle)
	if _, err := r.uploadAndRecord(ctx, ar, bundle, "bundle", shortRev(head)+".bundle", "application/octet-stream", head); err != nil {
		return nil, apiErr("unavailable", "Uploading the revision bundle failed: %s", err.Error())
	}
	rec := protocol.RevisionRecord{RepoID: ar.m.Repo.RepoID, Branch: ar.ws.Branch, Base: ar.ws.Base, Head: head, DiffArtifactID: diffArt.ID,
		DiffHash: diffArt.Hash, FilesChanged: files, Insertions: ins, Deletions: del, Summary: a.Summary}
	recRaw, _ := json.Marshal(rec)
	res, e := r.callHub(ctx, ar, bridge.RecordRevision, recRaw)
	if e != nil {
		return nil, e
	}
	r.emit(ar.m.RunID, ar.epoch, protocol.RunEvent{Kind: protocol.RunEvStatus, Text: fmt.Sprintf("Published %s (%d files)", shortRev(head), files)})
	return json.RawMessage(res), nil
}

// toolArtifactPublish uploads a file from inside the workspace. Paths are
// resolved against the runner-owned workspace root; traversal and symlink
// escapes are rejected.
func (r *Runner) toolArtifactPublish(ctx context.Context, ar *activeRun, raw json.RawMessage) (any, *protocol.APIError) {
	a, e := decodeArgs[bridge.ArtifactPublishArgs](raw)
	if e != nil {
		return nil, e
	}
	path, err := containedPath(ar.ws.Dir, a.Path)
	if err != nil {
		return nil, apiErr("forbidden", "%s", err.Error())
	}
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return nil, apiErr("invalid", "%s is not a regular file in the workspace.", a.Path)
	}
	if st.Size() > 50<<20 {
		return nil, apiErr("invalid", "Artifacts are limited to 50 MB.")
	}
	kind := firstNonEmpty(a.Kind, "document")
	if kind != "document" && kind != "file" {
		// Bundles, checkpoints, diffs and logs are produced by the runner
		// itself; an agent can't publish evidence under those kinds.
		return nil, apiErr("invalid", "kind must be document or file.")
	}
	ct := mime.TypeByExtension(filepath.Ext(path))
	if ct == "" || strings.HasPrefix(ct, "text/html") {
		ct = "text/plain; charset=utf-8"
	}
	if filepath.Ext(path) == ".md" {
		ct = "text/markdown; charset=utf-8"
	}
	head, _, _, _ := ar.ws.Head(ctx)
	art, err := r.uploadAndRecord(ctx, ar, path, kind, firstNonEmpty(a.Name, filepath.Base(path)), ct, head)
	if err != nil {
		return nil, apiErr("unavailable", "Publishing failed: %s", err.Error())
	}
	return map[string]any{"artifactId": art.ID, "hash": art.Hash, "size": art.Size, "name": art.Name}, nil
}

func containedPath(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", errors.New("give a path relative to the workspace root")
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("the path must stay inside the workspace")
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, clean))
	if err != nil {
		return "", errors.New("no such file in the workspace")
	}
	if full != rootReal && !strings.HasPrefix(full, rootReal+string(os.PathSeparator)) {
		return "", errors.New("the path resolves outside the workspace (symlink escape)")
	}
	return full, nil
}

// toolPermissionPrompt answers Claude Code's permission prompts through the
// hub's policy and, when needed, the owner's exact-action decision. The reply
// is the JSON text Claude Code expects; permission rules are never updated.
func (r *Runner) toolPermissionPrompt(ar *activeRun, raw json.RawMessage) (any, *protocol.APIError) {
	var a bridge.PermissionPromptArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, apiErr("invalid", "bad permission request")
	}
	action := classifyClaudeTool(a.ToolName, a.Input, ar.ws.Dir)
	reqID := "pp-" + firstNonEmpty(a.ToolUseID, domain.NewID())
	d := r.requestApproval(ar, reqID, action, raw)
	var reply map[string]any
	if d.Decision == "allow" {
		input := a.Input
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		reply = map[string]any{"behavior": "allow", "updatedInput": input}
	} else {
		reply = map[string]any{"behavior": "deny", "message": firstNonEmpty(d.Reason, "Not permitted by the project's grants.")}
	}
	b, _ := json.Marshal(reply)
	return string(b), nil
}

// classifyClaudeTool normalizes a Claude Code tool request into an action.
func classifyClaudeTool(tool string, input json.RawMessage, workdir string) protocol.ApprovalAction {
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	str := func(k string) string {
		if v, ok := in[k].(string); ok {
			return v
		}
		return ""
	}
	switch {
	case strings.HasPrefix(tool, "mcp__yip__"):
		return protocol.ApprovalAction{Kind: "mcp", Summary: "yip tool " + strings.TrimPrefix(tool, "mcp__yip__")}
	case tool == "Bash":
		cmd := str("command")
		return protocol.ApprovalAction{Kind: "exec", Command: cmd, Target: workdir, Summary: "Run `" + truncate(cmd, 160) + "`"}
	case tool == "Edit" || tool == "Write" || tool == "MultiEdit" || tool == "NotebookEdit":
		p := firstNonEmpty(str("file_path"), str("notebook_path"))
		kind := "edit"
		if rel, err := filepath.Rel(workdir, p); err != nil || strings.HasPrefix(rel, "..") {
			kind = "other" // outside the worktree
		}
		return protocol.ApprovalAction{Kind: kind, Target: p, Summary: tool + " " + p}
	case tool == "Read" || tool == "Grep" || tool == "Glob" || tool == "LS":
		return protocol.ApprovalAction{Kind: "read", Target: firstNonEmpty(str("file_path"), str("path")), Summary: tool}
	case tool == "WebFetch" || tool == "WebSearch":
		return protocol.ApprovalAction{Kind: "network", Target: firstNonEmpty(str("url"), str("query")), Summary: tool + " " + firstNonEmpty(str("url"), str("query"))}
	}
	return protocol.ApprovalAction{Kind: "other", Summary: "Use tool " + tool}
}

// ---- artifact transfer ----

// uploadAndRecord uploads content (hash and size declared, verified by the
// hub before it becomes visible) and registers it with the run's job.
func (r *Runner) uploadAndRecord(ctx context.Context, ar *activeRun, path, kind, name, contentType, revision string) (protocol.Artifact, error) {
	f, err := os.Open(path)
	if err != nil {
		return protocol.Artifact{}, err
	}
	h := sha256.New()
	size, err := io.Copy(h, f)
	f.Close()
	if err != nil {
		return protocol.Artifact{}, err
	}
	hash := hex.EncodeToString(h.Sum(nil))
	body, err := os.Open(path)
	if err != nil {
		return protocol.Artifact{}, err
	}
	defer body.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, strings.TrimRight(r.id.HubURL, "/")+"/v1/runner/artifacts/"+hash, body)
	if err != nil {
		return protocol.Artifact{}, err
	}
	req.ContentLength = size
	req.Header.Set("X-Yip-Size", fmt.Sprint(size))
	resp, err := r.httpc.Do(req)
	if err != nil {
		return protocol.Artifact{}, err
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return protocol.Artifact{}, fmt.Errorf("upload rejected (HTTP %d): %s", resp.StatusCode, msg)
	}
	meta, _ := json.Marshal(protocol.Artifact{Hash: hash, Name: name, ContentType: contentType, Size: size, Kind: kind, Revision: revision})
	raw, e := r.callHub(ctx, ar, bridge.RecordArtifact, meta)
	if e != nil {
		return protocol.Artifact{}, errors.New(e.Message)
	}
	var art protocol.Artifact
	return art, json.Unmarshal(raw, &art)
}

// fetchBundle downloads a code bundle and verifies its hash.
func (r *Runner) fetchBundle(ctx context.Context, a protocol.Artifact) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(r.id.HubURL, "/")+"/v1/runner/artifacts/"+a.ID, nil)
	if err != nil {
		return "", err
	}
	resp, err := r.httpc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch bundle: HTTP %d", resp.StatusCode)
	}
	f, err := os.CreateTemp("", "yip-fetch-*.bundle")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 1<<30)); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	f.Close()
	if got := hex.EncodeToString(h.Sum(nil)); got != a.Hash {
		os.Remove(f.Name())
		return "", fmt.Errorf("bundle checksum mismatch (%s != %s)", got, a.Hash)
	}
	return f.Name(), nil
}

func writeTemp(b []byte) (string, error) {
	f, err := os.CreateTemp("", "yip-artifact-*")
	if err != nil {
		return "", err
	}
	defer f.Close()
	_, err = f.Write(b)
	return f.Name(), err
}

type limitWriter struct {
	w io.Writer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	if len(p) > l.n {
		_, err := l.w.Write(p[:l.n])
		l.n = 0
		_, _ = l.w.Write([]byte("\n[yip] output truncated\n"))
		return len(p), err
	}
	l.n -= len(p)
	return l.w.Write(p)
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
