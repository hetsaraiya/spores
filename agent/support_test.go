package agent

import (
	"errors"
	"strings"
	"testing"
)

func TestSlug(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Fix the login bug!", "fix-the-login-bug"},
		{"  --weird   input--  ", "weird-input"},
		{"", "issue"},
		{"###", "issue"},
		{strings.Repeat("a", 60), strings.Repeat("a", 48)},
	}
	for _, c := range cases {
		if got := slug(c.in); got != c.want {
			t.Errorf("slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

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
		if got := extractJSON(c); got != want {
			t.Errorf("extractJSON(%q) = %q, want %q", c, got, want)
		}
	}
	if got := extractJSON("not json at all"); got != "not json at all" {
		t.Errorf("extractJSON passthrough = %q", got)
	}
}

func TestStepError(t *testing.T) {
	err := fail(3, errors.New("boom"))
	if err.Error() != "Failed at step 3: boom" {
		t.Errorf("StepError = %q", err.Error())
	}
}
