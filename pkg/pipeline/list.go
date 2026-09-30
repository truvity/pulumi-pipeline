package pipeline

import (
	"fmt"
	"os"
	"strings"
)

// ListSteps prints a formatted table of steps in topological order.
// If metadataFn is non-nil, it is called for each step to get additional display columns.
func ListSteps(cfg Config, metadataFn ListMetadataFunc) error {
	repoRoot, err := cfg.RootDir()
	if err != nil {
		return fmt.Errorf("find repo root: %w", err)
	}

	sorted, err := TopologicalSort(cfg.Steps)
	if err != nil {
		return fmt.Errorf("topological sort: %w", err)
	}

	if metadataFn != nil {
		printTableWithMetadata(sorted, repoRoot, metadataFn)
	} else {
		printTable(sorted)
	}

	return nil
}

// printTableWithMetadata prints the step table with project/stack/backend columns.
func printTableWithMetadata(steps []Step, repoRoot string, metadataFn ListMetadataFunc) {
	_, _ = fmt.Fprintf(os.Stdout,
		"\n%-4s %-40s %-10s %-30s %-18s %-18s %-20s %s\n",
		"#", "STEP", "TYPE", "DEPENDS ON", "PROJECT", "STACK", "BACKEND", "ACTIONS")
	_, _ = fmt.Fprintf(os.Stdout,
		"%-4s %-40s %-10s %-30s %-18s %-18s %-20s %s\n",
		"--", "----", "----", "----------", "-------", "-----", "-------", "-------")

	for i := range steps {
		s := &steps[i]

		displayName := formatStepName(s)
		depsStr := formatDeps(s.DependsOn)
		actionsStr := formatActions(s.Actions)

		project, stack, backend := metadataFn(repoRoot, s)

		_, _ = fmt.Fprintf(os.Stdout,
			"%-4d %-40s %-10s %-30s %-18s %-18s %-20s %s\n",
			i+1, displayName, s.Kind, depsStr, project, stack, backend, actionsStr)
	}

	_, _ = fmt.Fprintln(os.Stdout)
}

// printTable prints the step table without metadata columns.
func printTable(steps []Step) {
	_, _ = fmt.Fprintf(os.Stdout,
		"\n%-4s %-40s %-10s %-30s %s\n",
		"#", "STEP", "TYPE", "DEPENDS ON", "ACTIONS")
	_, _ = fmt.Fprintf(os.Stdout,
		"%-4s %-40s %-10s %-30s %s\n",
		"--", "----", "----", "----------", "-------")

	for i := range steps {
		s := &steps[i]

		displayName := formatStepName(s)
		depsStr := formatDeps(s.DependsOn)
		actionsStr := formatActions(s.Actions)

		_, _ = fmt.Fprintf(os.Stdout,
			"%-4d %-40s %-10s %-30s %s\n",
			i+1, displayName, s.Kind, depsStr, actionsStr)
	}

	_, _ = fmt.Fprintln(os.Stdout)
}

// formatStepName returns the step name.
func formatStepName(s *Step) string {
	return s.Name
}

// formatDeps returns a comma-separated string of dependencies, or "—" if none.
func formatDeps(deps []string) string {
	if len(deps) == 0 {
		return "—"
	}

	return strings.Join(deps, ", ")
}

// formatActions returns a human-readable string of supported actions.
func formatActions(actions ActionSet) string {
	var parts []string

	if actions.Supports(ActionDryRun) {
		parts = append(parts, "diff")
	}

	if actions.Supports(ActionExecute) {
		parts = append(parts, "deploy")
	}

	if actions.Supports(ActionSync) {
		parts = append(parts, "refresh")
	}

	if actions.Supports(ActionDestroy) {
		parts = append(parts, "destroy")
	}

	return strings.Join(parts, ", ")
}
