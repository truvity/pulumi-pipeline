package pipeline_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/truvity/pulumi-pipeline/pkg/pipeline"
)

type (
	// testKindOutput simulates KindOutput — produced by kind.Create.
	testKindOutput struct {
		Kubeconfig string
		TempDir    string
	}

	// testClusterAccess simulates ClusterAccess — produced by a cluster/extract step.
	testClusterAccess struct {
		PrimaryKubeconfig string
		DirectKubeconfig  string
	}
)

func TestTypedProduce_SetsDeferred(t *testing.T) {
	produced := &testKindOutput{Kubeconfig: "/tmp/kubeconfig", TempDir: "/tmp/dir"}

	kindOut, step := pipeline.TypedProduce("kind/create",
		func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action) (*testKindOutput, *pipeline.StepResult, error) {
			return produced, nil, nil
		},
		nil, // no resolve
	)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	_, err := step.Fn(context.Background(), logger, "/tmp", pipeline.ActionExecute)
	require.NoError(t, err)

	// Deferred should be resolved after step execution.
	assert.NotPanics(t, func() {
		got := kindOut.Get()
		assert.Equal(t, "/tmp/kubeconfig", got.Kubeconfig)
		assert.Equal(t, "/tmp/dir", got.TempDir)
	})
}

func TestTypedConsume_ReceivesInput(t *testing.T) {
	// Simulate a resolved deferred from a prior step.
	kindOut, kindStep := pipeline.TypedProduce("kind/create",
		func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action) (*testKindOutput, *pipeline.StepResult, error) {
			return &testKindOutput{Kubeconfig: "/tmp/kubeconfig"}, nil, nil
		},
		nil,
	)

	var received string

	_, consumeStep := pipeline.TypedConsume("access/install",
		func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action, kind *testKindOutput) (*pipeline.StepResult, error) {
			received = kind.Kubeconfig
			return nil, nil
		},
		kindOut,
	)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// Execute producer first.
	_, err := kindStep.Fn(context.Background(), logger, "/tmp", pipeline.ActionExecute)
	require.NoError(t, err)

	// Execute consumer — should receive the produced value.
	_, err = consumeStep.Fn(context.Background(), logger, "/tmp", pipeline.ActionExecute)
	require.NoError(t, err)
	assert.Equal(t, "/tmp/kubeconfig", received)
}

func TestTypedTransform_ProducesAndConsumes(t *testing.T) {
	kindOut, kindStep := pipeline.TypedProduce("kind/create",
		func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action) (*testKindOutput, *pipeline.StepResult, error) {
			return &testKindOutput{Kubeconfig: "/tmp/bootstrap.kubeconfig"}, nil, nil
		},
		nil,
	)

	cluster, transformStep := pipeline.TypedTransform("cluster/extract",
		func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action, _ *testKindOutput) (*testClusterAccess, *pipeline.StepResult, error) {
			return &testClusterAccess{
				PrimaryKubeconfig: "/tmp/primary.kubeconfig",
				DirectKubeconfig:  "/tmp/direct.kubeconfig",
			}, nil, nil
		},
		nil, // no resolve
		kindOut,
	)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	_, err := kindStep.Fn(context.Background(), logger, "/tmp", pipeline.ActionExecute)
	require.NoError(t, err)
	_, err = transformStep.Fn(context.Background(), logger, "/tmp", pipeline.ActionExecute)
	require.NoError(t, err)

	assert.NotPanics(t, func() {
		got := cluster.Get()
		assert.Equal(t, "/tmp/primary.kubeconfig", got.PrimaryKubeconfig)
	})
}

func TestTypedConsume2_TwoInputs(t *testing.T) {
	kindOut, kindStep := pipeline.TypedProduce("kind/create",
		func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action) (*testKindOutput, *pipeline.StepResult, error) {
			return &testKindOutput{Kubeconfig: "/tmp/bootstrap"}, nil, nil
		},
		nil,
	)

	cluster, clusterStep := pipeline.TypedProduce("cluster/extract",
		func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action) (*testClusterAccess, *pipeline.StepResult, error) {
			return &testClusterAccess{PrimaryKubeconfig: "/tmp/primary"}, nil, nil
		},
		nil,
	)

	var gotKind, gotCluster string

	_, pivotStep := pipeline.TypedConsume2("pivot/move",
		func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action, k *testKindOutput, c *testClusterAccess) (*pipeline.StepResult, error) {
			gotKind = k.Kubeconfig
			gotCluster = c.PrimaryKubeconfig
			return nil, nil
		},
		kindOut, cluster,
	)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	_, err := kindStep.Fn(context.Background(), logger, "/tmp", pipeline.ActionExecute)
	require.NoError(t, err)
	_, err = clusterStep.Fn(context.Background(), logger, "/tmp", pipeline.ActionExecute)
	require.NoError(t, err)
	_, err = pivotStep.Fn(context.Background(), logger, "/tmp", pipeline.ActionExecute)
	require.NoError(t, err)

	assert.Equal(t, "/tmp/bootstrap", gotKind)
	assert.Equal(t, "/tmp/primary", gotCluster)
}

