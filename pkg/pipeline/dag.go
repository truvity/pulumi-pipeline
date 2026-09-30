package pipeline

import (
	"fmt"
)

// ValidateDAG checks that the step dependency graph is valid.
// It returns an error if any of the following are detected:
//   - duplicate step names
//   - DependsOn references to non-existent step names
//   - dependency cycles
//
// It does not modify the input steps slice.
func ValidateDAG(steps []Step) error {
	names := make(map[string]bool, len(steps))

	// Check for duplicate names.
	for i := range steps {
		if names[steps[i].Name] {
			return fmt.Errorf("duplicate step name %q", steps[i].Name)
		}

		names[steps[i].Name] = true
	}

	// Check for missing dependency targets.
	for i := range steps {
		for _, dep := range steps[i].DependsOn {
			if !names[dep] {
				return fmt.Errorf("step %q depends on non-existent step %q", steps[i].Name, dep)
			}
		}
	}

	// Check for cycles using DFS with coloring.
	// white=0 (unvisited), gray=1 (in progress), black=2 (done).
	const (
		white = 0
		gray  = 1
		black = 2
	)

	color := make(map[string]int, len(steps))

	// Build name-to-index map for O(1) lookup in visit.
	nameToIdx := make(map[string]int, len(steps))

	for i := range steps {
		nameToIdx[steps[i].Name] = i
	}

	var visit func(name string) error
	visit = func(name string) error {
		color[name] = gray

		idx := nameToIdx[name]
		for _, dep := range steps[idx].DependsOn {
			switch color[dep] {
			case gray:
				return fmt.Errorf("dependency cycle detected involving step %q", dep)
			case white:
				if err := visit(dep); err != nil {
					return err
				}
			}
		}

		color[name] = black

		return nil
	}

	for i := range steps {
		if color[steps[i].Name] == white {
			if err := visit(steps[i].Name); err != nil {
				return err
			}
		}
	}

	return nil
}

// TopologicalSort returns steps ordered so that every dependency appears before
// its dependent. It uses Kahn's algorithm for a stable topological sort.
//
// Linear chain fallback: when no step has explicit DependsOn, the original
// slice order is returned unchanged.
//
// Stable sort: steps at the same dependency level maintain their original
// relative order from the input slice.
func TopologicalSort(steps []Step) ([]Step, error) {
	if err := ValidateDAG(steps); err != nil {
		return nil, err
	}

	// Linear chain fallback: if no step has explicit DependsOn, return original order.
	hasExplicitDeps := false

	for i := range steps {
		if len(steps[i].DependsOn) > 0 {
			hasExplicitDeps = true

			break
		}
	}

	if !hasExplicitDeps {
		result := make([]Step, len(steps))
		copy(result, steps)

		return result, nil
	}

	// Build index and in-degree map.
	nameToIdx := make(map[string]int, len(steps))

	for i := range steps {
		nameToIdx[steps[i].Name] = i
	}

	inDegree := make([]int, len(steps))

	for i := range steps {
		inDegree[i] = len(steps[i].DependsOn)
	}

	// Initialize queue with steps that have no dependencies (in original order for stability).
	queue := make([]int, 0, len(steps))

	for i := range steps {
		if inDegree[i] == 0 {
			queue = append(queue, i)
		}
	}

	// Build adjacency list: dep -> list of dependents.
	dependents := make(map[int][]int, len(steps))

	for i := range steps {
		for _, dep := range steps[i].DependsOn {
			depIdx := nameToIdx[dep]
			dependents[depIdx] = append(dependents[depIdx], i)
		}
	}

	result := make([]Step, 0, len(steps))

	for len(queue) > 0 {
		// Pop front (FIFO for stability).
		idx := queue[0]
		queue = queue[1:]
		result = append(result, steps[idx])

		// Collect newly-ready dependents, then sort by original index for stability.
		var ready []int

		for _, depIdx := range dependents[idx] {
			inDegree[depIdx]--

			if inDegree[depIdx] == 0 {
				ready = append(ready, depIdx)
			}
		}

		// Insert ready items in original-order position to maintain stability.
		for _, r := range ready {
			inserted := false

			for j := range queue {
				if r >= queue[j] {
					continue
				}

				queue = append(queue, 0)
				copy(queue[j+1:], queue[j:])
				queue[j] = r
				inserted = true

				break
			}

			if !inserted {
				queue = append(queue, r)
			}
		}
	}

	return result, nil
}

