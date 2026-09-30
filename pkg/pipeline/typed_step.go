package pipeline

import (
	"context"
	"log/slog"
)

// ── Function signatures for typed steps ─────────────────────────────────────
//
// Step authors write natural Go functions. The framework wraps them into StepFunc
// via the typed step constructors below.

type (
	// RunFunc is a step with no typed input and no typed output.
	RunFunc = StepFunc

	// ProduceFunc is a step that produces typed output.
	ProduceFunc[Out any] func(ctx context.Context, logger *slog.Logger, repoRoot string, action Action) (Out, *StepResult, error)

	// ResolveFunc loads a step's output from existing state without executing the step.
	// Used for --exclusive mode when the producing step is skipped.
	ResolveFunc[Out any] func(ctx context.Context, logger *slog.Logger, repoRoot string) (Out, error)

	// ConsumeFunc is a step that takes typed input and produces no output.
	ConsumeFunc[In any] func(ctx context.Context, logger *slog.Logger, repoRoot string, action Action, in In) (*StepResult, error)

	// TransformFunc is a step that takes typed input and produces typed output.
	TransformFunc[In, Out any] func(ctx context.Context, logger *slog.Logger, repoRoot string, action Action, in In) (Out, *StepResult, error)

	// Consume2Func is a step that takes two typed inputs and produces no output.
	Consume2Func[A, B any] func(ctx context.Context, logger *slog.Logger, repoRoot string, action Action, a A, b B) (*StepResult, error)
)

// ── Typed step constructors ─────────────────────────────────────────────────
//
// Each constructor returns a *Deferred that downstream steps can depend on.
// The constructor also registers a Step in the scope's step list via the collector.
//
// The "after" variadic parameter accepts ordering-only dependencies (Deferred[None]
// or any other Deferred whose value is not consumed by this step).

// TypedRun creates a step with no typed input and no typed output.
// Returns a Deferred[None] that serves as an ordering token for downstream steps.
func TypedRun(name string, fn RunFunc, after ...node) (*Deferred[None], Step) {
	d := newDeferred[None](name)
	deps := collectDeps(after)

	step := Step{
		Name:      name,
		Kind:      StepExec,
		Actions:   DefaultExecActions(),
		DependsOn: deps,
		Fn: func(ctx context.Context, logger *slog.Logger, repoRoot string, action Action) (*StepResult, error) {
			result, err := fn(ctx, logger, repoRoot, action)
			if err != nil {
				return result, err
			}

			d.set(None{})

			return result, nil
		},
	}

	return d, step
}

// TypedProduce creates a step that produces typed output.
// The produce function runs during execution; the optional resolve function
// loads the output from existing state when the step is skipped (--exclusive).
func TypedProduce[Out any](
	name string,
	fn ProduceFunc[Out],
	resolve ResolveFunc[Out],
	after ...node,
) (*Deferred[Out], Step) {
	d := newDeferred[Out](name)
	deps := collectDeps(after)

	step := Step{
		Name:      name,
		Kind:      StepExec,
		Actions:   DefaultExecActions(),
		DependsOn: deps,
		Fn: func(ctx context.Context, logger *slog.Logger, repoRoot string, action Action) (*StepResult, error) {
			out, result, err := fn(ctx, logger, repoRoot, action)
			if err != nil {
				return result, err
			}

			d.set(out)

			return result, nil
		},
	}

	// Store the resolve function on the deferred for --exclusive mode.
	if resolve != nil {
		d.resolve = resolve
	}

	return d, step
}

// TypedConsume creates a step that takes typed input and produces no output.
// The input deferred creates a data dependency edge — the producing step
// must complete before this step runs.
func TypedConsume[In any](
	name string,
	fn ConsumeFunc[In],
	in *Deferred[In],
	after ...node,
) (*Deferred[None], Step) {
	d := newDeferred[None](name)
	deps := collectDeps(append([]node{in}, after...))

	step := Step{
		Name:      name,
		Kind:      StepExec,
		Actions:   DefaultExecActions(),
		DependsOn: deps,
		Fn: func(ctx context.Context, logger *slog.Logger, repoRoot string, action Action) (*StepResult, error) {
			if err := ensureResolved(ctx, in, logger, repoRoot); err != nil {
				return nil, err
			}

			result, err := fn(ctx, logger, repoRoot, action, in.Get())
			if err != nil {
				return result, err
			}

			d.set(None{})

			return result, nil
		},
	}

	return d, step
}

// TypedTransform creates a step that takes typed input and produces typed output.
func TypedTransform[In, Out any](
	name string,
	fn TransformFunc[In, Out],
	resolve ResolveFunc[Out],
	in *Deferred[In],
	after ...node,
) (*Deferred[Out], Step) {
	d := newDeferred[Out](name)
	deps := collectDeps(append([]node{in}, after...))

	step := Step{
		Name:      name,
		Kind:      StepExec,
		Actions:   DefaultExecActions(),
		DependsOn: deps,
		Fn: func(ctx context.Context, logger *slog.Logger, repoRoot string, action Action) (*StepResult, error) {
			if err := ensureResolved(ctx, in, logger, repoRoot); err != nil {
				return nil, err
			}

			out, result, err := fn(ctx, logger, repoRoot, action, in.Get())
			if err != nil {
				return result, err
			}

			d.set(out)

			return result, nil
		},
	}

	if resolve != nil {
		d.resolve = resolve
	}

	return d, step
}

// TypedConsume2 creates a step that takes two typed inputs and produces no output.
func TypedConsume2[A, B any](
	name string,
	fn Consume2Func[A, B],
	inA *Deferred[A],
	inB *Deferred[B],
	after ...node,
) (*Deferred[None], Step) {
	d := newDeferred[None](name)
	deps := collectDeps(append([]node{inA, inB}, after...))

	step := Step{
		Name:      name,
		Kind:      StepExec,
		Actions:   DefaultExecActions(),
		DependsOn: deps,
		Fn: func(ctx context.Context, logger *slog.Logger, repoRoot string, action Action) (*StepResult, error) {
			if err := ensureResolved(ctx, inA, logger, repoRoot); err != nil {
				return nil, err
			}

			if err := ensureResolved(ctx, inB, logger, repoRoot); err != nil {
				return nil, err
			}

			result, err := fn(ctx, logger, repoRoot, action, inA.Get(), inB.Get())
			if err != nil {
				return result, err
			}

			d.set(None{})

			return result, nil
		},
	}

	return d, step
}

// ── Step options for typed steps ────────────────────────────────────────────

// ApplyOpts applies ExecOptions to a Step returned by a typed step constructor.
// This allows setting Actions, DependsOn, etc. on typed steps.
func ApplyOpts(s *Step, opts ...ExecOption) {
	for _, opt := range opts {
		opt(s)
	}
}

// ── Internal helpers ────────────────────────────────────────────────────────

// ensureResolved attempts to resolve a deferred from existing state if it hasn't
// been set by a prior step. This happens during destroy (reverse order) when the
// producing step hasn't run yet. Returns an error if resolution fails.
func ensureResolved[T any](ctx context.Context, d *Deferred[T], logger *slog.Logger, repoRoot string) error {
	if d.IsResolved() {
		return nil
	}

	return d.ResolveFromState(ctx, logger, repoRoot)
}

// collectDeps extracts step names from deferred nodes for DependsOn.
func collectDeps(nodes []node) []string {
	if len(nodes) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(nodes))
	deps := make([]string, 0, len(nodes))

	for _, n := range nodes {
		name := n.stepName()
		if name != "" && !seen[name] {
			seen[name] = true
			deps = append(deps, name)
		}
	}

	return deps
}
