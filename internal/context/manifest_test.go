package manifest

import (
	"strings"
	"testing"
	"time"
)

func TestFingerprintChangesWithScope(t *testing.T) {
	a := Fingerprint("mira", "room-security", "3", "0", "atlas", "grants-v1", "job:1")
	if a != Fingerprint("mira", "room-security", "3", "0", "atlas", "grants-v1", "job:1") {
		t.Fatal("fingerprint must be deterministic")
	}
	for i, other := range []string{
		Fingerprint("mira", "room-engineering", "3", "0", "atlas", "grants-v1", "job:1"),
		Fingerprint("mira", "room-security", "4", "0", "atlas", "grants-v1", "job:1"),
		Fingerprint("mira", "room-security", "3", "1", "atlas", "grants-v1", "job:1"),
		Fingerprint("mira", "room-security", "3", "0", "atlas", "grants-v2", "job:1"),
	} {
		if other == a {
			t.Errorf("scope change %d did not change the fingerprint", i)
		}
	}
	// Adjacent parts can't collide by concatenation.
	if Fingerprint("ab", "c") == Fingerprint("a", "bc") {
		t.Fatal("fingerprint parts must be delimited")
	}
}

func TestPromptOnlyContainsManifestFacts(t *testing.T) {
	m := &Manifest{Purpose: "message", OwnerName: "Brayden", RoomName: "Engineering", Mode: "conversation", Now: time.Now(),
		Engineer:     Colleague{Name: "Mira", Handle: "mira", Role: "Platform engineer"},
		Conversation: []Message{{ID: "m1", Author: "Brayden", Body: "What did we decide?", At: time.Now()}},
		Job:          Job{Kind: "reply"}}
	p := m.Prompt()
	if !strings.Contains(p, "What did we decide?") || strings.Contains(p, "vault") {
		t.Fatalf("prompt should render exactly the gathered context:\n%s", p)
	}
	ins := m.Instructions()
	if !strings.Contains(ins, "an AI engineer") || !strings.Contains(ins, "human_ask") {
		t.Fatalf("instructions should state AI identity and the question protocol")
	}
}

// The verdict validator requires the full immutable identity. The prompt
// must supply it without relying on a provider to expand an abbreviated ID.
func TestReviewPromptSuppliesExactTarget(t *testing.T) {
	for _, target := range []Review{
		{TargetKind: "patch", Head: strings.Repeat("a", 40)},
		{TargetKind: "pr", Head: strings.Repeat("b", 40)},
		{TargetKind: "artifact", Hash: strings.Repeat("c", 64)},
	} {
		m := &Manifest{Purpose: "review", Review: &target}
		key := target.Head
		if key == "" {
			key = target.Hash
		}
		if !strings.Contains(m.Prompt(), key) {
			t.Fatalf("%s review prompt omitted the exact target", target.TargetKind)
		}
	}
}

func TestReadOnlyInstructionsExplainVerificationWithinScope(t *testing.T) {
	m := &Manifest{Mode: "readonly"}
	instructions := m.Instructions()
	for _, guidance := range []string{"git diff and git show", "existing project checks through work_run_check", "Do not write outside the workspace", "ask the author for that regression", "unable_to_review"} {
		if !strings.Contains(instructions, guidance) {
			t.Errorf("reviewer lacks guidance for verification within its scope: %q", guidance)
		}
	}
	m.Mode = "edit"
	if strings.Contains(m.Instructions(), "Do not silently skip verification or rewrite the review snapshot") {
		t.Fatal("review-only restrictions were applied to an author's edit run")
	}
}

func TestEveryManifestAdvertisesBundledSkills(t *testing.T) {
	for _, mode := range []string{"conversation", "readonly", "edit"} {
		instructions := (&Manifest{Mode: mode}).Instructions()
		for _, text := range []string{"skill_read", "arena", "babysit-pr", "bro", "file-pr", "do not grant permissions"} {
			if !strings.Contains(instructions, text) {
				t.Errorf("%s manifest lacks %q", mode, text)
			}
		}
	}
}
