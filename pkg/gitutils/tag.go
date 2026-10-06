package gitutils

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"strings"
)

// CreateLocalTag creates a local git tag
// dir: Working directory (usually git root)
// tagName: Name of the tag to create
func CreateLocalTag(ctx context.Context, logger *slog.Logger, dir, tagName string) error {
	cmd := exec.CommandContext(ctx, "git", "tag", tagName)
	cmd.Dir = dir
	cmd.Stderr = io.Discard

	if err := cmd.Run(); err != nil {
		return NewGitError("tag", err)
	}

	logger.InfoContext(ctx, "created local git tag",
		slog.String("tag", tagName),
		slog.String("dir", dir))
	return nil
}

// TagExists checks if a git tag exists
// dir: Working directory (usually git root)
// tagName: Name of the tag to check
func TagExists(ctx context.Context, logger *slog.Logger, dir, tagName string) (bool, error) {
	var stdoutStream strings.Builder
	cmd := exec.CommandContext(ctx, "git", "tag", "-l", tagName)
	cmd.Dir = dir
	cmd.Stdout = &stdoutStream
	cmd.Stderr = io.Discard

	if err := cmd.Run(); err != nil {
		return false, NewGitError("tag", err)
	}

	output := strings.TrimSpace(stdoutStream.String())
	exists := output == tagName

	logger.DebugContext(ctx, "checked tag existence",
		slog.String("tag", tagName),
		slog.Bool("exists", exists),
		slog.String("dir", dir))
	return exists, nil
}

// GetCurrentTag returns the tag name if HEAD is on a tag, empty string otherwise
// dir: Working directory (usually git root)
func GetCurrentTag(ctx context.Context, logger *slog.Logger, dir string) (string, error) {
	var stdoutStream strings.Builder
	cmd := exec.CommandContext(ctx, "git", "describe", "--exact-match", "--tags", "HEAD")
	cmd.Dir = dir
	cmd.Stdout = &stdoutStream
	cmd.Stderr = io.Discard

	if err := cmd.Run(); err != nil {
		// HEAD is not on a tag
		logger.DebugContext(ctx, "HEAD is not on a tag",
			slog.String("dir", dir))
		return "", nil
	}

	tag := strings.TrimSpace(stdoutStream.String())
	logger.DebugContext(ctx, "found current tag",
		slog.String("tag", tag),
		slog.String("dir", dir))
	return tag, nil
}

// CheckoutTag checks out a specific git tag
// dir: Working directory (usually git root)
// tagName: Name of the tag to checkout
func CheckoutTag(ctx context.Context, logger *slog.Logger, dir, tagName string) error {
	cmd := exec.CommandContext(ctx, "git", "checkout", tagName)
	cmd.Dir = dir
	cmd.Stderr = io.Discard

	if err := cmd.Run(); err != nil {
		return NewGitError("checkout", err)
	}

	logger.InfoContext(ctx, "checked out tag",
		slog.String("tag", tagName),
		slog.String("dir", dir))
	return nil
}
