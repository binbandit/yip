package hub

import "testing"

func TestAppendSentence(t *testing.T) {
	for _, c := range []struct{ detail, add, want string }{
		{"", "Still open: which database", "Still open: which database"},
		{"Waiting for your answer", "Still open: which database", "Waiting for your answer. Still open: which database"},
		{"Blocked on CI.", "Still open: which database", "Blocked on CI. Still open: which database"},
		{"Can it ship?", "Permission needed: push", "Can it ship? Permission needed: push"},
		{"Still open: which database", "Permission needed: push", "Still open: which database. Permission needed: push"},
	} {
		if got := appendSentence(c.detail, c.add); got != c.want {
			t.Errorf("appendSentence(%q, %q) = %q, want %q", c.detail, c.add, got, c.want)
		}
	}
}
