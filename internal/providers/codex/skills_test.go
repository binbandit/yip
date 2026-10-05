package codex

import (
	"encoding/json"
	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/skilltest"
	"testing"
)

func TestBundledSkillsThroughThreadConfig(t *testing.T) {
	for _, mode := range []string{"edit", "readonly", "conversation"} {
		t.Run(mode, func(t *testing.T) {
			spec := providers.StartSpec{Mode: mode, MCP: skilltest.New(t, mode)}
			raw, err := json.Marshal(threadConfig(spec, nil, nil))
			if err != nil {
				t.Fatal(err)
			}
			var cfg struct {
				Servers map[string]struct {
					providers.MCPServer
					Enabled, Required bool
				} `json:"mcp_servers"`
			}
			if err := json.Unmarshal(raw, &cfg); err != nil {
				t.Fatal(err)
			}
			server := cfg.Servers["yip"]
			if !server.Enabled || !server.Required {
				t.Fatal("bridge disabled")
			}
			if err := skilltest.Verify(server.MCPServer); err != nil {
				t.Fatal(err)
			}
		})
	}
}
