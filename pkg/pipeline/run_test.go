package pipeline_test

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	slogt "github.com/neilotoole/slogt/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/truvity/pulumi-pipeline/pkg/pipeline"
)

type (
	// recorder tracks which steps were executed and in what order.
	recorder struct {
		mu    sync.Mutex
		order []string
	}
)

func (r *recorder) record(name string) pipeline.StepFunc {
	return func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action) (*pipeline.StepResult, error) {
		r.mu.Lock()
		defer r.mu.Unlock()

		r.order = append(r.order, name)

		return nil, nil
	}
}

func (r *recorder) failAt(name string) pipeline.StepFunc {
	return func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action) (*pipeline.StepResult, error) {
		r.mu.Lock()
		defer r.mu.Unlock()

		r.order = append(r.order, name)

		return nil, fmt.Errorf("intentional failure at %s", name)
	}
}

func (r *recorder) executed() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	result := make([]string, len(r.order))
	copy(result, r.order)

	return result
}

// --- Linear Pipeline Tests ---

func linearPipeline(rec *recorder) pipeline.Config {
	return pipeline.Config{
		Name:  "linear-test",
		Usage: "Test linear chain fallback",
		Steps: []pipeline.Step{
			pipeline.ExecStep("step-a", rec.record("step-a")),
			pipeline.ExecStep("step-b", rec.record("step-b")),
			pipeline.ExecStep("step-c", rec.record("step-c")),
		},
	}
}

func TestLinearPipeline_ExecutesInOrder(t *testing.T) {
	rec := &recorder{}
	cfg := linearPipeline(rec)
	logger := slogt.New(t)

	err := pipeline.Run(context.Background(), logger, cfg, pipeline.ActionExecute, nil, false, true, false, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"step-a", "step-b", "step-c"}, rec.executed())
}

func TestLinearPipeline_ActionSkipping(t *testing.T) {
	rec := &recorder{}
	cfg := pipeline.Config{
		Name:  "action-skip-test",
		Usage: "Test action capability skipping",
		Steps: []pipeline.Step{
			pipeline.ExecStep("step-a", rec.record("step-a")),
			pipeline.ExecStep("step-b", rec.record("step-b"),
				pipeline.WithActions(pipeline.NewActionSet(pipeline.ActionExecute))), // only Execute
			pipeline.ExecStep("step-c", rec.record("step-c")),
		},
	}
	logger := slogt.New(t)

	// DryRun should skip step-b (it only supports Execute).
	err := pipeline.Run(context.Background(), logger, cfg, pipeline.ActionDryRun, nil, false, true, false, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"step-a", "step-c"}, rec.executed())
}

// --- DAG with Mixed Step Types ---

func dagPipeline(rec *recorder) pipeline.Config {
	// Simulate mixed step types using ExecSteps with different ActionSets.
	// "chart-a" has Helm-like actions (DryRun + Execute + Destroy).
	// "manifests-b" has Kustomize-like actions (DryRun + Execute + Destroy).
	helmActions := pipeline.NewActionSet(pipeline.ActionDryRun, pipeline.ActionExecute, pipeline.ActionDestroy)

	return pipeline.Config{
		Name:  "dag-test",
		Usage: "Test DAG with mixed step types",
		Steps: []pipeline.Step{
			pipeline.ExecStep("init", rec.record("init")),
			pipeline.ExecStep("chart-a", rec.record("chart-a"),
				pipeline.WithDependsOn("init"),
				pipeline.WithActions(helmActions)),
			pipeline.ExecStep("manifests-b", rec.record("manifests-b"),
				pipeline.WithDependsOn("init"),
				pipeline.WithActions(helmActions)),
			pipeline.ExecStep("generate/config", rec.record("generate/config"),
				pipeline.WithDependsOn("init")),
			pipeline.ExecStep("apply-config", rec.record("apply-config"),
				pipeline.WithDependsOn("generate/config")),
		},
	}
}

