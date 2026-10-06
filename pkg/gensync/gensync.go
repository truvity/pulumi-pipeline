// Package gensync writes a deployed Pulumi stack's non-secret outputs into a
// committed file, gen/{scope}/{stack}.yaml under a directory of the consuming
// repository.
//
// The file's shape and generated-file header belong to the consumer, so the
// write itself is a Saver the consumer supplies; this package owns the rest:
// the path, the name checks (each segment becomes a filesystem component and
// a pulumi argument), the opt-in rule, and the standalone sync that reads the
// outputs back with `pulumi stack output`.
//
// There are two routes into the same write. The pipeline already holds the
// outputs in memory after a deploy and uses Hooks; Sync serves a stack
// deployed earlier.
package gensync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"github.com/truvity/pulumi-pipeline/pkg/pulumistep"
)

// nameRe guards both segments: each becomes a filesystem path component
// and, in Sync's case, a pulumi CLI argument.
var nameRe = regexp.MustCompile(`^[a-z0-9-]+$`)

type (
	// Saver persists a stack's outputs at path, which is relative to the
	// consumer's config directory ("gen/{scope}/{stack}.yaml"). Secrets are
	// already filtered out by the caller.
	Saver func(ctx context.Context, logger *slog.Logger, path string, outputs map[string]any) error

	// Hooks implements pulumistep.OutputHooks: a stack's outputs are written
	// when, and only when, its gen file already exists.
	Hooks struct {
		// ConfigDir is the config directory, relative to the repository root
		// ("cfg"). The gen files live under ConfigDir/gen.
		ConfigDir string
		// Save writes one file.
		Save Saver
	}

	// SyncOptions is the input of Sync.
	SyncOptions struct {
		// Root is the repository root.
		Root string
		// ScopesDir is the directory of Pulumi programs, relative to Root;
		// the scope's program is ScopesDir/{scope}.
		ScopesDir string
		// Save writes the file.
		Save Saver
	}
)

// Path returns the config-relative path of a stack's gen file.
func Path(scope, stack string) string {
	return fmt.Sprintf("gen/%s/%s.yaml", scope, stack)
}

// ValidateNames rejects any segment that is not a plain lowercase name.
func ValidateNames(segments ...string) error {
	for _, seg := range segments {
		if !nameRe.MatchString(seg) {
			return fmt.Errorf("invalid name %q (want [a-z0-9-]+)", seg)
		}
	}

	return nil
}

// Write persists a stack's outputs through save.
//
// Secrets must already have been filtered by the caller: pulumistep's
// OutputsToMap does this, which is why a credential cannot reach a committed
// file by anyone forgetting to think about it.
func Write(ctx context.Context, logger *slog.Logger, save Saver, scope, stack string, outputs map[string]any) error {
	if err := ValidateNames(scope, stack); err != nil {
		return err
	}

	if len(outputs) == 0 {
		return fmt.Errorf("stack %s/%s has no outputs", scope, stack)
	}

	path := Path(scope, stack)
	if err := save(ctx, logger, path, outputs); err != nil {
		return fmt.Errorf("save %s: %w", path, err)
	}

	logger.InfoContext(ctx, "gen file synced",
		slog.String("path", path),
		slog.Int("outputs", len(outputs)),
	)

	return nil
}

// OutputsFor returns the callback that writes the stack's outputs, or nil
// when the stack has no gen file yet.
//
// The FILESYSTEM is the opt-in, deliberately. Syncing every stack would mint
// a committed file for each one on its first run, a large and surprising diff
// for stacks whose outputs nobody consumes. Requiring the file to exist makes
// enrolment an explicit act (create the gen file once, or run Sync) while an
// unenrolled stack behaves exactly as before.
func (h Hooks) OutputsFor(repoRoot, scope, stack string) pulumistep.OutputFunc {
	if _, err := os.Stat(filepath.Join(repoRoot, h.ConfigDir, Path(scope, stack))); err != nil {
		return nil
	}

	return func(ctx context.Context, logger *slog.Logger, outputs map[string]any) error {
		return Write(ctx, logger, h.Save, scope, stack, outputs)
	}
}

// Sync reads a deployed stack's outputs with `pulumi stack output --json` and
// writes them. It needs the credentials of the scope's state backend.
func Sync(ctx context.Context, logger *slog.Logger, opts SyncOptions, scope, stack string) error {
	if err := ValidateNames(scope, stack); err != nil {
		return err
	}

	if opts.Save == nil {
		return errors.New("gensync: no Saver")
	}

	cmd := exec.CommandContext(ctx, "pulumi", "stack", "output", "--json", "--stack", stack) //nolint:gosec // scope and stack validated against ^[a-z0-9-]+$ above
	cmd.Dir = filepath.Join(opts.Root, opts.ScopesDir, scope)
	cmd.Stderr = os.Stderr

	raw, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("pulumi stack output: %w", err)
	}

	var outputs map[string]any
	if err := json.Unmarshal(raw, &outputs); err != nil {
		return fmt.Errorf("parse outputs: %w", err)
	}

	return Write(ctx, logger, opts.Save, scope, stack, outputs)
}
