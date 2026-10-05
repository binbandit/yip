// Package skilltest exercises the real bridge using provider test boundaries.
// It is imported only by tests and never linked into yip.
package skilltest

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/skills"
	"github.com/binbandit/yip/protocol"
)

// Re-exec any importing test binary as the real stdio bridge.
func init() {
	if mode := os.Getenv("YIP_SKILL_TEST_BRIDGE"); mode != "" {
		if err := bridge.RunFromEnv(mode, "test"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
}

func Handle(req bridge.LocalRequest) bridge.LocalResponse {
	var args bridge.SkillReadArgs
	if req.Tool != bridge.SkillRead || json.Unmarshal(req.Args, &args) != nil {
		return bridge.LocalResponse{Error: &protocol.APIError{Code: "invalid", Message: "invalid skill request"}}
	}
	doc, err := skills.Read(args.Name)
	if err != nil {
		return bridge.LocalResponse{Error: &protocol.APIError{Code: "invalid", Message: err.Error()}}
	}
	raw, _ := json.Marshal(doc)
	return bridge.LocalResponse{OK: true, Result: raw}
}

func New(t *testing.T, mode string) providers.MCPServer {
	t.Helper()
	// Keep Unix socket paths below the macOS limit.
	dir, err := os.MkdirTemp("/tmp", "yip-skills-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "bridge.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go bridge.Serve(ln, func(req bridge.LocalRequest) bridge.LocalResponse {
		if req.Token != "skill-test-token" {
			return bridge.LocalResponse{Error: &protocol.APIError{Code: "forbidden"}}
		}
		return Handle(req)
	})
	return providers.MCPServer{Name: "yip", Command: os.Args[0], Env: map[string]string{
		"YIP_SKILL_TEST_BRIDGE": mode, bridge.EnvSocket: socket, bridge.EnvToken: "skill-test-token",
	}}
}

// Verify discovers and reads the actual pinned bytes through serialized MCP config.
func Verify(server providers.MCPServer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, server.Command, server.Args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	for k, v := range server.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() { _ = in.Close(); _ = cmd.Wait() }()
	scan := bufio.NewScanner(out)
	scan.Buffer(make([]byte, 4096), 1<<20)
	next := 0
	request := func(method string, params any, result any) error {
		next++
		if err := json.NewEncoder(in).Encode(map[string]any{"jsonrpc": "2.0", "id": next, "method": method, "params": params}); err != nil {
			return err
		}
		if !scan.Scan() {
			return fmt.Errorf("bridge exited during %s: %v", method, scan.Err())
		}
		var response struct {
			ID     int
			Result json.RawMessage
			Error  json.RawMessage
		}
		if err := json.Unmarshal(scan.Bytes(), &response); err != nil {
			return err
		}
		if response.ID != next || len(response.Error) > 0 {
			return fmt.Errorf("invalid response: %s", scan.Bytes())
		}
		return json.Unmarshal(response.Result, result)
	}
	var init map[string]any
	if err := request("initialize", map[string]any{"protocolVersion": "2025-06-18"}, &init); err != nil {
		return err
	}
	if err := json.NewEncoder(in).Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		return err
	}
	var listed struct{ Tools []struct{ Name string } }
	if err := request("tools/list", map[string]any{}, &listed); err != nil {
		return err
	}
	found := false
	for _, tool := range listed.Tools {
		if tool.Name == bridge.SkillRead {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("skill_read was not discovered")
	}
	for _, skill := range skills.Catalog() {
		var result struct {
			IsError bool
			Content []struct{ Type, Text string }
		}
		if err := request("tools/call", map[string]any{"name": bridge.SkillRead, "arguments": map[string]string{"name": skill.Name}}, &result); err != nil {
			return err
		}
		if result.IsError || len(result.Content) != 1 {
			return fmt.Errorf("skill_read %s failed: %+v", skill.Name, result)
		}
		var got skills.Document
		if err := json.Unmarshal([]byte(result.Content[0].Text), &got); err != nil {
			return err
		}
		want, _ := skills.Read(skill.Name)
		if got != want {
			return fmt.Errorf("skill %s content changed in transit", skill.Name)
		}
	}
	return nil
}
