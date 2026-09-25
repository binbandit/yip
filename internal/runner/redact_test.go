package runner

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	in := "export GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789\n" +
		"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.signature123\n" +
		"ANTHROPIC_API_KEY=sk-ant-api03-abcdefghijklmnopqrstu\n" +
		"password: hunter2hunter2\n" +
		"-----BEGIN EC PRIVATE KEY-----\nMHcCAQEE\n-----END EC PRIVATE KEY-----\n" +
		"ok: tests passed"
	out := Redact(in)
	for _, leak := range []string{"ghp_abc", "eyJhbGci", "sk-ant-api03", "hunter2", "MHcCAQEE"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %q in:\n%s", leak, out)
		}
	}
	if !strings.Contains(out, "ok: tests passed") {
		t.Errorf("redaction removed ordinary output")
	}
}
