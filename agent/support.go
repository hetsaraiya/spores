package agent

import "fmt"

func fail(step int, err error) error {
	return fmt.Errorf("coding agent step %d: %w", step, err)
}