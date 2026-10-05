package runner

import (
	"testing"

	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/protocol"
)

func TestEngineerDraftDeniesLocalToolsAndApprovals(t *testing.T) {
	ar := &activeRun{m: protocol.ExecutionManifest{RunID: "draft", EngineerDraft: true, Mode: protocol.ModeConversation}}
	ar.admit.Store(true)
	r := &Runner{tokens: map[string]string{"token": "draft"}, runs: map[string]*activeRun{"draft": ar}}
	for _, tool := range []string{bridge.SkillRead, bridge.PermissionPrompt, bridge.ArtifactPublish, "room_post"} {
		res := r.handleBridge(bridge.LocalRequest{Token: "token", Tool: tool})
		if res.OK || res.Error == nil || res.Error.Code != "forbidden" {
			t.Errorf("draft admitted %s: %+v", tool, res)
		}
	}
	for _, kind := range []string{"read", "exec", "edit", "network", "mcp"} {
		res := r.requestApproval(ar, "permission", protocol.ApprovalAction{Kind: kind}, nil)
		if res.Decision != "deny" {
			t.Errorf("draft admitted approval %s", kind)
		}
	}
}

func TestEngineerDraftCapabilityIsSpecificToAvailableProvider(t *testing.T) {
	r := &Runner{opts: Options{ExecutionProfile: "native"}}
	caps := protocol.RunnerCapabilities{Providers: []protocol.ProviderInstallation{{Provider: "test", ProfileID: "account", AuthState: protocol.AuthReady, Capabilities: protocol.ProviderCapabilities{ReadOnly: true}}}}
	r.caps.Store(caps)
	if r.draftProviderAvailable("test", "account") || r.profiles(caps)[0].EngineerDrafts {
		t.Fatal("read-only alone admitted drafting")
	}
	caps.Providers[0].Capabilities.EngineerDrafts = true
	r.caps.Store(caps)
	if !r.draftProviderAvailable("test", "account") || !r.profiles(caps)[0].EngineerDrafts {
		t.Fatal("supported provider not advertised")
	}
	if r.draftProviderAvailable("test", "different") {
		t.Fatal("wrong account admitted")
	}
	caps.Providers[0].AuthState = protocol.AuthNeedsSignIn
	r.caps.Store(caps)
	if r.draftProviderAvailable("test", "account") || r.profiles(caps)[0].EngineerDrafts {
		t.Fatal("unconfigured provider advertised")
	}
}
