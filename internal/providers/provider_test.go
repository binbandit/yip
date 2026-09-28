package providers

import "testing"

func TestRunSummaryIsOneLine(t *testing.T) {
	for in, want := range map[string]string{
		"go test ./...": "Run `go test ./...`",
		"cat > /tmp/body.md <<'EOF'\n## Cause\nEOF": "Run `cat > /tmp/body.md <<'EOF' …`",
		"  git status  \n":                          "Run `git status`",
	} {
		if got := RunSummary(in); got != want {
			t.Errorf("RunSummary(%q) = %q, want %q", in, got, want)
		}
	}
}
