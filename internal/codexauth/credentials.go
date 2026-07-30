// Package codexauth keeps Codex's refreshable login JSON current for the
// lifetime of the Spores process.
package codexauth

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

const environmentKey = "CODEX_AUTH_JSON"

// Credentials serializes use of one Codex login so refresh-token rotation
// cannot race between delegated jobs.
type Credentials struct {
	mu       sync.Mutex
	authJSON string
}

// NewFromEnvironment seeds the credentials from CODEX_AUTH_JSON.
func NewFromEnvironment() *Credentials {
	return &Credentials{authJSON: strings.TrimSpace(os.Getenv(environmentKey))}
}

// Configured reports whether ChatGPT-managed Codex credentials are available.
func (c *Credentials) Configured() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.authJSON != ""
}

// Use runs fn with the latest credentials, then retains the refreshed JSON it
// returns for the next call. The environment is updated for this process only.
func (c *Credentials) Use(fn func(string) (string, error)) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	refreshed, runErr := fn(c.authJSON)
	refreshed = strings.TrimSpace(refreshed)
	if refreshed == "" || refreshed == c.authJSON {
		return runErr
	}
	if !json.Valid([]byte(refreshed)) {
		if runErr != nil {
			return runErr
		}
		return fmt.Errorf("refreshed Codex credentials are not valid JSON")
	}
	if err := os.Setenv(environmentKey, refreshed); err != nil {
		if runErr != nil {
			return runErr
		}
		return fmt.Errorf("update %s: %w", environmentKey, err)
	}
	c.authJSON = refreshed
	return runErr
}