// ResolveDependents returns the target step plus all steps that transitively
// depend on it (direct and indirect dependents). The result is in reverse
// topological order: dependents are destroyed first, then the target itself
// appears as the LAST element.
func ResolveDependents(steps []Step, target string) ([]Step, error) {
	if err := ValidateDAG(steps); err != nil {
		return nil, err
	}

	// Build name-to-step index.
	nameToStep := make(map[string]*Step, len(steps))

	for i := range steps {
		nameToStep[steps[i].Name] = &steps[i]
	}

	if _, ok := nameToStep[target]; !ok {
		return nil, fmt.Errorf("unknown step %q (available: %s)", target, StepNames(steps))
	}

	// Build reverse adjacency: step -> list of steps that depend on it.
	reverseDeps := make(map[string][]string, len(steps))

	for i := range steps {
		for _, dep := range steps[i].DependsOn {
			reverseDeps[dep] = append(reverseDeps[dep], steps[i].Name)
		}
	}

	// Collect transitive dependents via DFS on reverse edges.
	visited := make(map[string]bool)

	var collect func(name string)
	collect = func(name string) {
		if visited[name] {
			return
		}

		visited[name] = true

		for _, dependent := range reverseDeps[name] {
			collect(dependent)
		}
	}

	collect(target)

	// Filter steps to only those in the visited set, preserving original order.
	var subset []Step

	for i := range steps {
		if visited[steps[i].Name] {
			subset = append(subset, steps[i])
		}
	}

	// Strip DependsOn entries that reference steps outside the subset,
	// since we only have the target + its dependents (not its dependencies).
	subsetNames := make(map[string]bool, len(subset))
	for i := range subset {
		subsetNames[subset[i].Name] = true
	}

	filtered := make([]Step, len(subset))
	copy(filtered, subset)

	for i := range filtered {
		if len(filtered[i].DependsOn) == 0 {
			continue
		}

		var deps []string

		for _, dep := range filtered[i].DependsOn {
			if subsetNames[dep] {
				deps = append(deps, dep)
			}
		}

		filtered[i].DependsOn = deps
	}

	// Topologically sort the subset, then reverse for destroy order.
	sorted, err := TopologicalSort(filtered)
	if err != nil {
		return nil, err
	}

	// Reverse: dependents first, target last.
	for i, j := 0, len(sorted)-1; i < j; i, j = i+1, j-1 {
		sorted[i], sorted[j] = sorted[j], sorted[i]
	}

	return sorted, nil
}

// ResolveMultiSubgraph resolves transitive dependencies for each target via
// ResolveSubgraph, unions all results, deduplicates, and returns in topological order.
func ResolveMultiSubgraph(steps []Step, targets []string) ([]Step, error) {
	if err := ValidateDAG(steps); err != nil {
		return nil, err
	}

	included := make(map[string]bool)

	for _, target := range targets {
		sub, err := ResolveSubgraph(steps, target)
		if err != nil {
			return nil, fmt.Errorf("resolve subgraph for %q: %w", target, err)
		}

		for i := range sub {
			included[sub[i].Name] = true
		}
	}

	// Filter steps to the union set, preserving original order.
	var subset []Step

	for i := range steps {
		if included[steps[i].Name] {
			subset = append(subset, steps[i])
		}
	}

	return TopologicalSort(subset)
}

// ResolveMultiDependents resolves transitive dependents for each target via
// ResolveDependents, unions all results, deduplicates, and returns in reverse
// topological order (dependents destroyed first).
func ResolveMultiDependents(steps []Step, targets []string) ([]Step, error) {
	if err := ValidateDAG(steps); err != nil {
		return nil, err
	}

	included := make(map[string]bool)

	for _, target := range targets {
		sub, err := ResolveDependents(steps, target)
		if err != nil {
			return nil, fmt.Errorf("resolve dependents for %q: %w", target, err)
		}

		for i := range sub {
			included[sub[i].Name] = true
		}
	}

	// Filter steps to the union set, preserving original order.
	var subset []Step

	for i := range steps {
		if included[steps[i].Name] {
			subset = append(subset, steps[i])
		}
	}

	// Strip DependsOn entries that reference steps outside the subset.
	subsetNames := make(map[string]bool, len(subset))
	for i := range subset {
		subsetNames[subset[i].Name] = true
	}

	filtered := make([]Step, len(subset))
	copy(filtered, subset)

	for i := range filtered {
		if len(filtered[i].DependsOn) == 0 {
			continue
		}

		var deps []string

		for _, dep := range filtered[i].DependsOn {
			if subsetNames[dep] {
				deps = append(deps, dep)
			}
		}

		filtered[i].DependsOn = deps
	}

	// Topologically sort, then reverse for destroy order.
	sorted, err := TopologicalSort(filtered)
	if err != nil {
		return nil, err
	}

	for i, j := 0, len(sorted)-1; i < j; i, j = i+1, j-1 {
		sorted[i], sorted[j] = sorted[j], sorted[i]
	}

	return sorted, nil
}

// ResolveSubgraph returns the target step plus all its transitive dependencies,
// in topological order. The target step is always the last element.
func ResolveSubgraph(steps []Step, target string) ([]Step, error) {
	if err := ValidateDAG(steps); err != nil {
		return nil, err
	}

	// Build name-to-step index.
	nameToStep := make(map[string]*Step, len(steps))

	for i := range steps {
		nameToStep[steps[i].Name] = &steps[i]
	}

	if _, ok := nameToStep[target]; !ok {
		return nil, fmt.Errorf("unknown step %q (available: %s)", target, StepNames(steps))
	}

	// Collect transitive dependencies via DFS.
	visited := make(map[string]bool)

	var collect func(name string)
	collect = func(name string) {
		if visited[name] {
			return
		}

		visited[name] = true

		s := nameToStep[name]
		for _, dep := range s.DependsOn {
			collect(dep)
		}
	}

	collect(target)

	// Filter steps to only those in the visited set, preserving original order.
	var subset []Step

	for i := range steps {
		if visited[steps[i].Name] {
			subset = append(subset, steps[i])
		}
	}

	// Topologically sort the subset.
	sorted, err := TopologicalSort(subset)
	if err != nil {
		return nil, err
	}

	return sorted, nil
}
