package urngolden

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"go.yaml.in/yaml/v3"
)

// The stack layout of an account: a stack is a
// concern, and a restructure moves state between stacks with `pulumi state
// move` instead of recreating anything. Layout.Check is what makes a move
// reviewable:
//
//   - every target stack's dispatch is pinned to testdata/urns-<stack>.yaml;
//   - the target stacks register exactly the resources (names, parents,
//     options) the legacy stacks they replace registered, so the move is a
//     pure re-partition: nothing created, nothing deleted;
//   - testdata/moves.yaml lists, per target stack and source stack, the URNs
//     to pass to `pulumi state move`, generated from the same runs.
//
// UPDATE_GOLDEN=1 rewrites the goldens and the moves file.

type (
	// Layout describes one account's restructure.
	Layout struct {
		// Project is the scope's Pulumi project (Pulumi.yaml); it is part of
		// every URN.
		Project string
		// Legacy are the stacks the account ran before the restructure, as
		// the builders they dispatched to. A row goes when its stack is gone
		// from every account that shares the builder.
		Legacy map[string]pulumi.RunFunc
		// Restructured maps each target stack to the legacy stacks its
		// resources come from. A target that keeps a legacy name (the residue
		// of a split, or the destination of a merge) lists itself.
		Restructured map[string][]string
		// Target is the deploy main's own dispatch (by c.Stack()).
		Target pulumi.RunFunc
		// Dir holds the goldens; "testdata" when empty.
		Dir string
		// Note is appended to the first line of the moves file's header
		// (" (TICKET-1)"), so the file says what restructure it belongs to.
		Note string
		// Extra and State are the mock hooks every run uses (Recorder.Extra,
		// Recorder.State).
		Extra func(args pulumi.MockCallArgs) (resource.PropertyMap, bool)
		State func(args pulumi.MockResourceArgs, state resource.PropertyMap)
	}

	// layoutRow is one registered resource with everything that is its
	// identity in state: "<type> <name>", the parent, and the options a move
	// must carry over.
	layoutRow struct {
		Key, Parent, Options string
	}

	// MoveRow is one `pulumi state move` invocation.
	MoveRow struct {
		To   string   `yaml:"to"`
		From string   `yaml:"from"`
		URNs []string `yaml:"urns"`
	}
)

// run is RunProject with the layout's hooks.
func (l Layout) run(t *testing.T, stack string, program pulumi.RunFunc) *Golden {
	t.Helper()

	return RunProjectHooks(t, l.Project, stack, program, l.Extra, l.State)
}

// Check runs the three layout checks as subtests. setup runs at the start of
// each (fake AWS, provider seams), since t.Setenv is per test.
func (l Layout) Check(t *testing.T, setup func(t *testing.T)) {
	t.Helper()

	dir := l.Dir
	if dir == "" {
		dir = "testdata"
	}

	t.Run("goldens", func(t *testing.T) {
		setup(t)

		for _, target := range sortedKeys(l.Restructured) {
			got := l.run(t, target, l.Target)
			Assert(t, filepath.Join(dir, "urns-"+target+".yaml"), got)
		}
	})

	t.Run("repartition", func(t *testing.T) {
		setup(t)
		l.checkRepartition(t)
	})

	t.Run("moves", func(t *testing.T) {
		setup(t)
		l.checkMoves(t, filepath.Join(dir, "moves.yaml"))
	})
}

// checkRepartition holds the target stacks to the legacy stacks they
// replace: the same resources, under the same parents, with the same
// options. A difference is a create or a delete of live infrastructure,
// which a state move must never be.
func (l Layout) checkRepartition(t *testing.T) {
	t.Helper()

	var legacy, target []layoutRow

	legacyProviders := map[string]bool{}

	for _, name := range l.legacyNames() {
		run, ok := l.Legacy[name]
		if !ok {
			t.Fatalf("legacy stack %s has no builder in Legacy", name)
		}

		rows, providers := rowsOf(l.run(t, name, run))
		legacy = append(legacy, rows...)

		for _, p := range providers {
			legacyProviders[p] = true
		}
	}

	for _, name := range sortedKeys(l.Restructured) {
		rows, providers := rowsOf(l.run(t, name, l.Target))
		target = append(target, rows...)

		for _, p := range providers {
			if !legacyProviders[p] {
				t.Errorf("target stack %s registers provider %q, which no legacy stack had (its resources would see a provider change)", name, p)
			}
		}
	}

	for _, row := range diffRows(legacy, target) {
		t.Errorf("legacy only (a DELETE if moved): %+v", row)
	}

	for _, row := range diffRows(target, legacy) {
		t.Errorf("target only (a CREATE): %+v", row)
	}
}

