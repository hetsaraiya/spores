package agent

import (
	"testing"
)

func TestIssueURL(t *testing.T) {
	repo, number, ok := issueURL("please fix https://github.com/foo/bar/issues/42 thanks")
	if !ok || repo != "foo/bar" || number != 42 {
		t.Errorf("issueURL = (%q, %d, %t), want (foo/bar, 42, true)", repo, number, ok)
	}
	if _, _, ok := issueURL("no link here"); ok {
		t.Error("issueURL matched a message without an issue link")
	}
	if _, _, ok := issueURL("https://github.com/foo/bar/pull/7"); ok {
		t.Error("issueURL matched a pull request link")
	}
}

func TestExtractJSON(t *testing.T) {
	want := `{"repo":"foo/bar"}`
	cases := []string{
		want,
		"```json\n" + want + "\n```",
		"Here is the JSON you asked for:\n" + want + "\nLet me know!",
		"  " + want + "  ",
	}
	for _, c := range cases {
		if got := ExtractJSON(c); got != want {
			t.Errorf("ExtractJSON(%q) = %q, want %q", c, got, want)
		}
	}
	if got := ExtractJSON("not json at all"); got != "not json at all" {
		t.Errorf("extractJSON passthrough = %q", got)
	}
}
