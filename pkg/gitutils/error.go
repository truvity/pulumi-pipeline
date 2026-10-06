package gitutils

import (
	"errors"
	"fmt"
)

// ErrNotInRepo is returned when a git command fails because the working
// directory is not inside a git repository (git exit code 128).
var (
	ErrNotInRepo = errors.New("not in a git repository")
)

// GitError represents an error from git command execution
type (
	GitError struct {
		Command string
		Err     error
	}
)

func (e *GitError) Error() string {
	return fmt.Sprintf("git %s failed: %v", e.Command, e.Err)
}

func (e *GitError) Unwrap() error {
	return e.Err
}

// NewGitError creates a new GitError
func NewGitError(command string, err error) *GitError {
	return &GitError{
		Command: command,
		Err:     err,
	}
}
