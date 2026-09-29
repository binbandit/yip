package hub

import (
	"context"
	"strings"
	"testing"

	"github.com/binbandit/yip/protocol"
)

func TestEnrollmentRunnerCommands(t *testing.T) {
	ctx := context.Background()
	root, err := Open(ctx, Config{DataDir: t.TempDir(), RunnerURL: "https://localhost:7443", Demo: true})
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	secret, _, err := root.IssueBootstrapSecret(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := root.Setup(ctx, protocol.SetupRequest{BootstrapSecret: secret, OrgName: "Root", Name: "Owner", Handle: "owner", Password: "a-long-test-password"})
	if err != nil {
		t.Fatal(err)
	}
	enroll := func(h *Hub, name string) protocol.Enrollment {
		t.Helper()
		en, err := h.CreateEnrollment(ctx, owner.ID, protocol.CreateEnrollmentRequest{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		return en
	}
	rootEnrollment := enroll(root, "Root machine")
	want := "yip runner pair --hub " + root.cfg.RunnerURL + " --fingerprint " + rootEnrollment.HubFingerprint + " --name 'Root machine' --token " + rootEnrollment.Token
	if rootEnrollment.Command != want || rootEnrollment.RunCommand != "yip runner" {
		t.Fatalf("root commands changed: pair=%q run=%q", rootEnrollment.Command, rootEnrollment.RunCommand)
	}
	seen := map[string]bool{rootEnrollment.RunCommand: true}
	for _, name := range []string{"Second", "Third"} {
		child, err := Open(ctx, Config{DataDir: t.TempDir(), RunnerURL: "https://localhost:7443/w/" + name, SessionHub: root, Demo: true})
		if err != nil {
			t.Fatal(err)
		}
		defer child.Close()
		if err := child.SeedWorkspace(ctx, name, owner); err != nil {
			t.Fatal(err)
		}
		en := enroll(child, "Office machine")
		stateArg := " --state ~/.yip/runners/" + child.Org().ID
		if en.RunCommand != "yip runner"+stateArg || !strings.HasPrefix(en.Command, "yip runner pair"+stateArg+" --hub "+child.cfg.RunnerURL+" ") {
			t.Fatalf("inconsistent child commands: pair=%q run=%q", en.Command, en.RunCommand)
		}
		if seen[en.RunCommand] {
			t.Fatalf("workspace reused runner state: %q", en.RunCommand)
		}
		seen[en.RunCommand] = true
		again := enroll(child, "Renamed machine")
		if again.RunCommand != en.RunCommand || !strings.HasPrefix(again.Command, "yip runner pair"+stateArg+" ") {
			t.Fatal("new enrollment changed the workspace's runner state directory")
		}
	}
}
