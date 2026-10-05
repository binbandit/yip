package cursor

import (
	"encoding/json"
	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/providers/acp"
	"github.com/binbandit/yip/internal/skilltest"
	"testing"
)

func TestBundledSkillsThroughACPConfig(t *testing.T) {
	for _, mode := range []string{"edit", "readonly", "conversation"} {
		t.Run(mode, func(t *testing.T) {
			servers, err := buildMCP(skilltest.New(t, mode))
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(acp.NewSessionParams{MCPServers: servers})
			if err != nil {
				t.Fatal(err)
			}
			var params acp.NewSessionParams
			if err := json.Unmarshal(raw, &params); err != nil {
				t.Fatal(err)
			}
			server := params.MCPServers[0]
			env := map[string]string{}
			for _, kv := range server.Env {
				env[kv.Name] = kv.Value
			}
			if err := skilltest.Verify(providers.MCPServer{Command: server.Command, Args: server.Args, Env: env}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