func TestDAGPipeline_TopologicalOrder(t *testing.T) {
	rec := &recorder{}
	cfg := dagPipeline(rec)
	logger := slogt.New(t)

	err := pipeline.Run(context.Background(), logger, cfg, pipeline.ActionExecute, nil, false, true, false, false)
	require.NoError(t, err)

	executed := rec.executed()
	// init must be first (all others depend on it).
	assert.Equal(t, "init", executed[0])
	// apply-config must come after generate/config.
	genIdx := indexOf(executed, "generate/config")
	applyIdx := indexOf(executed, "apply-config")
	assert.Greater(t, applyIdx, genIdx, "apply-config should come after generate/config")
}

func TestDAGPipeline_ActionFiltering_SyncSkipsHelmKustomize(t *testing.T) {
	rec := &recorder{}
	cfg := dagPipeline(rec)
	logger := slogt.New(t)

	// Sync should skip all steps: the Helm/Kustomize-like steps don't support Sync
	// (not in their ActionSet), and ExecStep's default ActionSet is DryRun+Execute
	// which also excludes Sync.
	err := pipeline.Run(context.Background(), logger, cfg, pipeline.ActionSync, nil, false, true, false, false)
	require.NoError(t, err)

	executed := rec.executed()
	assert.Empty(t, executed, "all steps should be skipped — none support ActionSync by default")
}

// --- Pipeline with ConfirmStep ---

func confirmPipeline(rec *recorder) pipeline.Config {
	return pipeline.Config{
		Name:  "confirm-test",
		Usage: "Test confirm gate",
		Steps: []pipeline.Step{
			pipeline.ExecStep("create-env", rec.record("create-env")),
			pipeline.ConfirmStep("confirm-dangerous-op", "Are you sure you want to proceed?"),
			pipeline.ExecStep("dangerous-op", rec.record("dangerous-op"),
				pipeline.WithDependsOn("confirm-dangerous-op")),
			pipeline.ExecStep("verify", rec.record("verify"),
				pipeline.WithDependsOn("dangerous-op")),
		},
	}
}

func TestConfirmPipeline_DryRun(t *testing.T) {
	rec := &recorder{}
	cfg := confirmPipeline(rec)
	logger := slogt.New(t)

	err := pipeline.Run(context.Background(), logger, cfg, pipeline.ActionDryRun, nil, false, true, false, false)
	require.NoError(t, err)

	executed := rec.executed()
	assert.Contains(t, executed, "create-env")
	assert.Contains(t, executed, "dangerous-op")
	assert.Contains(t, executed, "verify")
}

// --- Error handling tests ---

func TestRun_StopOnFirstFailure(t *testing.T) {
	rec := &recorder{}
	cfg := pipeline.Config{
		Name:  "failure-test",
		Usage: "Test stop on first failure",
		Steps: []pipeline.Step{
			pipeline.ExecStep("step-a", rec.record("step-a")),
			pipeline.ExecStep("step-b", rec.failAt("step-b")),
			pipeline.ExecStep("step-c", rec.record("step-c")),
		},
	}
	logger := slogt.New(t)

	err := pipeline.Run(context.Background(), logger, cfg, pipeline.ActionExecute, nil, false, true, false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "step step-b")
	assert.Equal(t, []string{"step-a", "step-b"}, rec.executed(), "step-c should not execute after failure")
}

