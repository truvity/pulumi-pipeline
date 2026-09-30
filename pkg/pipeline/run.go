package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// RunOptions selects how Run treats its targets and which gates it applies.
type RunOptions struct {
	// Exclusive runs only the listed steps, with no dependency resolution.
	Exclusive bool
	// All runs every step in topological order (reverse for destroy).
	All bool
	// Detail prints the resource-level summary after a dry run.
	Detail bool
	// ShowProviders keeps provider-version-only diffs in a dry run's output.
	ShowProviders bool
	// StepTimeout bounds each step. Zero means DefaultStepTimeout; a negative
	// value disables the bound.
	StepTimeout time.Duration
	// ExpectNoChanges fails a dry run whose steps report any change. It is an
	// error with any other action, because "no changes" of an apply is not a
	// thing a caller can check before it happens.
	ExpectNoChanges bool
	// AllowStaleCheckout lets deploy and destroy run from a checkout that is
	// dirty, behind its upstream or unverifiable. The refusal it overrides is
	// logged as a warning instead.
	AllowStaleCheckout bool
}

// Run executes the pipeline steps with target-based resolution. It is RunWith
// with the default gates; see RunWith for the semantics.
func Run(
	ctx context.Context,
	logger *slog.Logger,
	cfg Config,
	action Action,
	targets []string,
	exclusive, all, detail, showProviders bool,
) error {
	return RunWith(ctx, logger, cfg, action, targets, RunOptions{
		Exclusive:     exclusive,
		All:           all,
		Detail:        detail,
		ShowProviders: showProviders,
	})
}

// RunWith executes the pipeline steps with target-based resolution.
//
// Target resolution modes:
//   - no targets + no All: returns an error
//   - All: uses all steps in topological order (reverse for destroy)
//   - targets without Exclusive: resolves transitive deps (deploy/diff/refresh/doctor)
//     or transitive dependents (destroy)
//   - targets with Exclusive: filters to only the listed steps in topological order
//     (reverse for destroy)
//
// Steps whose ActionSet does not support the current action are skipped with an
// info log.
//
// Failure handling differs by action: ActionDryRun (diff) is continue-on-error —
// a failing step is recorded and the sweep continues so a single flaky/broken
// scope doesn't hide drift in the rest of the stacks. All other actions
// (deploy/destroy/refresh) remain fail-fast and stop on the first step failure
// with a wrapped error "step %s: %w". When diff collects one or more failures,
// RunWith prints a per-step failure summary and returns a non-nil error so the
// process exits non-zero.
//
// Gates, in the order they act:
//   - ExpectNoChanges is refused for any action but ActionDryRun.
//   - Deploy and destroy refuse a dirty checkout or one behind its upstream
//     (CheckCheckout) unless AllowStaleCheckout says otherwise.
//   - Every step runs under StepTimeout.
//   - With ExpectNoChanges, a dry-run step that reports any change fails.
func RunWith(
	ctx context.Context,
	logger *slog.Logger,
	cfg Config,
	action Action,
	targets []string,
	opts RunOptions,
) error {
	if len(targets) == 0 && !opts.All {
		return fmt.Errorf("specify stack names or --all")
	}

	if opts.ExpectNoChanges && action != ActionDryRun {
		return fmt.Errorf("--expect-no-changes only applies to diff, not %s", action)
	}

	repoRoot, err := cfg.RootDir()
	if err != nil {
		return fmt.Errorf("find repo root: %w", err)
	}

	steps, err := resolveTargets(cfg.Steps, action, targets, opts.Exclusive, opts.All)
	if err != nil {
		return err
	}

	if action == ActionExecute || action == ActionDestroy {
		if err := requireFreshCheckout(ctx, logger, repoRoot, opts.AllowStaleCheckout); err != nil {
			return err
		}
	}

	// Run pre-flight hook (e.g., render CRDs) before any step executes.
	if cfg.BeforeRun != nil {
		if err := cfg.BeforeRun(ctx, logger, repoRoot, action); err != nil {
			return fmt.Errorf("before-run: %w", err)
		}
	}

	ctx = WithShowProviders(ctx, opts.ShowProviders)
	timeout := opts.stepTimeout()

	logger.InfoContext(ctx, "starting pipeline",
		slog.String("action", action.String()),
		slog.Int("steps", len(steps)),
		slog.Duration("step_timeout", max(timeout, 0)),
	)

	results := make(map[string]*StepResult)

	var failures []StepFailure

	for i := range steps {
		s := &steps[i]

		if !s.Actions.Supports(action) {
			logger.InfoContext(ctx, "skipping step (action not supported)",
				slog.String("step", s.Name),
				slog.String("action", action.String()),
			)

			continue
		}

		logger.InfoContext(ctx, "executing step",
			slog.String("step", s.Name),
			slog.String("action", action.String()),
		)

		result, stepErr := runStep(ctx, logger, s, repoRoot, action, timeout)

		if stepErr == nil && opts.ExpectNoChanges {
			stepErr = expectNoChanges(s.Name, result)
		}

		if stepErr != nil {
			if action != ActionDryRun {
				return fmt.Errorf("step %s: %w", s.Name, stepErr)
			}

			// diff is continue-on-error: record the failure and keep sweeping
			// the rest of the steps instead of aborting the whole scope.
			logger.ErrorContext(ctx, "step failed, continuing (diff is continue-on-error)",
				slog.String("step", s.Name),
				slog.Any("error", stepErr),
			)

			failures = append(failures, StepFailure{StepName: s.Name, Err: stepErr})

			if result != nil {
				results[s.Name] = result
			}

			continue
		}

		if result != nil {
			results[s.Name] = result
		}

		logger.InfoContext(ctx, "step completed", slog.String("step", s.Name))
	}

	if action == ActionDryRun && len(results) > 0 {
		PrintSummaryTable(os.Stdout, results)

		if opts.Detail {
			PrintDetailedSummary(os.Stdout, results)
		}
	}

	if len(failures) > 0 {
		PrintFailureSummary(os.Stdout, failures)

		return fmt.Errorf("%d of %d step(s) failed", len(failures), len(steps))
	}

	return nil
}

