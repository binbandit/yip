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
