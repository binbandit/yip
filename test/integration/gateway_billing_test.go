package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/providers/codex"
	"github.com/binbandit/yip/protocol"
)

// This subprocess speaks only configuration/account RPC. It never starts a
// thread or contacts a model, and all credentials are synthetic.
func TestGatewayProbeProcess(t *testing.T) {
	if os.Getenv("YIP_GATEWAY_PROBE_PROCESS") != "1" {
		return
	}
	for _, arg := range os.Args {
		if arg == "--version" {
			fmt.Println("codex-cli " + codex.TestedVersion)
			os.Exit(0)
		}
	}
	scan := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scan.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(scan.Bytes(), &req) != nil {
			os.Exit(2)
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{}
		case "initialized":
			continue
		case "config/read":
			result = map[string]any{
				"config": map[string]any{"model_provider": "gateway", "model_providers": map[string]any{
					"gateway": map[string]any{"base_url": "https://gateway.invalid/v1", "env_key": "WORK_GATEWAY_KEY"},
				}}, "layers": []any{},
			}
		case "account/read":
			result = map[string]any{"account": nil, "requiresOpenaiAuth": false}
		case "model/list":
			result = map[string]any{"data": []any{}, "nextCursor": nil}
		default:
			os.Exit(3)
		}
		if enc.Encode(map[string]any{"id": req.ID, "result": result}) != nil {
			os.Exit(4)
		}
	}
	os.Exit(0)
}

func TestGatewayDiscoveryRequiresEngineerAPIPermission(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "gateway-harness")
	body := "#!/bin/sh\nexport YIP_GATEWAY_PROBE_PROCESS=1\nexec '" + strings.ReplaceAll(self, "'", "'\\''") + "' -test.run=^TestGatewayProbeProcess$ -- \"$@\"\n"
	if err := os.WriteFile(exe, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	adapter := codex.New(codex.WithExecutable(exe), codex.WithProbeEnv([]string{
		"HOME=" + t.TempDir(), "WORK_GATEWAY_KEY=synthetic-key",
	}))
	inst := adapter.Probe(context.Background())
	if inst.AuthState != protocol.AuthReady || inst.Billing != protocol.BillingAPI {
		t.Fatalf("gateway probe classification: %+v", inst)
	}
	inst.ProfileID = "codex:gateway"
	e := newEnv(t, envOptions{})
	n := e.connectNodeWith("gateway-box", inst)
	e.post("Engineering", "@Mira hello", []string{"mira"}, nil)
	e.waitFor("API permission is required", 10*time.Second, func() bool {
		e.hub.Tick(e.ctx)
		for _, j := range e.jobsWithReplies() {
			if j.Kind == protocol.JobKindReply && strings.Contains(j.StateDetail, "API key") {
				return true
			}
		}
		return false
	})
	if _, offered := n.find(func(f protocol.Frame) bool { return f.Type == protocol.CmdOfferRun }); offered {
		t.Fatal("configured gateway ran without the engineer's API permission")
	}
	var engineer protocol.Engineer
	e.c.must("GET", "/v1/engineers/"+e.engineerID("mira"), nil, &struct {
		Engineer *protocol.Engineer `json:"engineer"`
	}{&engineer})
	pref := engineer.Provider
	pref.AllowAPIBilling = true
	e.c.must("PATCH", "/v1/engineers/"+engineer.ID, protocol.UpdateEngineerRequest{Version: engineer.Version, Provider: &pref}, &engineer)
	e.post("Engineering", "@Mira hello again", []string{"mira"}, nil)
	n.waitFrame(e, "an offer after explicit API permission", func(f protocol.Frame) bool { return f.Type == protocol.CmdOfferRun })
}
