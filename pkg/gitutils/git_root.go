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
	"sync"
)

// GitRoot returns the root directory of the git repository (or worktree).
// Runs: git rev-parse --show-toplevel
// Drops the GIT_* variables a git hook (lefthook's pre-push, say) exports for
// ITS OWN invocation: with GIT_DIR set but GIT_WORK_TREE unset, --show-toplevel
// stops walking up from the working directory and echoes it back, so the root
// of a worktree resolves wrongly. Only GIT_CEILING_DIRECTORIES, which bounds
// the search rather than redirecting it, is kept.
// Returns ErrNotInRepo when the working directory is not inside a git repository.
func GitRoot(ctx context.Context, logger *slog.Logger) (string, error) {
	var stdoutStream strings.Builder
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Stdout = &stdoutStream
	cmd.Stderr = io.Discard

	cmd.Env = hookSafeEnv(os.Environ())

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

// hookSafeEnv returns a copy of env without the GIT_* variables a git hook
// sets, so git discovers its repository from the working directory.
func hookSafeEnv(env []string) []string {
	filtered := make([]string, 0, len(env))

	for _, e := range env {
		if strings.HasPrefix(e, "GIT_") && !strings.HasPrefix(e, "GIT_CEILING_DIRECTORIES=") {
			continue
		}

		filtered = append(filtered, e)
	}

	return filtered
}

// RootResolver resolves the repository root once and caches it, for a
// process that asks many times. The zero value is ready; it is safe for
// concurrent use. A failed resolution is cached too.
type RootResolver struct {
	mu   sync.Mutex
	root string
	err  error
}

// Get returns the root, resolving it with GitRoot on the first call.
func (r *RootResolver) Get(ctx context.Context, logger *slog.Logger) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.root != "" || r.err != nil {
		return r.root, r.err
	}

	root, err := GitRoot(ctx, logger)
	if err != nil {
		r.err = err

		return "", err
	}

	r.root = root

	return root, nil
}

// Set overrides the resolved root (tests that write to a temp directory
// outside any repository).
func (r *RootResolver) Set(root string) {
	r.mu.Lock()
	r.root, r.err = root, nil
	r.mu.Unlock()
}

// Reset forgets the cached root or error.
func (r *RootResolver) Reset() { r.Set("") }
