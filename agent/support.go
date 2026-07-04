package agent

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type StepError struct {
	Step int
	Err  error
}

func (e *StepError) Error() string {
	return fmt.Sprintf("Failed at step %d: %v", e.Step, e.Err)
}

func fail(step int, err error) error {
	return &StepError{Step: step, Err: err}
}

func issueURL(msg string) (string, int, bool) {
	re := regexp.MustCompile(`https://github\.com/([^/\s]+/[^/\s]+)/issues/([0-9]+)`)
	match := re.FindStringSubmatch(msg)
	if len(match) == 0 {
		return "", 0, false
	}
	number, err := strconv.Atoi(match[2])
	return match[1], number, err == nil
}

// ExtractJSON peels markdown code fences and surrounding prose off a model
// response, returning the JSON object inside.
func ExtractJSON(text string) string {
	text = strings.TrimSpace(text)
	if start := strings.Index(text, "{"); start >= 0 {
		if end := strings.LastIndex(text, "}"); end > start {
			return text[start : end+1]
		}
	}
	return text
}

func slug(s string) string {
	s = regexp.MustCompile(`[^a-zA-Z0-9]+`).ReplaceAllString(strings.ToLower(s), "-")
	s = strings.Trim(s, "-")
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	if s == "" {
		return "issue"
	}
	return s
}
