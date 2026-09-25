package hub

import "testing"

func TestHumanReason(t *testing.T) {
	cases := map[string]string{
		`work_update failed: {"code":"incomplete","message":"Not complete yet. Missing: a passing run of go test.","recoverable":true}`: "Not complete yet. Missing: a passing run of go test",
		"workspace: git fetch failed": "workspace: git fetch failed",
		"exit status 1: {broken":      "exit status 1",
		"":                            "it stopped before finishing (the details are in the work)",
		"panic: runtime error\ngoroutine 1 [running]:": "it stopped before finishing (the details are in the work)",
	}
	for in, want := range cases {
		if got := humanReason(in); got != want {
			t.Errorf("humanReason(%q) = %q, want %q", in, got, want)
		}
	}
}
