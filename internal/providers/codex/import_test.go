package codex

import (
	"testing"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

func TestImportedMCPOptIn(t *testing.T) {
	for _, mode := range []string{protocol.ModeEdit, protocol.ModeReadOnly, protocol.ModeConversation} {
		for _, inherit := range []bool{false, true} {
			spec := providers.StartSpec{Mode: mode, InheritUserConfig: inherit, MCP: providers.MCPServer{Name: "yip", Command: "bridge"}}
			cfg := threadConfig(spec, []string{"user", "yip"}, nil)
			servers := cfg["mcp_servers"].(map[string]any)
			_, disabled := servers["user"]
			if disabled == (inherit && mode == protocol.ModeEdit) {
				t.Errorf("mode=%s inherit=%v: user MCP policy wrong", mode, inherit)
			}
			bridge := servers["yip"].(map[string]any)
			if bridge["command"] != "bridge" || bridge["default_tools_approval_mode"] != "approve" {
				t.Fatal("bridge override lost")
			}
		}
	}
}
