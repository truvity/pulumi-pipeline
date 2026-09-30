// Package pulumistep provides a Pulumi stack operation step for the pipeline framework.
// It wraps the Pulumi Automation API as a pipeline.StepFunc.
//
// When OnOutput is set, stack outputs (secrets excluded) are passed to the callback
// after both deploy and diff. The callback should use cfg.SaveFromMap or typed Save
// wrappers to persist outputs. When OnDestroy is set, it is called after a successful destroy.
package pulumistep

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optdestroy"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optrefresh"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"

	"github.com/truvity/pulumi-pipeline/pkg/pipeline"
)

type (
	// Config holds PulumiStep configuration.
	Config struct {
		Name      string // optional: override step name (default: StackName)
		WorkDir   string // relative path to Pulumi program directory
		StackName string // Pulumi stack name
		// OnOutput is called after deploy/diff with stack outputs (secrets excluded).
		OnOutput func(ctx context.Context, logger *slog.Logger, outputs map[string]any) error
		// OnDestroy is called after a successful destroy.
		OnDestroy func(ctx context.Context, logger *slog.Logger) error
	}
)

// PulumiStep creates a pipeline.Step that runs a Pulumi stack operation.
// Name is derived from cfg.StackName.
// Maps ActionDryRun→preview, ActionExecute→up, ActionSync→refresh, ActionDestroy→destroy.
//
// If cfg.OnOutput is set, stack outputs are passed to the callback after deploy/diff.
// If cfg.OnDestroy is set, it is called after a successful destroy.
func PulumiStep(cfg Config) pipeline.Step {
	name := cfg.StackName
	if cfg.Name != "" {
		name = cfg.Name
	}

	return pipeline.Step{
		Name:      name,
		Kind:      pipeline.StepPulumi,
		Fn:        pulumiStepFn(cfg),
		Actions:   pipeline.AllActions(),
		WorkDir:   cfg.WorkDir,
		StackName: cfg.StackName,
	}
}

// pulumiStepFn returns the StepFunc for a Pulumi step.
func pulumiStepFn(cfg Config) pipeline.StepFunc {
	return func(ctx context.Context, logger *slog.Logger, repoRoot string, action pipeline.Action) (*pipeline.StepResult, error) {
		absWorkDir := filepath.Join(repoRoot, cfg.WorkDir)

		stack, err := auto.SelectStackLocalSource(ctx, cfg.StackName, absWorkDir)
		if err != nil {
			return nil, fmt.Errorf("select stack %s in %s: %w", cfg.StackName, cfg.WorkDir, err)
		}

		color := pipeline.ColorMode()

		switch action {
		case pipeline.ActionDryRun:
			// By default, suppress pulumi:providers:* diff blocks whose only
			// change is the plugin version (e.g. aws 7.35.0 -> 7.39.0) — they
			// drown out real drift. --show-providers restores full output.
			// This only filters what's printed; the preview itself always
			// runs in full.
			var (
				progressOut io.Writer = os.Stdout
				filter      *providerFilterWriter
			)

			if !pipeline.ShowProviders(ctx) {
				filter = newProviderFilterWriter(os.Stdout)
				progressOut = filter
			}

			prev, prevErr := stack.Preview(ctx,
				optpreview.ProgressStreams(progressOut),
				optpreview.ErrorProgressStreams(os.Stderr),
				optpreview.Diff(),
				optpreview.Color(color),
			)

			if filter != nil {
				if flushErr := filter.Flush(); flushErr != nil && prevErr == nil {
					prevErr = flushErr
				}

				if n := filter.Suppressed(); n > 0 {
					_, _ = fmt.Fprintf(os.Stdout,
						"    (%d provider-version updates suppressed — --show-providers to include)\n\n", n)
				}
			}

			// On diff, call OnOutput callback with stack outputs — skipped
			// for a stack that has never been deployed (no outputs yet).
			if prevErr == nil && cfg.OnOutput != nil {
				prevErr = callOnOutputAfterDiff(ctx, logger, stack, cfg.OnOutput)
			}

			if prevErr != nil {
				return nil, fmt.Errorf("%s stack %s: %w", action, cfg.StackName, prevErr)
			}

			return toStepResult(cfg, prev.ChangeSummary), nil

		case pipeline.ActionExecute:
			upRes, upErr := stack.Up(ctx,
				optup.ProgressStreams(os.Stdout),
				optup.ErrorProgressStreams(os.Stderr),
				optup.Diff(),
				optup.Color(color),
			)

			// On deploy, call OnOutput callback with stack outputs.
			if upErr == nil && cfg.OnOutput != nil {
				upErr = callOnOutput(ctx, logger, stack, cfg.OnOutput)
			}

			if upErr != nil {
				return nil, fmt.Errorf("%s stack %s: %w", action, cfg.StackName, upErr)
			}

			// Gate: an apply is not done until a refresh-preview of the
			// stack it just changed plans nothing destructive.
			if verifyErr := verifyAfterApply(ctx, logger, stack, cfg.StackName); verifyErr != nil {
				return nil, fmt.Errorf("%s stack %s: %w", action, cfg.StackName, verifyErr)
			}

			var changes map[string]int
			if upRes.Summary.ResourceChanges != nil {
				changes = *upRes.Summary.ResourceChanges
			}

			return toStepResultFromStrMap(cfg, changes), nil

		case pipeline.ActionSync:
			_, err = stack.Refresh(ctx,
				optrefresh.ProgressStreams(os.Stdout),
				optrefresh.ErrorProgressStreams(os.Stderr),
				optrefresh.Color(color),
			)
		case pipeline.ActionDestroy:
			_, err = stack.Destroy(ctx,
				optdestroy.ProgressStreams(os.Stdout),
				optdestroy.ErrorProgressStreams(os.Stderr),
				optdestroy.Color(color),
			)
		default:
			return nil, fmt.Errorf("unknown action %d for stack %s", action, cfg.StackName)
		}

		if err != nil {
			return nil, fmt.Errorf("%s stack %s: %w", action, cfg.StackName, err)
		}

		return nil, nil
	}
}

// toStepResult converts a Pulumi preview ChangeSummary to a pipeline.StepResult.
func toStepResult(cfg Config, changes map[apitype.OpType]int) *pipeline.StepResult {
	if len(changes) == 0 {
		return nil
	}

	summary := make(map[string]int, len(changes))
	for op, count := range changes {
		summary[string(op)] = count
	}

	return &pipeline.StepResult{
		StepName:      cfg.StackName,
		ChangeSummary: summary,
	}
}

// toStepResultFromStrMap converts a string-keyed change map to a pipeline.StepResult.
func toStepResultFromStrMap(cfg Config, changes map[string]int) *pipeline.StepResult {
	if len(changes) == 0 {
		return nil
	}

	summary := make(map[string]int, len(changes))
	for op, count := range changes {
		summary[op] = count
	}

	return &pipeline.StepResult{
		StepName:      cfg.StackName,
		ChangeSummary: summary,
	}
}
