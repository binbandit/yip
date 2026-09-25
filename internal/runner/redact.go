package runner

import "regexp"

// secretPatterns match common credential formats. Logs and activity text are
// redacted on the runner before they are uploaded or reported.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}\b`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{30,}\b`),
	regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\byipe_[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`(?i)\b(bearer|token)\s+[A-Za-z0-9._~+/-]{24,}=*`),
	regexp.MustCompile(`(?i)(api[_-]?key|secret|password|passwd|token)(["']?\s*[:=]\s*["']?)[^\s"',;]{8,}`),
}

// Redact replaces likely secrets with a placeholder.
func Redact(s string) string {
	for i, re := range secretPatterns {
		if i == len(secretPatterns)-1 {
			s = re.ReplaceAllString(s, "$1$2[REDACTED]")
			continue
		}
		s = re.ReplaceAllString(s, "[REDACTED]")
	}
	return s
}
