package pipeline

import (
	"context"
	"fmt"
	"log/slog"
)

type (
	// None is the unit type for steps that produce no meaningful output.
	// A Deferred[None] serves as an ordering token — it carries the dependency
	// edge without data.
	None struct{}

	// node is the interface implemented by all deferred values.
	// The pipeline framework uses it to extract DAG edges from deferred connections.
	node interface {
		// stepName returns the name of the step that produces this deferred.
		stepName() string
	}

	// Deferred is a typed placeholder for a step's output.
	// It is created at scope-build time (empty) and filled at execution time.
	//
	// Passing a Deferred from one step to another creates a DAG dependency edge.
	// The step function never sees the Deferred — it receives the unwrapped value.
	//
	// Three resolution states:
	//   - Unresolved: created but not yet filled (before step executes)
	//   - Executed: filled by the step's Execute/Preview function
	//   - Resolved: filled by the step's Resolve function (--exclusive mode)
	//
	// Thread safety: Deferred is NOT synchronized internally. The DAG executor
	// guarantees happens-before ordering — a producer step completes before any
	// consumer step starts. If the executor is changed to allow concurrent
	// execution without barriers, synchronization (mutex/atomic) must be added
	// to set and readers.
	Deferred[T any] struct {
		name     string // step name that produces this value
		val      T
		resolved bool
		resolve  func(ctx context.Context, logger *slog.Logger, repoRoot string) (T, error) // optional: load from existing state
	}
)

// newDeferred creates an unresolved Deferred associated with a step name.
func newDeferred[T any](name string) *Deferred[T] {
	return &Deferred[T]{name: name}
}

// stepName implements the node interface.
func (d *Deferred[T]) stepName() string {
	return d.name
}

// set stores the value in the deferred. Called by the framework after step execution.
func (d *Deferred[T]) set(val T) {
	d.val = val
	d.resolved = true
}

// Get returns the stored value. Panics if the deferred is unresolved.
// This is a programming error — the DAG should guarantee the producer runs first.
func (d *Deferred[T]) Get() T {
	if !d.resolved {
		panic(fmt.Sprintf("pipeline: deferred %q not resolved — check step dependencies", d.name))
	}

	return d.val
}

// IsResolved returns true if the deferred has been filled.
func (d *Deferred[T]) IsResolved() bool {
	return d.resolved
}

// ResolveFromState attempts to fill the deferred from existing state using the
// resolve function. Returns an error if no resolve function is set or if it fails.
// This is called by the executor when a producing step is skipped (--exclusive).
func (d *Deferred[T]) ResolveFromState(ctx context.Context, logger *slog.Logger, repoRoot string) error {
	if d.resolved {
		return nil
	}

	if d.resolve == nil {
		return fmt.Errorf("step %q was skipped but has no resolve function — run it first", d.name)
	}

	val, err := d.resolve(ctx, logger, repoRoot)
	if err != nil {
		return fmt.Errorf("resolve %q from existing state: %w", d.name, err)
	}

	d.val = val
	d.resolved = true

	return nil
}
