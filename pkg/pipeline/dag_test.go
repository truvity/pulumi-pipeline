package pipeline_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/truvity/pulumi-pipeline/pkg/pipeline"
)

var (
	// noopFn is a StepFunc that does nothing.
	noopFn pipeline.StepFunc = func(_ context.Context, _ *slog.Logger, _ string, _ pipeline.Action) (*pipeline.StepResult, error) {
		return nil, nil
	}
)

// makeStep creates a simple ExecStep with the given name and dependencies.
func makeStep(name string, deps ...string) pipeline.Step {
	return pipeline.Step{
		Name:      name,
		Kind:      pipeline.StepExec,
		Fn:        noopFn,
		Actions:   pipeline.DefaultExecActions(),
		DependsOn: deps,
	}
}

// --- ValidateDAG unit tests ---

func TestValidateDAG_DuplicateNames(t *testing.T) {
	steps := []pipeline.Step{
		makeStep("a"),
		makeStep("a"),
	}

	err := pipeline.ValidateDAG(steps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate step name")
}

func TestValidateDAG_MissingDependency(t *testing.T) {
	steps := []pipeline.Step{
		makeStep("a", "nonexistent"),
	}

	err := pipeline.ValidateDAG(steps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-existent step")
}

func TestValidateDAG_Cycle(t *testing.T) {
	steps := []pipeline.Step{
		makeStep("a", "b"),
		makeStep("b", "a"),
	}

	err := pipeline.ValidateDAG(steps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
}

func TestValidateDAG_Valid(t *testing.T) {
	steps := []pipeline.Step{
		makeStep("a"),
		makeStep("b", "a"),
		makeStep("c", "a", "b"),
	}

	require.NoError(t, pipeline.ValidateDAG(steps))
}

// --- Property 1: DAG validation rejects invalid graphs ---

// genValidDAGSteps generates a random valid DAG (unique names, valid deps, no cycles).
func genValidDAGSteps() *rapid.Generator[[]pipeline.Step] {
	return genValidDAGStepsMin(1)
}

// genValidDAGStepsMin is genValidDAGSteps with a floor on the number of
// steps. A property that needs two steps (to make a duplicate, say) asks
// for two: the alternative was drawing one and SKIPPING it, which threw
// away a share of every run's cases for nothing.
func genValidDAGStepsMin(minSteps int) *rapid.Generator[[]pipeline.Step] {
	return rapid.Custom(func(t *rapid.T) []pipeline.Step {
		n := rapid.IntRange(minSteps, 8).Draw(t, "numSteps")
		steps := make([]pipeline.Step, n)

		for i := range n {
			name := rapid.StringMatching(`[a-z]{2,6}`).Draw(t, "name")
			// Ensure unique names by appending index.
			name = fmt.Sprintf("%s-%d", name, i)

			// Dependencies can only reference earlier steps (guarantees acyclicity).
			var deps []string

			for j := range i {
				if rapid.Bool().Draw(t, "dep") {
					deps = append(deps, steps[j].Name)
				}
			}

			steps[i] = makeStep(name, deps...)
		}

		return steps
	})
}

func TestProperty1_DAGValidationRejectsInvalidGraphs(t *testing.T) {
	// Valid DAGs should pass.
	rapid.Check(t, func(t *rapid.T) {
		steps := genValidDAGSteps().Draw(t, "steps")
		assert.NoError(t, pipeline.ValidateDAG(steps))
	})
}

func TestProperty1_DAGValidationRejectsDuplicates(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		steps := genValidDAGStepsMin(2).Draw(t, "steps")

		// Introduce a duplicate name.
		dupIdx := rapid.IntRange(1, len(steps)-1).Draw(t, "dupIdx")
		steps[dupIdx].Name = steps[0].Name

		assert.Error(t, pipeline.ValidateDAG(steps))
	})
}

// --- Property 2: Topological sort ordering invariant ---

func TestProperty2_TopologicalSortOrderingInvariant(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		steps := genValidDAGSteps().Draw(t, "steps")

		sorted, err := pipeline.TopologicalSort(steps)
		require.NoError(t, err)

		// Build index map for sorted result.
		idxMap := make(map[string]int, len(sorted))
		for i := range sorted {
			idxMap[sorted[i].Name] = i
		}

		// Verify: for every step, all DependsOn entries appear at earlier indices.
		for i := range sorted {
			for _, dep := range sorted[i].DependsOn {
				depIdx, ok := idxMap[dep]
				require.True(t, ok, "dependency %q not found in sorted output", dep)
				assert.Less(t, depIdx, i,
					"dependency %q (idx %d) should appear before %q (idx %d)",
					dep, depIdx, sorted[i].Name, i)
			}
		}
	})
}

