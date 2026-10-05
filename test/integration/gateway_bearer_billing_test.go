package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	stream "github.com/binbandit/yip/internal/providers/claude"
	"github.com/binbandit/yip/protocol"
)

func TestGatewayBearerProbeRequiresEngineerAPIPermission(t *testing.T) {
	t.Parallel()
	exe := filepath.Join(t.TempDir(), "gateway-harness")
	// Only local discovery commands are implemented; a model launch fails.
	const script = `#!/bin/sh
case "$1" in
--version) printf '%s\n' 'stream-cli 2.1.282';;
--help) printf '%s\n' '--print --output-format --input-format --include-partial-messages --restricted --strict-mcp-config --mcp-config --permission-prompts --permission-prompt-tool --permission-mode --tools --allowedTools --disallowedTools --disable-slash-commands --settings --resume --append-system-prompt --model --verbose';;
auth)
  [ "$ANTHROPIC_AUTH_TOKEN" = synthetic-token ] || exit 2
  printf '%s\n' '{"loggedIn":true,"authMethod":"oauth_token","apiProvider":"firstParty"}';;
*) exit 3;;
esac
`
	if err := os.WriteFile(exe, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	adapter := stream.New(stream.Options{Executable: exe, ProfileID: "gateway-profile", Env: []string{
		"HOME=" + t.TempDir(), "ANTHROPIC_AUTH_TOKEN=synthetic-token",
	}})
	inst := adapter.Probe(t.Context())
	if inst.AuthState != protocol.AuthReady || inst.Billing != protocol.BillingAPI || !inst.Capabilities.StructuredEvents {
		t.Fatalf("gateway probe classification: auth=%s billing=%s capabilities=%+v", inst.AuthState, inst.Billing, inst.Capabilities)
	}
	e := newEnv(t, envOptions{})
	n := e.connectNodeWith("gateway-box", inst)
	var engineer protocol.Engineer
	e.c.must("GET", "/v1/engineers/"+e.engineerID("mira"), nil, &struct {
		Engineer *protocol.Engineer `json:"engineer"`
	}{&engineer})
	pref := protocol.ProviderPreference{Provider: inst.Provider, ProfileID: inst.ProfileID}
	e.c.must("PATCH", "/v1/engineers/"+engineer.ID, protocol.UpdateEngineerRequest{Version: engineer.Version, Provider: &pref}, &engineer)
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
		t.Fatal("gateway bearer ran without the engineer's API permission")
	}
	pref.AllowAPIBilling = true
	e.c.must("PATCH", "/v1/engineers/"+engineer.ID, protocol.UpdateEngineerRequest{Version: engineer.Version, Provider: &pref}, &engineer)
	e.post("Engineering", "@Mira hello again", []string{"mira"}, nil)
	n.waitFrame(e, "an offer after explicit API permission", func(f protocol.Frame) bool { return f.Type == protocol.CmdOfferRun })
}
