// Package gitutils holds small helpers over the git command line: the
// repository root, the branch, tags, commits and the dirty check.
package gitutils

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"strings"
)

// GetCurrentBranch returns the current branch name
// dir: Working directory (usually git root)
func GetCurrentBranch(ctx context.Context, logger *slog.Logger, dir string) (string, error) {
	var stdoutStream strings.Builder
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	cmd.Stdout = &stdoutStream
	cmd.Stderr = io.Discard

	if err := cmd.Run(); err != nil {
		return "", NewGitError("rev-parse", err)
	}

	branch := strings.TrimSpace(stdoutStream.String())
	logger.DebugContext(ctx, "git current branch",
		slog.String("branch", branch),
		slog.String("dir", dir))
	return branch, nil
}
