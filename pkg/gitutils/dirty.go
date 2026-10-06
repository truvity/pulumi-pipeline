package gitutils

import (
	"context"
	"log/slog"
	"os/exec"
	"strings"
)

// IsDirty checks if the working directory has uncommitted changes
// dir: Working directory (usually git root)
func IsDirty(ctx context.Context, logger *slog.Logger, dir string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = dir

	output, err := cmd.Output()
	if err != nil {
		return false, NewGitError("status", err)
	}

	isDirty := strings.TrimSpace(string(output)) != ""
	logger.DebugContext(ctx, "checked git dirty status",
		slog.Bool("is_dirty", isDirty),
		slog.String("dir", dir))
	return isDirty, nil
}
