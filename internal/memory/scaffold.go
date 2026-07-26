package memory

import (
	"fmt"
	"os"
)

// Scaffold guidance lives in HTML comments, which meaningful() strips. An
// unfilled file therefore costs nothing in the prompt while still showing the
// portal and the curator what belongs in it.
var scaffolds = map[string]string{
	"USER.md": `<!--
Who you are and how you like to work: role, communication style, standing
instructions. Only the configured owner's stated preferences belong here.

Example:
- Prefers short Slack replies, no bullet-point summaries of single tasks.
- Reviews PRs before merge; never merge without asking.
-->
`,
	"STACK.md": `<!--
Technologies, services, and conventions you generally prefer, across projects.
Not repository specifics — those are rediscovered from the repository.

Example:
- Go for backend services; avoid adding dependencies for small helpers.
- Deploys are containers on a self-hosted host, built by GHCR CI.
-->
`,
}

// scaffold writes the guidance templates for root files that do not exist yet.
func (s *Store) scaffold() error {
	for name, template := range scaffolds {
		path := s.path(name)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := writeAtomic(path, template); err != nil {
			return fmt.Errorf("scaffold %s: %w", name, err)
		}
	}
	return nil
}