// resolveTargets determines the execution set based on targets and flags.
func resolveTargets(cfgSteps []Step, action Action, targets []string, exclusive, all bool) ([]Step, error) {
	isDestroy := action == ActionDestroy

	if all {
		sorted, err := TopologicalSort(cfgSteps)
		if err != nil {
			return nil, fmt.Errorf("topological sort: %w", err)
		}

		if isDestroy {
			reverseSteps(sorted)
		}

		return sorted, nil
	}

	// Validate all targets exist.
	for _, t := range targets {
		if !hasStep(cfgSteps, t) {
			return nil, fmt.Errorf("unknown step %q (available: %s)", t, StepNames(cfgSteps))
		}
	}

	if exclusive {
		return resolveExclusive(cfgSteps, targets, isDestroy)
	}

	if isDestroy {
		return ResolveMultiDependents(cfgSteps, targets)
	}

	return ResolveMultiSubgraph(cfgSteps, targets)
}

// resolveExclusive filters steps to only those explicitly listed, in topological
// order (reverse for destroy). Dependencies pointing outside the subset are ignored.
func resolveExclusive(cfgSteps []Step, targets []string, isDestroy bool) ([]Step, error) {
	targetSet := make(map[string]bool, len(targets))
	for _, t := range targets {
		targetSet[t] = true
	}

	var subset []Step

	for i := range cfgSteps {
		if targetSet[cfgSteps[i].Name] {
			subset = append(subset, cfgSteps[i])
		}
	}

	// Strip DependsOn entries that reference steps outside the subset.
	subset = filterDepsToSubset(subset)

	sorted, err := TopologicalSort(subset)
	if err != nil {
		return nil, fmt.Errorf("topological sort (exclusive): %w", err)
	}

	if isDestroy {
		reverseSteps(sorted)
	}

	return sorted, nil
}

// filterDepsToSubset returns a copy of steps where DependsOn entries only
// reference steps within the set. This is needed when creating subsets that
// may have external dependencies.
func filterDepsToSubset(steps []Step) []Step {
	nameSet := make(map[string]bool, len(steps))
	for i := range steps {
		nameSet[steps[i].Name] = true
	}

	result := make([]Step, len(steps))
	copy(result, steps)

	for i := range result {
		if len(result[i].DependsOn) == 0 {
			continue
		}

		var filtered []string

		for _, dep := range result[i].DependsOn {
			if nameSet[dep] {
				filtered = append(filtered, dep)
			}
		}

		result[i].DependsOn = filtered
	}

	return result
}

// reverseSteps reverses a slice of steps in place.
func reverseSteps(steps []Step) {
	for i, j := 0, len(steps)-1; i < j; i, j = i+1, j-1 {
		steps[i], steps[j] = steps[j], steps[i]
	}
}
