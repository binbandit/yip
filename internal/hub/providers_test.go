package hub

import (
	"testing"

	"github.com/binbandit/yip/protocol"
)

func TestHarnessPreferencesAndSummary(t *testing.T) {
	h := &Hub{}
	for _, id := range []string{"opencode", "pi"} {
		if err := validateProvider(protocol.ProviderPreference{Provider: id}); err != nil {
			t.Fatalf("%s cannot be assigned to an engineer: %v", id, err)
		}
		if err := validateProvider(protocol.ProviderPreference{Provider: "codex", Alternatives: []string{id}}); err != nil {
			t.Fatalf("%s cannot be used as an alternative: %v", id, err)
		}
		found := false
		for _, p := range h.providerSummary([]protocol.Node{{
			ID: "runner", Status: protocol.NodeOnline,
			Providers: []protocol.ProviderInstallation{{Provider: id, AuthState: protocol.AuthReady, Billing: protocol.BillingSubscription}},
		}}) {
			if p.Provider != id {
				continue
			}
			found = true
			if p.Label == id || len(p.ReadyNodes) != 1 || p.ReadyNodes[0] != "runner" {
				t.Errorf("invalid %s summary: %+v", id, p)
			}
		}
		if !found {
			t.Errorf("%s missing from bootstrap choices", id)
		}
	}
}
