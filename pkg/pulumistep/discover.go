package pulumistep

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/truvity/pulumi-pipeline/pkg/pipeline"
)

type (
	// OutputFunc receives a stack's non-secret outputs after a deploy, and after
	// a diff of a stack that has outputs.
	OutputFunc = func(ctx context.Context, logger *slog.Logger, outputs map[string]any) error

	// OutputHooks decides, stack by stack, whether outputs are persisted and
	// where. It is how a caller plugs in its own store: the library writes
	// nothing itself.
	OutputHooks interface {
		// OutputsFor returns the callback for one stack, or nil to persist
		// nothing for it. root is the checkout root; scope and stack are the
		// discovered directory name and stack name.
		OutputsFor(root, scope, stack string) OutputFunc
	}

	// OutputHooksFunc adapts a function to OutputHooks.
	OutputHooksFunc func(root, scope, stack string) OutputFunc

	// DiscoverOptions says where scopes live.
	DiscoverOptions struct {
		// Root is the checkout root. Required.
		Root string
		// ScopesDir is the directory under Root whose subdirectories are the
		// scopes, e.g. "deploy". Required.
		ScopesDir string
		// Hooks decides output persistence. Nil persists nothing.
		Hooks OutputHooks
	}
)

// OutputsFor implements OutputHooks.
func (f OutputHooksFunc) OutputsFor(root, scope, stack string) OutputFunc {
	return f(root, scope, stack)
}

// Discover builds one pipeline.Scope per subdirectory of ScopesDir that holds
// at least one Pulumi.{stack}.yaml stack file, with one Pulumi step per stack
// file. Adding a stack file is enough; nothing is registered. Scopes come back
// sorted by name, and a subdirectory without a stack file is not a scope.
func Discover(opts DiscoverOptions) ([]pipeline.Scope, error) {
	if opts.Root == "" || opts.ScopesDir == "" {
		return nil, fmt.Errorf("discover: both a root and a scopes directory are required")
	}

	entries, err := os.ReadDir(filepath.Join(opts.Root, opts.ScopesDir))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", opts.ScopesDir, err)
	}

	var scopes []pipeline.Scope

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		scopeName := e.Name()
		workDir := filepath.Join(opts.ScopesDir, scopeName)

		stacks, err := DiscoverStacks(filepath.Join(opts.Root, workDir))
		if err != nil {
			return nil, fmt.Errorf("scope %s: %w", scopeName, err)
		}

		if len(stacks) == 0 {
			continue
		}

		steps := make([]pipeline.Step, 0, len(stacks))

		for _, stack := range stacks {
			var onOutput OutputFunc
			if opts.Hooks != nil {
				onOutput = opts.Hooks.OutputsFor(opts.Root, scopeName, stack)
			}

			steps = append(steps, PulumiStep(Config{
				WorkDir:   workDir,
				StackName: stack,
				OnOutput:  onOutput,
			}))
		}

		scopes = append(scopes, pipeline.Scope{
			Name:  scopeName,
			Usage: fmt.Sprintf("%d stacks in %s", len(steps), workDir),
			Steps: steps,
		})
	}

	sort.Slice(scopes, func(i, j int) bool { return scopes[i].Name < scopes[j].Name })

	return scopes, nil
}

// DiscoverStacks lists stack names from Pulumi.{stack}.yaml files in dir,
// sorted. Pulumi.yaml is the project file, not a stack.
func DiscoverStacks(dir string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "Pulumi.*.yaml"))
	if err != nil {
		return nil, err
	}

	var stacks []string

	for _, m := range matches {
		base := filepath.Base(m)
		name := strings.TrimSuffix(strings.TrimPrefix(base, "Pulumi."), ".yaml")

		if name == "" || base == "Pulumi.yaml" {
			continue
		}

		stacks = append(stacks, name)
	}

	sort.Strings(stacks)

	return stacks, nil
}
