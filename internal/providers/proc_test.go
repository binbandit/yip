package providers

import (
	"os/exec"
	"testing"
	"time"
)

func TestProcessTerminateWaits(t *testing.T) {
	cmd := exec.Command("sh", "-c", "while :; do :; done")
	p, err := StartProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Terminate(time.Second)
	if !p.Terminate(time.Second) {
		t.Fatal("group exit was not confirmed")
	}
	if !p.Exited() {
		t.Fatal("leader was not waited")
	}
	if !p.Terminate(time.Second) {
		t.Fatal("repeated termination should confirm exit")
	}
}