// --- Property 3: Linear chain fallback preserves slice order ---

func TestProperty3_LinearChainFallbackPreservesOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 10).Draw(t, "numSteps")
		steps := make([]pipeline.Step, n)

		for i := range n {
			steps[i] = makeStep(fmt.Sprintf("step-%d", i))
		}

		sorted, err := pipeline.TopologicalSort(steps)
		require.NoError(t, err)
		require.Len(t, sorted, len(steps))

		// Output order must equal input order.
		for i := range steps {
			assert.Equal(t, steps[i].Name, sorted[i].Name,
				"step at index %d should be %q but got %q", i, steps[i].Name, sorted[i].Name)
		}
	})
}

// --- Property 4: Subgraph completeness and ordering ---

func TestProperty4_SubgraphCompletenessAndOrdering(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		steps := genValidDAGSteps().Draw(t, "steps")

		// Pick a random target.
		targetIdx := rapid.IntRange(0, len(steps)-1).Draw(t, "targetIdx")
		target := steps[targetIdx].Name

		subgraph, err := pipeline.ResolveSubgraph(steps, target)
		require.NoError(t, err)

		// Target must be the last element.
		assert.Equal(t, target, subgraph[len(subgraph)-1].Name,
			"target %q should be last in subgraph", target)

		// Collect all transitive deps.
		nameToStep := make(map[string]*pipeline.Step, len(steps))
		for i := range steps {
			nameToStep[steps[i].Name] = &steps[i]
		}

		expected := make(map[string]bool)

		var collectDeps func(name string)
		collectDeps = func(name string) {
			if expected[name] {
				return
			}

			expected[name] = true

			s := nameToStep[name]
			for _, dep := range s.DependsOn {
				collectDeps(dep)
			}
		}

		collectDeps(target)

		// Subgraph must contain exactly the expected steps.
		subgraphNames := make(map[string]bool, len(subgraph))
		for i := range subgraph {
			subgraphNames[subgraph[i].Name] = true
		}

		assert.Equal(t, expected, subgraphNames,
			"subgraph should contain exactly transitive deps + target")

		// Verify topological ordering within subgraph.
		idxMap := make(map[string]int, len(subgraph))
		for i := range subgraph {
			idxMap[subgraph[i].Name] = i
		}

		for i := range subgraph {
			for _, dep := range subgraph[i].DependsOn {
				if _, ok := idxMap[dep]; ok {
					assert.Less(t, idxMap[dep], i,
						"dependency %q should appear before %q in subgraph",
						dep, subgraph[i].Name)
				}
			}
		}
	})
}

// --- Property 19: Immutability of inputs ---

func TestProperty19_ImmutabilityOfInputs(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		steps := genValidDAGSteps().Draw(t, "steps")

		// Deep copy for comparison.
		original := make([]pipeline.Step, len(steps))
		for i := range steps {
			original[i] = steps[i]
			if steps[i].DependsOn != nil {
				original[i].DependsOn = make([]string, len(steps[i].DependsOn))
				copy(original[i].DependsOn, steps[i].DependsOn)
			}
		}

		// Call ValidateDAG.
		_ = pipeline.ValidateDAG(steps)

		// Verify no mutation.
		require.Len(t, steps, len(original))

		for i := range steps {
			assert.Equal(t, original[i].Name, steps[i].Name, "step %d name mutated", i)
			assert.Equal(t, original[i].DependsOn, steps[i].DependsOn, "step %d DependsOn mutated", i)
		}

		// Call TopologicalSort.
		_, _ = pipeline.TopologicalSort(steps)

		// Verify no mutation again.
		for i := range steps {
			assert.Equal(t, original[i].Name, steps[i].Name, "step %d name mutated after TopologicalSort", i)
			assert.Equal(t, original[i].DependsOn, steps[i].DependsOn, "step %d DependsOn mutated after TopologicalSort", i)
		}
	})
}