// checkMoves pins the moves file: per target stack and source stack, the
// URNs `pulumi state move` takes.
func (l Layout) checkMoves(t *testing.T, path string) {
	t.Helper()

	var moves []MoveRow

	for _, target := range sortedKeys(l.Restructured) {
		targetURNs := URNsOf(l.run(t, target, l.Target))

		for _, source := range l.Restructured[target] {
			if source == target {
				continue // stays in place
			}

			var urns []string

			for _, urn := range URNsOf(l.run(t, source, l.Legacy[source])) {
				if slices.Contains(targetURNs, urn) {
					urns = append(urns, urn)
				}
			}

			moves = append(moves, MoveRow{To: target, From: source, URNs: urns})
		}
	}

	var buf bytes.Buffer

	buf.WriteString("# The `pulumi state move` list of this account's stack restructure" + l.Note + ".\n" +
		"# Generated by urngolden.Layout; UPDATE_GOLDEN=1 rewrites it. Per row, run\n" +
		"#   pulumi state move --source <from> --dest <to> urn:pulumi:<from>::" + l.Project + "::<urn>...\n" +
		"# Providers are not listed: Pulumi copies the ones the moved resources use.\n")

	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)

	if err := encoder.Encode(moves); err != nil {
		t.Fatalf("encode moves: %v", err)
	}

	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}

		return
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s (UPDATE_GOLDEN=1 writes it): %v", path, err)
	}

	if !bytes.Equal(raw, buf.Bytes()) {
		t.Fatalf("%s is stale; rerun with UPDATE_GOLDEN=1 and review the diff", path)
	}
}

// legacyNames returns every legacy stack some target draws from, sorted.
func (l Layout) legacyNames() []string {
	seen := map[string]bool{}

	for _, sources := range l.Restructured {
		for _, s := range sources {
			seen[s] = true
		}
	}

	return sortedKeys(seen)
}

// URNsOf returns the URNs of a golden's resources (providers excepted), in
// the short form "<type chain>::<name>" (the chain is the ancestors' types
// joined by "$"), sorted.
func URNsOf(g *Golden) []string {
	var chain func(key string) string

	chain = func(key string) string {
		typ, _, _ := strings.Cut(key, " ")

		if parent := g.Parents[key]; parent != "" && parent != stackType {
			return chain(parent) + "$" + typ
		}

		return typ
	}

	var out []string

	for _, key := range g.Resources {
		if strings.HasPrefix(key, "pulumi:providers:") {
			continue
		}

		_, name, _ := strings.Cut(key, " ")
		out = append(out, chain(key)+"::"+name)
	}

	sort.Strings(out)

	return out
}

// rowsOf splits a golden into its resources (providers excepted) and the
// provider keys.
func rowsOf(g *Golden) (rows []layoutRow, providers []string) {
	for _, key := range g.Resources {
		if strings.HasPrefix(key, "pulumi:providers:") {
			providers = append(providers, key)

			continue
		}

		rows = append(rows, layoutRow{Key: key, Parent: g.Parents[key], Options: g.Options[key]})
	}

	return rows, providers
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))

	for k := range m {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

// diffRows returns the rows of a that b lacks, as a multiset difference,
// in a stable order.
func diffRows(a, b []layoutRow) []layoutRow {
	count := map[layoutRow]int{}

	for _, row := range b {
		count[row]++
	}

	var out []layoutRow

	for _, row := range a {
		if count[row] > 0 {
			count[row]--

			continue
		}

		out = append(out, row)
	}

	sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i]) < fmt.Sprint(out[j]) })

	return out
}
