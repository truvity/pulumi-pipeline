package pipeline_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/truvity/pulumi-pipeline/pkg/pipeline"
)

var (
	allActions = []pipeline.Action{
		pipeline.ActionDryRun,
		pipeline.ActionExecute,
		pipeline.ActionSync,
		pipeline.ActionDestroy,
	}
)

// genActionSubset generates a random subset of all Action values.
func genActionSubset() *rapid.Generator[[]pipeline.Action] {
	return rapid.Custom(func(t *rapid.T) []pipeline.Action {
		var subset []pipeline.Action

		for _, a := range allActions {
			if rapid.Bool().Draw(t, a.String()) {
				subset = append(subset, a)
			}
		}

		return subset
	})
}

// Property 5: ActionSet membership round-trip.
// For any subset of Actions passed to NewActionSet, Supports returns true
// for exactly those Actions and false for all others.
func TestProperty5_ActionSetMembershipRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		subset := genActionSubset().Draw(t, "actions")
		set := pipeline.NewActionSet(subset...)

		included := make(map[pipeline.Action]bool)
		for _, a := range subset {
			included[a] = true
		}

		for _, a := range allActions {
			if included[a] {
				assert.True(t, set.Supports(a), "ActionSet should support %s", a)
			} else {
				assert.False(t, set.Supports(a), "ActionSet should not support %s", a)
			}
		}
	})
}

// Property 20: Backward compatibility aliases.
// ActionPreview == ActionDryRun, ActionUp == ActionExecute, ActionRefresh == ActionSync.
func TestProperty20_BackwardCompatibilityAliases(t *testing.T) {
	require.Equal(t, pipeline.ActionDryRun, pipeline.ActionPreview, "ActionPreview must equal ActionDryRun")
	require.Equal(t, pipeline.ActionExecute, pipeline.ActionUp, "ActionUp must equal ActionExecute")
	require.Equal(t, pipeline.ActionSync, pipeline.ActionRefresh, "ActionRefresh must equal ActionSync")
}

// Unit tests for Action.String().
func TestActionString(t *testing.T) {
	tests := []struct {
		action pipeline.Action
		want   string
	}{
		{pipeline.ActionDryRun, "dry-run"},
		{pipeline.ActionExecute, "execute"},
		{pipeline.ActionSync, "sync"},
		{pipeline.ActionDestroy, "destroy"},
		{pipeline.Action(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.action.String())
		})
	}
}

// Unit tests for StepKind.String().
func TestStepKindString(t *testing.T) {
	tests := []struct {
		kind pipeline.StepKind
		want string
	}{
		{pipeline.StepPulumi, "pulumi"},
		{pipeline.StepExec, "exec"},
		{pipeline.StepConfirm, "confirm"},
		{pipeline.StepKind(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.kind.String())
		})
	}
}

// Unit test for AllActions constant.
func TestAllActions(t *testing.T) {
	for _, a := range allActions {
		assert.True(t, pipeline.AllActions().Supports(a), "AllActions should support %s", a)
	}
}