// --- ResolveDependents unit tests ---

func TestResolveDependents_SingleTarget(t *testing.T) {
	steps := []pipeline.Step{
		makeStep("a"),
		makeStep("b", "a"),
		makeStep("c", "b"),
		makeStep("d"),
	}

	result, err := pipeline.ResolveDependents(steps, "a")
	require.NoError(t, err)

	names := stepNames(result)
	// Reverse topological: dependents first, target last.
	assert.Equal(t, []string{"c", "b", "a"}, names)
}

func TestResolveDependents_LeafNode(t *testing.T) {
	steps := []pipeline.Step{
		makeStep("a"),
		makeStep("b", "a"),
		makeStep("c", "b"),
	}

	result, err := pipeline.ResolveDependents(steps, "c")
	require.NoError(t, err)

	names := stepNames(result)
	// c has no dependents, so only c itself.
	assert.Equal(t, []string{"c"}, names)
}

func TestResolveDependents_UnknownTarget(t *testing.T) {
	steps := []pipeline.Step{
		makeStep("a"),
	}

	_, err := pipeline.ResolveDependents(steps, "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown step")
}

// --- ResolveMultiSubgraph unit tests ---

func TestResolveMultiSubgraph_MultipleTargets(t *testing.T) {
	steps := []pipeline.Step{
		makeStep("a"),
		makeStep("b", "a"),
		makeStep("c"),
		makeStep("d", "c"),
		makeStep("e"),
	}

	result, err := pipeline.ResolveMultiSubgraph(steps, []string{"b", "d"})
	require.NoError(t, err)

	names := stepNames(result)
	// Should include a, b (deps of b) and c, d (deps of d), but not e.
	assert.Len(t, names, 4)
	assert.Contains(t, names, "a")
	assert.Contains(t, names, "b")
	assert.Contains(t, names, "c")
	assert.Contains(t, names, "d")
	assert.NotContains(t, names, "e")

	// Verify topological order.
	assertTopologicalOrder(t, result)
}

func TestResolveMultiSubgraph_OverlappingDeps(t *testing.T) {
	steps := []pipeline.Step{
		makeStep("a"),
		makeStep("b", "a"),
		makeStep("c", "a"),
	}

	result, err := pipeline.ResolveMultiSubgraph(steps, []string{"b", "c"})
	require.NoError(t, err)

	names := stepNames(result)
	// a is shared dep, should appear once.
	assert.Len(t, names, 3)
	assert.Contains(t, names, "a")
	assert.Contains(t, names, "b")
	assert.Contains(t, names, "c")
}

// --- ResolveMultiDependents unit tests ---

func TestResolveMultiDependents_MultipleTargets(t *testing.T) {
	steps := []pipeline.Step{
		makeStep("a"),
		makeStep("b", "a"),
		makeStep("c", "b"),
		makeStep("d"),
		makeStep("e", "d"),
	}

	result, err := pipeline.ResolveMultiDependents(steps, []string{"a", "d"})
	require.NoError(t, err)

	names := stepNames(result)
	// a's dependents: b, c. d's dependents: e. All in reverse topological order.
	assert.Len(t, names, 5)
	assert.Contains(t, names, "a")
	assert.Contains(t, names, "b")
	assert.Contains(t, names, "c")
	assert.Contains(t, names, "d")
	assert.Contains(t, names, "e")

	// Verify reverse topological order: dependents before their dependencies.
	idxMap := make(map[string]int, len(result))
	for i := range result {
		idxMap[result[i].Name] = i
	}

	// c should come before b, b before a (reverse order).
	assert.Less(t, idxMap["c"], idxMap["b"], "c should come before b in reverse order")
	assert.Less(t, idxMap["b"], idxMap["a"], "b should come before a in reverse order")
	assert.Less(t, idxMap["e"], idxMap["d"], "e should come before d in reverse order")
}

// --- Property: ResolveDependents always includes target ---