func TestTypedRun_OrderingToken(t *testing.T) {
	var ran bool

	token, step := pipeline.TypedRun("generate/overlays",
		func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action) (*pipeline.StepResult, error) {
			ran = true
			return nil, nil
		},
	)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	_, err := step.Fn(context.Background(), logger, "/tmp", pipeline.ActionExecute)
	require.NoError(t, err)

	assert.True(t, ran)
	assert.NotPanics(t, func() { token.Get() })
}

func TestDeferred_PanicsWhenUnresolved(t *testing.T) {
	kindOut, _ := pipeline.TypedProduce("kind/create",
		func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action) (*testKindOutput, *pipeline.StepResult, error) {
			return &testKindOutput{}, nil, nil
		},
		nil,
	)

	// Don't execute the step — deferred is unresolved.
	assert.Panics(t, func() { kindOut.Get() })
}

func TestDependsOn_DerivedFromDeferreds(t *testing.T) {
	overlays, overlaysStep := pipeline.TypedRun("generate/overlays",
		func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action) (*pipeline.StepResult, error) {
			return nil, nil
		},
	)

	kindOut, kindStep := pipeline.TypedProduce("bootstrap/local",
		func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action) (*testKindOutput, *pipeline.StepResult, error) {
			return &testKindOutput{}, nil, nil
		},
		nil,
		overlays, // ordering dependency
	)

	_, consumeStep := pipeline.TypedConsume("access/install",
		func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action, _ *testKindOutput) (*pipeline.StepResult, error) {
			return nil, nil
		},
		kindOut, // data dependency
	)

	// Verify DependsOn was derived from deferred connections.
	assert.Empty(t, overlaysStep.DependsOn)
	assert.Equal(t, []string{"generate/overlays"}, kindStep.DependsOn)
	assert.Equal(t, []string{"bootstrap/local"}, consumeStep.DependsOn)
}

func TestDAG_WithTypedSteps(t *testing.T) {
	overlays, overlaysStep := pipeline.TypedRun("generate/overlays",
		func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action) (*pipeline.StepResult, error) {
			return nil, nil
		},
	)

	kindOut, kindStep := pipeline.TypedProduce("bootstrap/local",
		func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action) (*testKindOutput, *pipeline.StepResult, error) {
			return &testKindOutput{Kubeconfig: "/tmp/kubeconfig"}, nil, nil
		},
		nil,
		overlays,
	)

	_, consumeStep := pipeline.TypedConsume("access/install",
		func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action, _ *testKindOutput) (*pipeline.StepResult, error) {
			return nil, nil
		},
		kindOut,
	)

	steps := []pipeline.Step{overlaysStep, kindStep, consumeStep}

	// DAG should be valid.
	require.NoError(t, pipeline.ValidateDAG(steps))

	// Topological sort should respect dependencies.
	sorted, err := pipeline.TopologicalSort(steps)
	require.NoError(t, err)
	require.Len(t, sorted, 3)
	assert.Equal(t, "generate/overlays", sorted[0].Name)
	assert.Equal(t, "bootstrap/local", sorted[1].Name)
	assert.Equal(t, "access/install", sorted[2].Name)
}

func TestResolve_FallbackForSkippedSteps(t *testing.T) {
	kindOut, _ := pipeline.TypedProduce("kind/create",
		func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action) (*testKindOutput, *pipeline.StepResult, error) {
			return nil, nil, fmt.Errorf("should not be called")
		},
		func(_ context.Context, _ *slog.Logger, _ string) (*testKindOutput, error) {
			return &testKindOutput{Kubeconfig: "/resolved/kubeconfig"}, nil
		},
	)

	// Don't execute the step — simulate --exclusive mode.
	// Resolve from existing state.
	require.NoError(t, kindOut.ResolveFromState(context.Background(), slog.New(slog.NewTextHandler(os.Stderr, nil)), "/tmp"))

	assert.NotPanics(t, func() {
		got := kindOut.Get()
		assert.Equal(t, "/resolved/kubeconfig", got.Kubeconfig)
	})
}

func TestResolve_ErrorWhenNoResolveFunc(t *testing.T) {
	kindOut, _ := pipeline.TypedProduce("kind/create",
		func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action) (*testKindOutput, *pipeline.StepResult, error) {
			return &testKindOutput{}, nil, nil
		},
		nil, // no resolve function
	)

	err := kindOut.ResolveFromState(context.Background(), slog.New(slog.NewTextHandler(os.Stderr, nil)), "/tmp")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no resolve function")
}
