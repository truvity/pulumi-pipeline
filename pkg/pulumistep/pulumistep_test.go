package pulumistep

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/truvity/pulumi-pipeline/pkg/pipeline"
)

func logger() *slog.Logger { return slog.New(slog.NewTextHandler(os.Stderr, nil)) }

func TestPulumiStep_DeployPersistsOutputsWithoutSecretsAndVerifiesTheApply(t *testing.T) {
	h := newHarness(t)

	var got map[string]any

	step := PulumiStep(Config{
		WorkDir:   h.workDir,
		StackName: testStack,
		OnOutput: func(_ context.Context, _ *slog.Logger, outputs map[string]any) error {
			got = outputs

			return nil
		},
	})

	result, err := step.Fn(context.Background(), logger(), h.root, pipeline.ActionExecute)
	require.NoError(t, err)

	require.NotNil(t, result)
	assert.Equal(t, 3, result.ChangeSummary["create"], "the stack resource and two components")

	assert.Equal(t, map[string]any{"count": float64(2)}, got, "the secret output must never reach the hook")
}

func TestPulumiStep_DiffReportsChangesAndSyncAndDestroyRun(t *testing.T) {
	h := newHarness(t)
	step := PulumiStep(Config{WorkDir: h.workDir, StackName: testStack})
	ctx := context.Background()

	result, err := step.Fn(ctx, logger(), h.root, pipeline.ActionDryRun)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 3, result.ChangeSummary["create"])

	_, err = step.Fn(ctx, logger(), h.root, pipeline.ActionExecute)
	require.NoError(t, err)

	// A converged stack previews as unchanged, which is what
	// --expect-no-changes relies on.
	result, err = step.Fn(ctx, logger(), h.root, pipeline.ActionDryRun)
	require.NoError(t, err)

	if result != nil {
		assert.Empty(t, result.ChangeSummary["create"]+result.ChangeSummary["update"]+result.ChangeSummary["delete"])
	}

	_, err = step.Fn(ctx, logger(), h.root, pipeline.ActionSync)
	require.NoError(t, err)

	_, err = step.Fn(ctx, logger(), h.root, pipeline.ActionDestroy)
	require.NoError(t, err)
}

func TestPulumiStep_ExpectNoChangesThroughTheEngine(t *testing.T) {
	h := newHarness(t)
	step := PulumiStep(Config{WorkDir: h.workDir, StackName: testStack})
	cfg := pipeline.Config{Name: "t", Root: h.root, Steps: []pipeline.Step{step}}
	ctx := context.Background()

	_, err := step.Fn(ctx, logger(), h.root, pipeline.ActionExecute)
	require.NoError(t, err)

	// Converged: the gate passes.
	require.NoError(t, pipeline.RunWith(ctx, logger(), cfg, pipeline.ActionDryRun, nil,
		pipeline.RunOptions{All: true, ExpectNoChanges: true}))

	// The program now wants a third component: the gate fails.
	h.setCount(t, 3)

	err = pipeline.RunWith(ctx, logger(), cfg, pipeline.ActionDryRun, nil,
		pipeline.RunOptions{All: true, ExpectNoChanges: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 of 1 step(s) failed")

	// Same change, no flag: an ordinary diff.
	require.NoError(t, pipeline.RunWith(ctx, logger(), cfg, pipeline.ActionDryRun, nil, pipeline.RunOptions{All: true}))
}

func TestVerifyAfterApply_PassesWhenConverged(t *testing.T) {
	h := newHarness(t)
	step := PulumiStep(Config{WorkDir: h.workDir, StackName: testStack})

	_, err := step.Fn(context.Background(), logger(), h.root, pipeline.ActionExecute)
	require.NoError(t, err)

	require.NoError(t, verifyAfterApply(context.Background(), logger(), h.stack(t), testStack))
}

func TestVerifyAfterApply_FailsWhenTheRefreshPreviewPlansADelete(t *testing.T) {
	h := newHarness(t)
	step := PulumiStep(Config{WorkDir: h.workDir, StackName: testStack})

	_, err := step.Fn(context.Background(), logger(), h.root, pipeline.ActionExecute)
	require.NoError(t, err)

	// The program stops declaring a component the state still holds: exactly
	// what a preview that wants to destroy something looks like.
	h.setCount(t, 1)

	err = verifyAfterApply(context.Background(), logger(), h.stack(t), testStack)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plans to destroy: delete=1")
}

func TestCheckStacks_ReportsAHealthyStack(t *testing.T) {
	h := newHarness(t)
	step := PulumiStep(Config{WorkDir: h.workDir, StackName: testStack})

	_, err := step.Fn(context.Background(), logger(), h.root, pipeline.ActionExecute)
	require.NoError(t, err)

	cfg := pipeline.Config{Name: "t", Root: h.root, Steps: []pipeline.Step{step}}
	require.NoError(t, CheckStacks(context.Background(), logger(), cfg))
}

func TestDestructiveChanges(t *testing.T) {
	assert.Nil(t, destructiveChanges(nil))
	assert.Nil(t, destructiveChanges(map[apitype.OpType]int{apitype.OpSame: 9, apitype.OpUpdate: 2, apitype.OpCreate: 1}))
	assert.Equal(t, []string{"create-replacement=1", "delete-replaced=1", "delete=2", "replace=1"},
		destructiveChanges(map[apitype.OpType]int{
			apitype.OpDelete: 2, apitype.OpReplace: 1, apitype.OpCreateReplacement: 1, apitype.OpDeleteReplaced: 1, apitype.OpSame: 4,
		}))
}
