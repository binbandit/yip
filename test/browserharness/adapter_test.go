package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

// The subprocess speaks MCP over the same stdio path used by the real bridge.
func TestHarnessMCPProcess(t *testing.T) {
	mode := os.Getenv("YIP_TEST_MCP_MODE")
	if mode == "" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil || req.ID == nil {
			continue
		}
		var result any = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "test", "version": "1"}}
		if req.Method == "tools/call" {
			payload := map[string]any{"message": "rejected by regression fixture"}
			failed := mode == "error"
			if req.Params.Name == "work_run_check" {
				switch mode {
				case "failed":
					payload = map[string]any{"passed": false, "revision": "head"}
				case "missing":
					payload = map[string]any{}
				case "wrong-revision":
					payload = map[string]any{"passed": true, "revision": "other"}
				case "passed":
					payload = map[string]any{"passed": true, "revision": "head"}
				}
			}
			if req.Params.Name == "work_review" {
				want := "unable_to_review"
				if mode == "passed" {
					want = "approved"
				}
				failed = req.Params.Arguments["verdict"] != want
				payload["message"] = fmt.Sprintf("wanted verdict %s, got %v", want, req.Params.Arguments["verdict"])
			}
			body, _ := json.Marshal(payload)
			result = map[string]any{"content": []map[string]string{{"type": "text", "text": string(body)}}, "isError": failed}
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result})
	}
	os.Exit(0)
}

func runScript(t *testing.T, script Script, mode string) providers.Result {
	t.Helper()
	raw, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	a := New(0)
	a.Script = func(context.Context, string) (json.RawMessage, error) { return raw, nil }
	sess, err := a.Start(context.Background(), providers.StartSpec{Workdir: t.TempDir(), Env: append(os.Environ(), "YIP_TEST_MCP_MODE="+mode), MCP: providers.MCPServer{Command: os.Args[0], Args: []string{"-test.run=^TestHarnessMCPProcess$"}}})
	if err != nil {
		t.Fatal(err)
	}
	for range sess.Events() {
	}
	return sess.Wait()
}

func TestSavedCriticalToolErrorsFailTheSession(t *testing.T) {
	for _, name := range []string{"work_request_review", "work_respond_to_review", "work_update"} {
		t.Run(name, func(t *testing.T) {
			result := runScript(t, Script{Steps: []Step{tool(name, map[string]any{}, "saved"), final("must not continue")}}, "error")
			if result.Outcome != protocol.OutcomeFailed || !strings.Contains(result.Error, name) || result.FinalText != "" {
				t.Fatalf("unhandled error was hidden: %+v", result)
			}
		})
	}
}

func TestWorkCreationErrorUsesItsExplicitFallback(t *testing.T) {
	result := runScript(t, Script{Steps: []Step{createWork(map[string]any{}), ifEq("w.ok", true, []Step{final("unexpected")}, []Step{final("creation was refused")})}}, "error")
	if result.Outcome != protocol.OutcomeSucceeded || result.FinalText != "creation was refused" {
		t.Fatalf("fallback: %+v", result)
	}
}

func TestReviewRequiresAPassingCheckOnItsRevision(t *testing.T) {
	for _, mode := range []string{"failed", "missing", "wrong-revision", "passed"} {
		t.Run(mode, func(t *testing.T) {
			m := &manifest.Manifest{Review: &manifest.Review{TargetKind: "patch", Head: "head", Base: "base"}}
			m.Job.Repo = "atlas"
			result := runScript(t, reviewScript(m), mode)
			if result.Outcome != protocol.OutcomeSucceeded {
				t.Fatalf("review guard: %+v", result)
			}
		})
	}
}
