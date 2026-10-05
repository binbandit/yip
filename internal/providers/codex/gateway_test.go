package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/binbandit/yip/protocol"
)

const gatewayConfig = `{"model_provider":"work","model_providers":{"work":{"base_url":"https://user:private-endpoint@example.invalid/v1?token=private-query","env_key":"WORK_GATEWAY_KEY","env_http_headers":{"X-Workspace":"WORK_WORKSPACE"}},"unused":{"env_key":"UNRELATED_KEY"}}}`

func TestProviderEnvironmentUsesSelectedReferencesOnly(t *testing.T) {
	cfg := configReadResponse{Config: json.RawMessage(gatewayConfig)}
	var reads []string
	lookup := func(key string) (string, bool) {
		reads = append(reads, key)
		return "synthetic-" + key, true
	}
	base := []string{"HOME=/isolated", "WORK_WORKSPACE=explicit"}
	got := providerEnvironment(base, cfg, lookup)
	if !reflect.DeepEqual(reads, []string{"WORK_GATEWAY_KEY"}) || !reflect.DeepEqual(got, append(base, "WORK_GATEWAY_KEY=synthetic-WORK_GATEWAY_KEY")) {
		t.Fatalf("unexpected references or environment: %v %v", reads, got)
	}
	for _, key := range []string{"HOME", "PATH", "LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "CODEX_HOME", "NODE_OPTIONS", "BASH_ENV", "XDG_CONFIG_HOME", "YIP_BRIDGE_TOKEN", "BAD=KEY", ""} {
		if providerEnvironmentKey(key) {
			t.Errorf("unsafe reference accepted: %s", key)
		}
	}
}

func TestGatewayClassification(t *testing.T) {
	cfg := configReadResponse{Config: json.RawMessage(gatewayConfig)}
	for _, test := range []struct {
		name  string
		env   []string
		state string
	}{
		{"missing", nil, protocol.AuthNeedsSignIn},
		{"blank", []string{"WORK_GATEWAY_KEY=  "}, protocol.AuthNeedsSignIn},
		{"configured", []string{"WORK_GATEWAY_KEY=synthetic-key"}, protocol.AuthReady},
		{"last value wins", []string{"WORK_GATEWAY_KEY=synthetic-key", "WORK_GATEWAY_KEY="}, protocol.AuthNeedsSignIn},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, detail, account, billing := classifyConfiguredAccount(getAccountResponse{RequiresOpenaiAuth: true}, cfg, test.env)
			wantBilling := protocol.BillingUnknown
			if test.state == protocol.AuthReady {
				wantBilling = protocol.BillingAPI
			}
			if state != test.state || billing != wantBilling || account != "" {
				t.Fatalf("classification: %s, %s, %s", state, billing, account)
			}
			for _, secret := range []string{"synthetic-key", "private-endpoint", "private-query", "example.invalid"} {
				if strings.Contains(detail, secret) {
					t.Fatalf("detail leaked configuration: %s", detail)
				}
			}
		})
	}
}

func TestGatewayHeaderReferencesRequireAPIPermission(t *testing.T) {
	cfg := configReadResponse{Config: json.RawMessage(`{"model_provider":"work","model_providers":{"work":{"env_http_headers":{"Authorization":"WORK_TOKEN"}}}}`)}
	state, _, _, billing := classifyConfiguredAccount(getAccountResponse{}, cfg, []string{"WORK_TOKEN=synthetic-token"})
	if state != protocol.AuthReady || billing != protocol.BillingAPI {
		t.Fatalf("header credential lost API permission: %s %s", state, billing)
	}
}

func TestBuiltInCloudProviderPreservesAPIBilling(t *testing.T) {
	for _, config := range []string{
		`{"model_provider":"amazon-bedrock","model_providers":{}}`,
		`{"model_provider":"amazon-bedrock","openai_base_url":"https://unselected.example.invalid"}`,
	} {
		cfg := configReadResponse{Config: json.RawMessage(config)}
		state, detail, label, billing := classifyConfiguredAccount(getAccountResponse{
			Account: &account{Type: accountBedrock},
		}, cfg, nil)
		if state != protocol.AuthReady || billing != protocol.BillingAPI || label != "" || !strings.Contains(detail, "Bedrock") {
			t.Fatalf("built-in provider lost API billing: %s %s %s %s", state, detail, label, billing)
		}
	}
}

func TestGatewayProbeAndStartShareEffectiveEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "dedicated"))
	t.Setenv("WORK_GATEWAY_KEY", "synthetic-gateway-key")
	t.Setenv("UNRELATED_KEY", "must-not-forward")
	h := newHarness(t, "probe-gateway", "YIP_CODEX_FAKE_GATEWAY_CONFIG="+gatewayConfig)
	inst := h.adapter.Probe(context.Background())
	if inst.AuthState != protocol.AuthReady || inst.Billing != protocol.BillingAPI || !strings.Contains(inst.AuthDetail, "not been verified") {
		t.Fatalf("probe: %+v", inst)
	}
	raw, err := os.ReadFile(h.logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "WORK_GATEWAY_KEY=synthetic-gateway-key") || strings.Contains(string(raw), "UNRELATED_KEY=") {
		t.Fatal("selected provider environment was not preserved at the process boundary")
	}
	if !strings.Contains(string(raw), "CODEX_HOME="+filepath.Join(home, "dedicated")) {
		t.Fatal("isolated configuration root was lost")
	}

	// Explicit empty or isolated environments must never gain ambient keys.
	h.adapter.probeEnv = []string{"HOME=" + home}
	inst = h.adapter.Probe(context.Background())
	if inst.AuthState != protocol.AuthNeedsSignIn {
		t.Fatalf("explicit environment gained an ambient key: %+v", inst)
	}

	// The normal session uses the same resolved environment as discovery.
	h = newHarness(t, "basic", "YIP_CODEX_FAKE_GATEWAY_CONFIG="+gatewayConfig)
	spec := h.spec()
	spec.Env = nil
	s := h.start(spec)
	collect(t, s, nil)
	if result := s.Wait(); result.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("configured run: %+v", result)
	}
	spec.Env = []string{"HOME=" + home}
	s = h.start(spec)
	collect(t, s, nil)
	if result := s.Wait(); result.Outcome != protocol.OutcomeAuthRequired {
		t.Fatalf("missing environment credential started a turn: %+v", result)
	}
}
