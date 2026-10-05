package runner

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/protocol"
)

func TestBundledSkillsAvailableInEveryRunMode(t *testing.T) {
	for _, mode := range []string{protocol.ModeConversation, protocol.ModeReadOnly, protocol.ModeEdit} {
		t.Run(mode, func(t *testing.T) {
			if !slices.Contains(bridge.NamesForMode(mode), "skill_read") {
				t.Fatal("the provider's tool catalog omits skill_read")
			}
			ar := &activeRun{m: protocol.ExecutionManifest{Mode: mode}}
			ar.admit.Store(true)
			r := &Runner{tokens: map[string]string{"run-token": "run"}, runs: map[string]*activeRun{"run": ar}}
			for _, name := range []string{"arena", "babysit-pr", "bro", "file-pr"} {
				raw, _ := json.Marshal(map[string]string{"name": name})
				res := r.handleBridge(bridge.LocalRequest{Token: "run-token", Tool: "skill_read", Args: raw})
				if !res.OK || !strings.Contains(string(res.Result), "name: "+name) || !strings.Contains(string(res.Result), "e71d506b83b9197fb6ec37dca1624d9ce51b05e4") {
					t.Fatalf("pinned skill %s is unavailable: %+v", name, res)
				}
			}
			for _, args := range []string{`{}`, `{"name":"../auth.json"}`, `{"name":"/etc/passwd"}`, `{"name":"missing"}`, `invalid`} {
				if res := r.handleBridge(bridge.LocalRequest{Token: "run-token", Tool: "skill_read", Args: json.RawMessage(args)}); res.OK || res.Error == nil {
					t.Fatalf("invalid skill request accepted: %s", args)
				}
			}
			ar.admit.Store(false)
			for _, token := range []string{"run-token", "unknown"} {
				res := r.handleBridge(bridge.LocalRequest{Token: token, Tool: "skill_read", Args: json.RawMessage(`{"name":"bro"}`)})
				if res.OK || res.Error == nil || res.Error.Code != "forbidden" {
					t.Fatalf("skill read bypassed run admission: %+v", res)
				}
			}
		})
	}
}
