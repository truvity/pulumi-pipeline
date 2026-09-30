package pipeline

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// PrintSummaryTable prints a table of change counts per stack with a TOTAL row.
// Columns: Stack, Create, Update, Delete, Replace, Same.
func PrintSummaryTable(w io.Writer, results map[string]*StepResult) {
	if len(results) == 0 {
		return
	}

	// Sort stack names for deterministic output.
	names := make([]string, 0, len(results))
	for name := range results {
		names = append(names, name)
	}

	sort.Strings(names)

	// Header.
	_, _ = fmt.Fprintf(w, "\n%-40s %8s %8s %8s %8s %8s\n",
		"Stack", "Create", "Update", "Delete", "Replace", "Same")
	_, _ = fmt.Fprintf(w, "%-40s %8s %8s %8s %8s %8s\n",
		"----------------------------------------",
		"--------", "--------", "--------", "--------", "--------")

	var (
		totalCreate, totalUpdate, totalDelete, totalReplace, totalSame int
	)

	for _, name := range names {
		r := results[name]
		if r == nil || r.ChangeSummary == nil {
			_, _ = fmt.Fprintf(w, "%-40s %8d %8d %8d %8d %8d\n",
				name, 0, 0, 0, 0, 0)

			continue
		}

		create := r.ChangeSummary["create"]
		update := r.ChangeSummary["update"]
		del := r.ChangeSummary["delete"]
		replace := r.ChangeSummary["replace"]
		same := r.ChangeSummary["same"]

		totalCreate += create
		totalUpdate += update
		totalDelete += del
		totalReplace += replace
		totalSame += same

		_, _ = fmt.Fprintf(w, "%-40s %8d %8d %8d %8d %8d\n",
			name, create, update, del, replace, same)
	}

	// Total row.
	_, _ = fmt.Fprintf(w, "%-40s %8s %8s %8s %8s %8s\n",
		"----------------------------------------",
		"--------", "--------", "--------", "--------", "--------")
	_, _ = fmt.Fprintf(w, "%-40s %8d %8d %8d %8d %8d\n",
		"TOTAL", totalCreate, totalUpdate, totalDelete, totalReplace, totalSame)
	_, _ = fmt.Fprintln(w)
}

// PrintDetailedSummary prints resource-level diff details.
// This is a placeholder — detailed diff will be implemented when we have
// resource-level data from Pulumi events.
func PrintDetailedSummary(w io.Writer, _ map[string]*StepResult) {
	_, _ = fmt.Fprintln(w, "Detailed diff not yet implemented")
}

// PrintFailureSummary prints a one-line-per-step report of steps that failed
// during a continue-on-error diff sweep (see Run). Steps are printed in the
// order they failed.
func PrintFailureSummary(w io.Writer, failures []StepFailure) {
	if len(failures) == 0 {
		return
	}

	_, _ = fmt.Fprintf(w, "\n%d step(s) failed:\n", len(failures))

	for _, f := range failures {
		_, _ = fmt.Fprintf(w, "  - %-30s %s\n", f.StepName, oneLine(f.Err))
	}

	_, _ = fmt.Fprintln(w)
}

// oneLine collapses a possibly multi-line error message onto a single line
// so it fits the "step name + one-line error" failure summary format.
func oneLine(err error) string {
	return strings.TrimSpace(strings.ReplaceAll(err.Error(), "\n", " "))
}
