package urngolden

import (
	"os"
	"sort"

	"strings"
	"sync"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Move is one registered resource: its URN, without the "urn:pulumi:<stack>::<project>::"
// prefix ("<type chain>::<name>"), and the URNs it is aliased from, in the
// same form.
type (
	Move struct {
		URN     string
		Aliases []string
	}

	movesRecorder struct {
		project, stack string

		mu    sync.Mutex
		moves []Move
	}
)

func (r *movesRecorder) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	if !strings.HasPrefix(args.TypeToken, "pulumi:providers:") && args.RegisterRPC != nil {
		rpc := args.RegisterRPC
		m := Move{URN: r.join(rpc.GetParent(), args.TypeToken, args.Name)}

		for _, urn := range rpc.GetAliasURNs() {
			m.Aliases = append(m.Aliases, r.short(urn))
		}

		for _, a := range rpc.GetAliases() {
			if urn := a.GetUrn(); urn != "" {
				m.Aliases = append(m.Aliases, r.short(urn))
				continue
			}

			spec := a.GetSpec()
			if spec == nil {
				continue
			}

			typ, name := spec.GetType(), spec.GetName()
			if typ == "" {
				typ = args.TypeToken
			}

			if name == "" {
				name = args.Name
			}

			parent := rpc.GetParent()
			if spec.GetNoParent() {
				parent = ""
			} else if p := spec.GetParentUrn(); p != "" {
				parent = p
			}

			m.Aliases = append(m.Aliases, r.join(parent, typ, name))
		}

		r.mu.Lock()
		r.moves = append(r.moves, m)
		r.mu.Unlock()
	}

	state := args.Inputs.Copy()
	state["arn"] = resource.NewProperty("arn:mock:" + args.TypeToken + ":" + args.Name)
	state["keyId"] = resource.NewProperty("keyid-" + args.Name)

	return args.Name + "-id", state, nil
}

func (r *movesRecorder) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return args.Args.Copy(), nil
}

// short drops the "urn:pulumi:<stack>::<project>::" prefix.
func (r *movesRecorder) short(urn string) string {
	parts := strings.SplitN(urn, "::", 3)
	if len(parts) != 3 {
		return urn
	}

	return parts[2]
}

// join is the URN of a resource of type typ and name under parent (a URN, or
// "" for the stack): the parent's type chain, "$", the type, "::", the name.
func (r *movesRecorder) join(parent, typ, name string) string {
	if parent != "" {
		if p := strings.SplitN(r.short(parent), "::", 2); len(p) == 2 && p[0] != stackType {
			typ = p[0] + "$" + typ
		}
	}

	return typ + "::" + name
}

// RunMoves runs program under mocks and returns every resource it registers
// (providers excepted) with its URN and the URNs it is aliased from. A
// resource that is not aliased and not moved has no aliases.
func RunMoves(t *testing.T, project, stack string, program pulumi.RunFunc) []Move {
	t.Helper()

	rec := &movesRecorder{project: project, stack: stack}

	if err := pulumi.RunErr(program, pulumi.WithMocks(project, stack, rec)); err != nil {
		t.Fatalf("program: %v", err)
	}

	return rec.moves
}

// LegacyURNs reads a URN golden (as Assert writes it) and returns the URN of
// each resource but the providers, in Move's short form, sorted.
func LegacyURNs(t *testing.T, path string) []string {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var g Golden
	if err := yaml.Unmarshal(raw, &g); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var out []string

	for _, key := range g.Resources {
		typ, name, _ := strings.Cut(key, " ")
		if strings.HasPrefix(typ, "pulumi:providers:") {
			continue
		}

		if parent := g.Parents[key]; parent != "" && parent != "pulumi:pulumi:Stack" {
			ptyp, _, _ := strings.Cut(parent, " ")
			typ = ptyp + "$" + typ
		}

		out = append(out, typ+"::"+name)
	}

	sort.Strings(out)

	return out
}
