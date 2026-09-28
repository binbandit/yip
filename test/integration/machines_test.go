package integration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/providers/fake"
	"github.com/binbandit/yip/protocol"
)

func (e *env) nodeByName(name string) protocol.Node {
	nodes, _ := e.hub.ListNodes(e.ctx)
	for _, n := range nodes {
		if n.Name == name {
			return n
		}
	}
	e.t.Fatalf("no node %s", name)
	return protocol.Node{}
}

// Slice F: two independent jobs run on two machines at once, each in its own
// worktree; after one machine is revoked, work continues elsewhere only from
// the verified bundle of its last published revision, as an explicit retry.
func TestTwoMachinesAndCheckpointMove(t *testing.T) {
	e := newEnv(t, envOptions{slots: 1, director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case m.Job.Kind == "reply" && strings.Contains(m.Request.Body, "task"):
			title := "Task " + m.Request.Body[len(m.Request.Body)-1:]
			project, repo := "Atlas", "atlas"
			if m.RoomName == "Reverse engineering" {
				project, repo = "Beacon", "beacon-gateway"
			}
			return script(toolStep("work_create", map[string]any{"title": title, "objective": "x", "kind": "code", "project": project, "repo": repo}, ""))
		case m.Job.Title == "Task A" && m.Job.Head == "":
			return script(fake.Step{Write: &fake.WriteFile{Path: "A.md", Content: "from machine one\n"}},
				toolStep("work_publish_revision", map[string]any{"summary": "A"}, ""),
				fake.Step{Fault: "hang"})
		case m.Job.Title == "Task A":
			return script(fake.Step{Shell: "cat A.md && git log --oneline -1", Save: "s"},
				toolStep("room_post", map[string]any{"body": "resumed with: {{s.out}}"}, ""),
				toolStep("work_update", map[string]any{"state": "failed", "summary": "test done"}, ""))
		case m.Job.Title == "Task B":
			return script(fake.Step{Fault: "hang"})
		}
		return nil
	}})
	stopTwo := e.startNamedRunner("runner2", "Second mini", 1)
	defer stopTwo()
	e.post("Security", "@Mira task A", []string{"mira"}, nil)
	e.post("Reverse engineering", "@Pip task B", []string{"pip"}, nil)
	var a, b protocol.JobDetail
	e.waitFor("both running on different machines", 40*time.Second, func() bool {
		ja, ok1 := e.job("Task A")
		jb, ok2 := e.job("Task B")
		if !ok1 || !ok2 {
			return false
		}
		a, b = e.jobDetail(ja.ID), e.jobDetail(jb.ID)
		return len(a.Runs) == 1 && len(b.Runs) == 1 && a.Runs[0].State == protocol.RunRunning && b.Runs[0].State == protocol.RunRunning &&
			a.Job.Revision != nil && a.Job.Revision.Head != ""
	})
	if a.Runs[0].NodeID == b.Runs[0].NodeID {
		t.Fatalf("both jobs ran on the same machine")
	}
	first := a.Runs[0].NodeID
	head := a.Job.Revision.Head
	// Free the other machine and revoke the one holding Task A.
	e.c.must("POST", "/v1/jobs/"+b.Job.ID+"/cancel", protocol.CancelJobRequest{Reason: "free the slot"}, nil)
	e.c.must("DELETE", "/v1/nodes/"+first+"/credential", nil, nil)
	e.waitFor("attempt marked unknown", 20*time.Second, func() bool {
		return e.jobDetail(a.Job.ID).Runs[0].State == protocol.RunUnknown
	})
	if n := len(e.jobDetail(a.Job.ID).Runs); n != 1 {
		t.Fatalf("no automatic second writer after revocation, got %d runs", n)
	}
	e.c.must("POST", "/v1/jobs/"+a.Job.ID+"/retry", protocol.RetryJobRequest{FromCheckpoint: true, Reason: "machine retired"}, nil)
	msg := e.waitMessage("Security", "resumed with:")
	if !strings.Contains(msg.Body, "from machine one") || !strings.Contains(msg.Body, head[:7]) {
		t.Fatalf("the new machine should restore the published revision from its bundle: %s", msg.Body)
	}
	d := e.jobDetail(a.Job.ID)
	if d.Runs[1].NodeID == first {
		t.Fatalf("the retry ran on the revoked machine")
	}
}

// Machines → Storage names each workspace for what it is. A reply's scratch
// space joins through its run to the conversation reply (it is not a review
// snapshot); a working checkout joins to its job. Sizes say whether they
// were measured, so a tiny workspace isn't shown as a measured zero.
func TestMachineWorkspaceKindsAndSizes(t *testing.T) {
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Engineering", "map the gateway"):
			return script(toolStep("work_create", map[string]any{"title": "Map the gateway", "objective": "Map it", "kind": "investigation", "project": "Beacon", "repo": "beacon-gateway"}, ""))
		case m.Job.Title == "Map the gateway":
			return script(fake.Step{Write: &fake.WriteFile{Path: "notes.txt", Content: "draft notes"}},
				toolStep("work_update", map[string]any{"state": "completed", "summary": "Mapped."}, ""))
		}
		return nil
	}})
	// Pip can read both of Engineering's projects, so a reply there works in
	// a scratch space rather than reading one repository.
	e.c.must("PUT", "/v1/projects/"+e.project("Atlas").ID+"/grants/"+e.engineerID("pip"), protocol.PutGrantRequest{Access: "read"}, nil)
	e.post("Engineering", "@Pip can you map the gateway?", []string{"pip"}, nil)
	done := e.waitJob("Map the gateway", protocol.JobCompleted)
	node := e.nodeByName("Test mini")
	var scratch, checkout protocol.NodeWorkspace
	e.waitFor("both workspaces reported", 20*time.Second, func() bool {
		e.c.must("POST", "/v1/nodes/"+node.ID+"/probe", struct{}{}, nil)
		time.Sleep(200 * time.Millisecond)
		scratch, checkout = protocol.NodeWorkspace{}, protocol.NodeWorkspace{}
		for _, w := range e.nodeByName("Test mini").Workspaces {
			switch {
			case w.Kind == "scratch" && strings.Contains(w.JobTitle, "map the gateway"):
				scratch = w
			case w.Kind == "job" && w.JobID == done.ID:
				checkout = w
			}
		}
		return scratch.Name != "" && checkout.Name != ""
	})
	if scratch.JobKind != protocol.JobKindReply || !strings.HasPrefix(scratch.Name, "scratch-") {
		t.Fatalf("a reply's scratch space should join to the reply: %+v", scratch)
	}
	if checkout.JobKind != protocol.JobKindInvestigation || checkout.JobTitle != "Map the gateway" {
		t.Fatalf("a working checkout should join to its job: %+v", checkout)
	}
	for _, w := range []protocol.NodeWorkspace{scratch, checkout} {
		if !w.SizeKnown || w.SizeApprox || w.SizeMB != w.SizeBytes>>20 {
			t.Fatalf("%s should report a measured size: %+v", w.Name, w)
		}
	}
	if checkout.SizeBytes == 0 {
		t.Fatalf("a checkout with files can't measure zero bytes: %+v", checkout)
	}
}
