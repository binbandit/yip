package claude

import (
	"os"
	"slices"
	"testing"

	"github.com/binbandit/yip/protocol"
)

func TestImportedConfigOptIn(t *testing.T) {
	for _, mode := range []string{protocol.ModeEdit, protocol.ModeReadOnly, protocol.ModeConversation} {
		for _, inherit := range []bool{false, true} {
			spec := testSpec(t, mode)
			spec.InheritUserConfig = inherit
			plan, err := buildLaunch(spec, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(plan.TempDir)
			for _, flag := range []string{"--restricted", "--strict-mcp-config", "--disable-slash-commands"} {
				if slices.Contains(plan.Args, flag) == (inherit && mode == protocol.ModeEdit) {
					t.Errorf("mode=%s inherit=%v: incorrect %s", mode, inherit, flag)
				}
			}
			if !slices.Contains(plan.Args, "--settings="+hookFreeSettings) {
				t.Fatal("hooks no longer disabled")
			}
			if !slices.Contains(plan.Args, "--permission-prompts=host") {
				t.Fatal("permission policy lost")
			}
		}
	}
}
