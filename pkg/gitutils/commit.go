package gitutils

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"strings"
)

// GetCommitHash returns the commit hash (short or full)
// dir: Working directory (usually git root)
// short: If true, returns short hash (--short), otherwise full hash
func GetCommitHash(ctx context.Context, logger *slog.Logger, dir string, short bool) (string, error) {
	args := []string{"rev-parse"}
	if short {
		args = append(args, "--short")
	}
	args = append(args, "HEAD")

	var stdoutStream strings.Builder
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Stdout = &stdoutStream
	cmd.Stderr = io.Discard

	if err := cmd.Run(); err != nil {
		return "", NewGitError("rev-parse", err)
	}

	hash := strings.TrimSpace(stdoutStream.String())
	logger.DebugContext(ctx, "git commit hash",
		slog.String("hash", hash),
		slog.Bool("short", short),
		slog.String("dir", dir))
	return hash, nil
}

// GetShortCommitHash returns the short commit hash
// Convenience wrapper around GetCommitHash(ctx, logger, dir, true)
func GetShortCommitHash(ctx context.Context, logger *slog.Logger, dir string) (string, error) {
	return GetCommitHash(ctx, logger, dir, true)
}
