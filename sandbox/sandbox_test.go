package sandbox

import (
	"strings"
	"testing"
)

func TestQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "'plain'"},
		{"with space", "'with space'"},
		{"it's", `'it'\''s'`},
		{"", "''"},
	}
	for _, c := range cases {
		if got := Quote(c.in); got != c.want {
			t.Errorf("Quote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeCommandRedactsFileWrites(t *testing.T) {
	cmd := "printf %s 'c2VjcmV0' | base64 -d > '/home/user/.git-credentials'"
	got := sanitizeCommand(cmd)
	if strings.Contains(got, "c2VjcmV0") {
		t.Errorf("sanitized command still contains payload: %q", got)
	}
	if !strings.Contains(got, ".git-credentials") {
		t.Errorf("sanitized command lost the target path: %q", got)
	}
}

func TestSanitizeCommandTruncatesLongCommands(t *testing.T) {
	got := sanitizeCommand(strings.Repeat("x", 600))
	if len(got) > 520 || !strings.HasSuffix(got, "... [truncated]") {
		t.Errorf("long command not truncated: len=%d", len(got))
	}
}
