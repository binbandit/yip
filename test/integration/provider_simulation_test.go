package integration

import (
	"strings"
	"testing"
	"time"

	"github.com/binbandit/yip/protocol"
)

func TestConversationWaitsForReadOnlyCapability(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"codex", "claude", "cursor", "opencode", "pi"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, envOptions{})
			var engineer protocol.Engineer
			e.c.must("GET", "/v1/engineers/"+e.engineerID("mira"), nil, &struct {
				Engineer *protocol.Engineer `json:"engineer"`
			}{&engineer})
			preference := protocol.ProviderPreference{Provider: provider}
			e.c.must("PATCH", "/v1/engineers/"+engineer.ID, protocol.UpdateEngineerRequest{
				Version: engineer.Version, Provider: &preference,
			}, &engineer)
			installation := protocol.ProviderInstallation{Provider: provider, AuthState: protocol.AuthReady, ProfileID: provider + ":simulation"}
			node := e.connectNodeWith("edit-only installation", installation)
			e.post("Security", "@Mira please inspect the session contract", []string{"mira"}, nil)
			e.waitFor("conversation waits instead of dispatching to an unsupported provider", 5*time.Second, func() bool {
				if _, offered := node.find(func(f protocol.Frame) bool { return f.Type == protocol.CmdOfferRun }); offered {
					t.Fatal("conversation offered despite unavailable read-only capability")
				}
				for _, job := range e.jobsWithReplies() {
					if job.Kind == protocol.JobKindReply && job.State == protocol.JobWaiting && strings.Contains(job.StateDetail, "conversation") {
						return true
					}
				}
				e.hub.Tick(e.ctx)
				return false
			})

			installation.Capabilities.ReadOnly = true
			node.send(e, protocol.EvCapabilities, "", 0, protocol.RunnerCapabilities{
				Slots: 2, Profiles: []protocol.ExecutionProfile{{Name: "native", Available: true}},
				Providers: []protocol.ProviderInstallation{installation},
			})
			node.waitFrame(e, "conversation resumes when its capability is available", func(f protocol.Frame) bool {
				return f.Type == protocol.CmdOfferRun
			})
		})
	}
}