func TestRun_DiffContinuesOnError(t *testing.T) {
	rec := &recorder{}
	cfg := pipeline.Config{
		Name:  "diff-continue-test",
		Usage: "Test diff continue-on-error",
		Steps: []pipeline.Step{
			pipeline.ExecStep("step-a", rec.record("step-a")),
			pipeline.ExecStep("step-b", rec.failAt("step-b")),
			pipeline.ExecStep("step-c", rec.record("step-c")),
		},
	}
	logger := slogt.New(t)

	// Unlike deploy, diff must run every step even after a failure, then
	// report a non-nil error so the process exits non-zero.
	err := pipeline.Run(context.Background(), logger, cfg, pipeline.ActionDryRun, nil, false, true, false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 of 3 step(s) failed")
	assert.Equal(t, []string{"step-a", "step-b", "step-c"}, rec.executed(),
		"diff should run every step even after step-b fails")
}

func TestRun_DiffContinuesOnError_MultipleFailures(t *testing.T) {
	rec := &recorder{}
	cfg := pipeline.Config{
		Name:  "diff-continue-multi-test",
		Usage: "Test diff continue-on-error with multiple failures",
		Steps: []pipeline.Step{
			pipeline.ExecStep("step-a", rec.failAt("step-a")),
			pipeline.ExecStep("step-b", rec.record("step-b")),
			pipeline.ExecStep("step-c", rec.failAt("step-c")),
		},
	}
	logger := slogt.New(t)

	err := pipeline.Run(context.Background(), logger, cfg, pipeline.ActionDryRun, nil, false, true, false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2 of 3 step(s) failed")
	assert.Equal(t, []string{"step-a", "step-b", "step-c"}, rec.executed())
}

func TestRun_NoTargetsNoAll_ReturnsError(t *testing.T) {
	cfg := pipeline.Config{
		Name:  "no-targets",
		Usage: "Test no targets no --all",
		Steps: []pipeline.Step{
			pipeline.ExecStep("step-a", noopFn),
		},
	}
	logger := slogt.New(t)

	err := pipeline.Run(context.Background(), logger, cfg, pipeline.ActionExecute, nil, false, false, false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "specify stack names or --all")
}

func TestRun_UnknownTarget_ReturnsError(t *testing.T) {
	cfg := pipeline.Config{
		Name:  "unknown-target",
		Usage: "Test unknown target",
		Steps: []pipeline.Step{
			pipeline.ExecStep("step-a", noopFn),
		},
	}
	logger := slogt.New(t)

	err := pipeline.Run(context.Background(), logger, cfg, pipeline.ActionExecute,
		[]string{"nonexistent"}, false, false, false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown step")
}

// --- Exclusive mode tests ---

func TestRun_ExclusiveMode(t *testing.T) {
	rec := &recorder{}
	cfg := pipeline.Config{
		Name:  "exclusive-test",
		Usage: "Test exclusive mode",
		Steps: []pipeline.Step{
			pipeline.ExecStep("step-a", rec.record("step-a")),
			pipeline.ExecStep("step-b", rec.record("step-b"),
				pipeline.WithDependsOn("step-a")),
			pipeline.ExecStep("step-c", rec.record("step-c"),
				pipeline.WithDependsOn("step-b")),
		},
	}
	logger := slogt.New(t)

	// Exclusive mode: only run step-b, skip its dependency step-a.
	err := pipeline.Run(context.Background(), logger, cfg, pipeline.ActionExecute,
		[]string{"step-b"}, true, false, false, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"step-b"}, rec.executed())
}

// --- Destroy reverse order tests ---

func TestRun_DestroyReverseOrder(t *testing.T) {
	rec := &recorder{}
	cfg := pipeline.Config{
		Name:  "destroy-test",
		Usage: "Test destroy reverse order",
		Steps: []pipeline.Step{
			pipeline.ExecStep("step-a", rec.record("step-a"),
				pipeline.WithActions(pipeline.NewActionSet(pipeline.ActionDestroy))),
			pipeline.ExecStep("step-b", rec.record("step-b"),
				pipeline.WithDependsOn("step-a"),
				pipeline.WithActions(pipeline.NewActionSet(pipeline.ActionDestroy))),
			pipeline.ExecStep("step-c", rec.record("step-c"),
				pipeline.WithDependsOn("step-b"),
				pipeline.WithActions(pipeline.NewActionSet(pipeline.ActionDestroy))),
		},
	}
	logger := slogt.New(t)

	err := pipeline.Run(context.Background(), logger, cfg, pipeline.ActionDestroy, nil, false, true, false, false)
	require.NoError(t, err)
	// Destroy should reverse: step-c, step-b, step-a.
	assert.Equal(t, []string{"step-c", "step-b", "step-a"}, rec.executed())
}

func TestRun_DestroyTargetResolveDependents(t *testing.T) {
	rec := &recorder{}
	cfg := pipeline.Config{
		Name:  "destroy-target-test",
		Usage: "Test destroy target resolves dependents",
		Steps: []pipeline.Step{
			pipeline.ExecStep("step-a", rec.record("step-a"),
				pipeline.WithActions(pipeline.NewActionSet(pipeline.ActionDestroy))),
			pipeline.ExecStep("step-b", rec.record("step-b"),
				pipeline.WithDependsOn("step-a"),
				pipeline.WithActions(pipeline.NewActionSet(pipeline.ActionDestroy))),
			pipeline.ExecStep("step-c", rec.record("step-c"),
				pipeline.WithDependsOn("step-b"),
				pipeline.WithActions(pipeline.NewActionSet(pipeline.ActionDestroy))),
			pipeline.ExecStep("step-d", rec.record("step-d"),
				pipeline.WithActions(pipeline.NewActionSet(pipeline.ActionDestroy))),
		},
	}
	logger := slogt.New(t)

	// Destroy step-a should also destroy step-b and step-c (dependents), but not step-d.
	err := pipeline.Run(context.Background(), logger, cfg, pipeline.ActionDestroy,
		[]string{"step-a"}, false, false, false, false)
	require.NoError(t, err)
	executed := rec.executed()
	assert.Equal(t, []string{"step-c", "step-b", "step-a"}, executed)
}

// --- Target resolution tests ---

func TestRun_TargetResolvesDependencies(t *testing.T) {
	rec := &recorder{}
	cfg := pipeline.Config{
		Name:  "target-deps-test",
		Usage: "Test target resolves dependencies",
		Steps: []pipeline.Step{
			pipeline.ExecStep("step-a", rec.record("step-a")),
			pipeline.ExecStep("step-b", rec.record("step-b"),
				pipeline.WithDependsOn("step-a")),
			pipeline.ExecStep("step-c", rec.record("step-c"),
				pipeline.WithDependsOn("step-b")),
			pipeline.ExecStep("step-d", rec.record("step-d")),
		},
	}
	logger := slogt.New(t)

	// Targeting step-c should also run step-a and step-b (dependencies), but not step-d.
	err := pipeline.Run(context.Background(), logger, cfg, pipeline.ActionExecute,
		[]string{"step-c"}, false, false, false, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"step-a", "step-b", "step-c"}, rec.executed())
}

// --- show-providers context propagation ---

func TestRun_ShowProvidersPropagatedToStepContext(t *testing.T) {
	for _, show := range []bool{true, false} {
		t.Run(fmt.Sprintf("show=%v", show), func(t *testing.T) {
			var got bool

			cfg := pipeline.Config{
				Name:  "show-providers-test",
				Usage: "Test --show-providers propagation",
				Steps: []pipeline.Step{
					pipeline.ExecStep("step-a", func(ctx context.Context, _ *slog.Logger, _ string, _ pipeline.Action) (*pipeline.StepResult, error) {
						got = pipeline.ShowProviders(ctx)

						return nil, nil
					}),
				},
			}
			logger := slogt.New(t)

			err := pipeline.Run(context.Background(), logger, cfg, pipeline.ActionDryRun, nil, false, true, false, show)
			require.NoError(t, err)
			assert.Equal(t, show, got)
		})
	}
}

// indexOf returns the index of name in the slice, or -1 if not found.
func indexOf(slice []string, name string) int {
	for i, s := range slice {
		if s == name {
			return i
		}
	}

	return -1
}
