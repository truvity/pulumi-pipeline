package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattn/go-isatty"
)

// RootDir returns the checkout root the pipeline runs against: Root when the
// caller named one, otherwise the nearest ancestor of the working directory
// that holds go.mod.
func (c Config) RootDir() (string, error) {
	if c.Root != "" {
		abs, err := filepath.Abs(c.Root)
		if err != nil {
			return "", fmt.Errorf("resolve root %q: %w", c.Root, err)
		}

		return abs, nil
	}

	return FindRepoRoot()
}

// FindGitRoot walks up from cwd looking for a .git entry (a directory, or the
// file a linked worktree has).
func FindGitRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	for {
		if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf(".git not found in any parent directory")
		}

		dir = parent
	}
}

// FindRepoRoot walks up from cwd looking for go.mod.
func FindRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found in any parent directory")
		}

		dir = parent
	}
}

// ColorMode returns "always" if stdout is a TTY, "never" otherwise.
func ColorMode() string {
	if isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd()) {
		return "always"
	}

	return "never"
}

// StepNames returns a formatted string of all step names.
func StepNames(steps []Step) string {
	names := make([]string, len(steps))

	for i := range steps {
		names[i] = steps[i].Name
	}

	return "[" + strings.Join(names, ", ") + "]"
}

// hasStep returns true if a step with the given name exists.
func hasStep(steps []Step, name string) bool {
	for i := range steps {
		if steps[i].Name == name {
			return true
		}
	}

	return false
}