func TestProperty_ResolveDependentsIncludesTarget(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		steps := genValidDAGSteps().Draw(t, "steps")

		targetIdx := rapid.IntRange(0, len(steps)-1).Draw(t, "targetIdx")
		target := steps[targetIdx].Name

		result, err := pipeline.ResolveDependents(steps, target)
		require.NoError(t, err)

		// Target must always be the last element.
		assert.Equal(t, target, result[len(result)-1].Name,
			"target %q should be last in ResolveDependents result", target)
	})
}

// --- Property: ResolveMultiSubgraph union is correct and deduplicated ---

func TestProperty_ResolveMultiSubgraphUnion(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		steps := genValidDAGSteps().Draw(t, "steps")

		// Pick 1-3 random targets.
		numTargets := rapid.IntRange(1, min(3, len(steps))).Draw(t, "numTargets")
		targetSet := make(map[string]bool)
		targets := make([]string, 0, numTargets)

		for len(targets) < numTargets {
			idx := rapid.IntRange(0, len(steps)-1).Draw(t, "targetIdx")
			name := steps[idx].Name

			if !targetSet[name] {
				targetSet[name] = true
				targets = append(targets, name)
			}
		}

		result, err := pipeline.ResolveMultiSubgraph(steps, targets)
		require.NoError(t, err)

		// No duplicates.
		seen := make(map[string]bool, len(result))
		for i := range result {
			assert.False(t, seen[result[i].Name], "duplicate step %q in result", result[i].Name)
			seen[result[i].Name] = true
		}

		// All targets must be in the result.
		for _, target := range targets {
			assert.True(t, seen[target], "target %q should be in result", target)
		}

		// Verify topological order.
		idxMap := make(map[string]int, len(result))
		for i := range result {
			idxMap[result[i].Name] = i
		}

		for i := range result {
			for _, dep := range result[i].DependsOn {
				if depIdx, ok := idxMap[dep]; ok {
					assert.Less(t, depIdx, i,
						"dependency %q should appear before %q in subgraph",
						dep, result[i].Name)
				}
			}
		}
	})
}

// --- Property: ResolveMultiDependents union is correct in reverse order ---

func TestProperty_ResolveMultiDependentsUnion(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		steps := genValidDAGSteps().Draw(t, "steps")

		numTargets := rapid.IntRange(1, min(3, len(steps))).Draw(t, "numTargets")
		targetSet := make(map[string]bool)
		targets := make([]string, 0, numTargets)

		for len(targets) < numTargets {
			idx := rapid.IntRange(0, len(steps)-1).Draw(t, "targetIdx")
			name := steps[idx].Name

			if !targetSet[name] {
				targetSet[name] = true
				targets = append(targets, name)
			}
		}

		result, err := pipeline.ResolveMultiDependents(steps, targets)
		require.NoError(t, err)

		// No duplicates.
		seen := make(map[string]bool, len(result))
		for i := range result {
			assert.False(t, seen[result[i].Name], "duplicate step %q in result", result[i].Name)
			seen[result[i].Name] = true
		}

		// All targets must be in the result.
		for _, target := range targets {
			assert.True(t, seen[target], "target %q should be in result", target)
		}

		// Verify reverse topological order: for each step, its DependsOn entries
		// that are in the result should appear AFTER it (reverse).
		idxMap := make(map[string]int, len(result))
		for i := range result {
			idxMap[result[i].Name] = i
		}

		for i := range result {
			for _, dep := range result[i].DependsOn {
				if depIdx, ok := idxMap[dep]; ok {
					assert.Greater(t, depIdx, i,
						"in reverse order, dependency %q (idx %d) should appear after %q (idx %d)",
						dep, depIdx, result[i].Name, i)
				}
			}
		}
	})
}

// --- Helpers ---

func stepNames(steps []pipeline.Step) []string {
	names := make([]string, len(steps))
	for i := range steps {
		names[i] = steps[i].Name
	}

	return names
}

func assertTopologicalOrder(t *testing.T, steps []pipeline.Step) {
	t.Helper()

	idxMap := make(map[string]int, len(steps))
	for i := range steps {
		idxMap[steps[i].Name] = i
	}

	for i := range steps {
		for _, dep := range steps[i].DependsOn {
			if depIdx, ok := idxMap[dep]; ok {
				assert.Less(t, depIdx, i,
					"dependency %q (idx %d) should appear before %q (idx %d)",
					dep, depIdx, steps[i].Name, i)
			}
		}
	}
}
