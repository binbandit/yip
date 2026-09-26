package runner

import "github.com/binbandit/yip/internal/redact"

// Redact replaces likely secrets with a placeholder. Logs and activity text
// are redacted on the runner before they are uploaded or reported.
func Redact(s string) string { return redact.String(s) }
