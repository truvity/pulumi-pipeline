package gitutils

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// GitRoot returns the root directory of the git repository (or worktree).
// Runs: git rev-parse --show-toplevel
// Unsets GIT_DIR to avoid incorrect resolution in worktrees when lefthook
// sets GIT_DIR to the main repo's .git directory.
// Returns ErrNotInRepo when the working directory is not inside a git repository.
func GitRoot(ctx context.Context, logger *slog.Logger) (string, error) {
	var stdoutStream strings.Builder
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Stdout = &stdoutStream
	cmd.Stderr = io.Discard

	// Unset GIT_DIR to ensure git resolves the toplevel from CWD, not from
	// a potentially incorrect GIT_DIR set by lefthook in worktrees.
	cmd.Env = filterEnv(os.Environ(), "GIT_DIR")

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 128 {
			return "", fmt.Errorf("git rev-parse: %w", ErrNotInRepo)
		}
		return "", NewGitError("rev-parse", err)
	}

	root := strings.TrimSpace(stdoutStream.String())
	logger.DebugContext(ctx, "git root", slog.String("root", root))
	return root, nil
}

// filterEnv returns a copy of env with the named variable removed.
func filterEnv(env []string, name string) []string {
	prefix := name + "="
	filtered := make([]string, 0, len(env))

	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			filtered = append(filtered, e)
		}
	}

	return filtered
}
