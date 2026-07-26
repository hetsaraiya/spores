package memory

import (
	"regexp"
	"strings"
)

// The curator prompt tells the model not to store credentials, but a prompt is
// guidance, not a control — and the curator's input includes GitHub API output
// and Codex sandbox logs. These patterns enforce it at the store layer, so the
// portal is covered too.
var secretPatterns = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{"a GitHub token", regexp.MustCompile(`\b(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{16,}`)},
	{"a GitHub personal access token", regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`)},
	{"an OpenAI API key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`)},
	{"a Slack token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`)},
	{"an E2B API key", regexp.MustCompile(`\be2b_[A-Za-z0-9]{16,}`)},
	{"an AWS access key ID", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"a private key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"a bearer token", regexp.MustCompile(`(?i)\bauthorization\s*:\s*bearer\s+\S{12,}`)},
}

// assignment catches the generic `SOMETHING_SECRET = value` shape that the
// prefixed patterns miss.
var assignment = regexp.MustCompile(`(?i)\b[\w.-]*(api[_-]?key|secret|token|password|passwd|credential)s?[\w.-]*\s*[:=]\s*("|')?(\S{12,}?)("|')?(\s|$)`)

// containsSecret returns a short description of the first credential found, or
// "" when the content is clean.
func containsSecret(content string) string {
	for _, candidate := range secretPatterns {
		if candidate.pattern.MatchString(content) {
			return candidate.name
		}
	}
	for _, match := range assignment.FindAllStringSubmatch(content, -1) {
		if secretish(match[3]) {
			return "what looks like a credential assigned to " + strings.ToLower(match[1])
		}
	}
	return ""
}

// secretish filters the assignment matches down to values that look like actual
// credentials. Prose ("token: required for private repos") stays allowed;
// opaque high-entropy strings do not.
func secretish(value string) bool {
	value = strings.Trim(value, `"'`+"`,.;")
	if len(value) < 12 {
		return false
	}
	var digits, upper, lower, symbols int
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r >= 'A' && r <= 'Z':
			upper++
		case r >= 'a' && r <= 'z':
			lower++
		default:
			symbols++
		}
	}
	if digits+upper+lower+symbols != len(value) {
		return false // multi-byte text, not a credential
	}
	// A credential mixes classes; an English word or a hyphenated phrase does not.
	classes := 0
	for _, count := range []int{digits, upper, lower} {
		if count > 0 {
			classes++
		}
	}
	if classes < 2 {
		return false
	}
	return digits > 0 || len(value) >= 32
}
