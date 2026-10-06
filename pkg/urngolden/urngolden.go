// Package urngolden pins the Pulumi resource names (URNs) a stack registers.
//
// A Pulumi logical name is a resource's identity: a name that changes is a
// create of the new resource and a DELETE of the old one. The stacks whose
// resources cannot be replaced lightly (an EKS cluster, its KMS key, its IAM
// roles) are therefore held to a golden: the program runs under Pulumi mocks,
// every registration is recorded, and the sorted "<type> <name>" list is
// compared with a file in testdata. Beside each name the golden keeps the
// resource options a later move of the code must carry over unchanged
// (protect, retainOnDelete, aliases, ignoreChanges, import, deleteBeforeReplace).
//
// UPDATE_GOLDEN=1 rewrites the file; a diff that is not an intended new
// resource is a rename.
package urngolden

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"go.yaml.in/yaml/v3"
)

// stackType is the Pulumi type of the stack resource.
const stackType = "pulumi:pulumi:Stack"

type (
	// Golden is the file's content.
	Golden struct {
		// Count is len(Resources).
		Count int `yaml:"count"`
		// Resources is every registered resource as "<type> <name>", sorted.
		Resources []string `yaml:"resources"`
		// Options lists, per resource, the options that are set and matter to
		// a move: protect, retainOnDelete, aliases, ignoreChanges, import,
		// deleteBeforeReplace. A resource with none of them has no entry.
		Options map[string]string `yaml:"options"`
		// Parents maps every resource to its parent, as "<type> <name>" (the
		// stack itself is "pulumi:pulumi:Stack"). A resource that moves into
		// a ComponentResource changes its URN even when its name stays, so
		// the parent is part of the identity the golden pins.
		Parents map[string]string `yaml:"parents"`
	}

	record struct {
		key     string
		options string
		parent  string
	}

	// Recorder is the mock monitor; it records every registration.
	Recorder struct {
		// Extra, when set, answers a read first; ok=false falls through to
		// the built-in answer (the arguments echoed back).
		Extra func(args pulumi.MockCallArgs) (out resource.PropertyMap, ok bool)

		// State, when set, adds the outputs a resource would have after it is
		// created (an endpoint, an identity, a key) to its state, which starts
		// as a copy of its inputs. A program reads these back, so the mock
		// has to answer them.
		State func(args pulumi.MockResourceArgs, state resource.PropertyMap)

		mu      sync.Mutex
		records []record
	}
)

// NewResource implements pulumi.MockResourceMonitor.
func (r *Recorder) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	rec := record{key: args.TypeToken + " " + args.Name}

	if rpc := args.RegisterRPC; rpc != nil {
		rec.parent = parentKey(rpc.GetParent())

		var opts []string

		if rpc.GetProtect() {
			opts = append(opts, "protect")
		}

		if rpc.GetRetainOnDelete() {
			opts = append(opts, "retainOnDelete")
		}

		if rpc.GetDeleteBeforeReplace() {
			opts = append(opts, "deleteBeforeReplace")
		}

		if ignore := slices.Clone(rpc.GetIgnoreChanges()); len(ignore) > 0 {
			sort.Strings(ignore)
			opts = append(opts, "ignoreChanges="+strings.Join(ignore, ","))
		}

		if id := rpc.GetImportId(); id != "" {
			opts = append(opts, "import")
		}

		var aliases []string

		aliases = append(aliases, rpc.GetAliasURNs()...)

		for _, alias := range rpc.GetAliases() {
			if spec := alias.GetSpec(); spec != nil {
				var parts []string
				if spec.GetName() != "" {
					parts = append(parts, "name="+spec.GetName())
				}

				if spec.GetType() != "" {
					parts = append(parts, "type="+spec.GetType())
				}

				if spec.GetNoParent() {
					parts = append(parts, "noParent")
				}

				aliases = append(aliases, strings.Join(parts, " "))
			} else if urn := alias.GetUrn(); urn != "" {
				aliases = append(aliases, urn)
			}
		}

		if len(aliases) > 0 {
			sort.Strings(aliases)
			opts = append(opts, "aliases=["+strings.Join(aliases, "; ")+"]")
		}

		rec.options = strings.Join(opts, " ")
	}

	r.mu.Lock()
	r.records = append(r.records, rec)
	r.mu.Unlock()

	state := args.Inputs.Copy()

	if r.State != nil {
		r.State(args, state)
	}

	if args.TypeToken == "tls:index/privateKey:PrivateKey" {
		// A real ED25519 key: a program that derives key material from the PEM.
		_, key, err := ed25519.GenerateKey(nil)
		if err != nil {
			return "", nil, err
		}

		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return "", nil, err
		}

		state["privateKeyPem"] = resource.NewProperty(string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})))
	}

	return args.Name + "-id", state, nil
}

// Call implements pulumi.MockResourceMonitor. Extra answers the reads a
// program needs a plausible answer to; everything else echoes its arguments.
func (r *Recorder) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	if r.Extra != nil {
		if out, ok := r.Extra(args); ok {
			return out, nil
		}
	}

	out := args.Args.Copy()

	return out, nil
}

// RunProjectWith is RunProject with extra read answers (see Recorder.Extra).
func RunProjectWith(t *testing.T, project, stack string, program pulumi.RunFunc,
	extra func(args pulumi.MockCallArgs) (resource.PropertyMap, bool),
) *Golden {
	t.Helper()

	return RunProjectHooks(t, project, stack, program, extra, nil)
}

// RunProjectHooks is RunProject with both mock hooks: extra read answers
// (Recorder.Extra) and created-resource state (Recorder.State).
func RunProjectHooks(t *testing.T, project, stack string, program pulumi.RunFunc,
	extra func(args pulumi.MockCallArgs) (resource.PropertyMap, bool),
	state func(args pulumi.MockResourceArgs, state resource.PropertyMap),
) *Golden {
	t.Helper()

	return runWith(t, project, stack, program, extra, state)
}

// RunProject runs program under the recorder, as the given Pulumi project and
// stack (the project name is part of every URN), and returns the golden it
// yields.
func RunProject(t *testing.T, project, stack string, program pulumi.RunFunc) *Golden {
	t.Helper()

	return runWith(t, project, stack, program, nil, nil)
}

func runWith(t *testing.T, project, stack string, program pulumi.RunFunc,
	extra func(args pulumi.MockCallArgs) (resource.PropertyMap, bool),
	state func(args pulumi.MockResourceArgs, state resource.PropertyMap),
) *Golden {
	t.Helper()

	rec := &Recorder{Extra: extra, State: state}

	if err := pulumi.RunErr(program, pulumi.WithMocks(project, stack, rec)); err != nil {
		t.Fatalf("program: %v", err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()

	golden := &Golden{Options: map[string]string{}, Parents: map[string]string{}}
	seen := map[string]bool{}

	for _, item := range rec.records {
		if seen[item.key] {
			t.Fatalf("the program registers %q twice", item.key)
		}

		seen[item.key] = true
		golden.Resources = append(golden.Resources, item.key)

		if item.options != "" {
			golden.Options[item.key] = item.options
		}

		if item.parent != "" {
			golden.Parents[item.key] = item.parent
		}
	}

	sort.Strings(golden.Resources)
	golden.Count = len(golden.Resources)

	return golden
}

// Assert holds got to the golden at path, or rewrites it under UPDATE_GOLDEN.
// A mismatch names every added and removed resource and every changed option.
func Assert(t *testing.T, path string, got *Golden) {
	t.Helper()

	var buf bytes.Buffer

	buf.WriteString("# Pulumi resource names (URNs) of this stack. Generated by the URN golden test;\n" +
		"# UPDATE_GOLDEN=1 rewrites it. A name that changes is a replace: move code only with aliases.\n")

	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)

	if err := encoder.Encode(got); err != nil {
		t.Fatalf("encode golden: %v", err)
	}

	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}

		return
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (UPDATE_GOLDEN=1 writes it): %v", err)
	}

	if bytes.Equal(raw, buf.Bytes()) {
		return
	}

	var want Golden
	if err := yaml.Unmarshal(raw, &want); err != nil {
		t.Fatalf("parse golden %s: %v", path, err)
	}

	var msg strings.Builder

	fmt.Fprintf(&msg, "the Pulumi resource names differ from %s.\n", path)
	msg.WriteString("A removed name is a DELETE of live infrastructure, an added one a create; a pair of them is a rename.\n")

	for _, name := range setDiff(want.Resources, got.Resources) {
		fmt.Fprintf(&msg, "  REMOVED %s\n", name)
	}

	for _, name := range setDiff(got.Resources, want.Resources) {
		fmt.Fprintf(&msg, "  ADDED   %s\n", name)
	}

	for _, name := range got.Resources {
		if w, g := want.Parents[name], got.Parents[name]; w != g && slices.Contains(want.Resources, name) {
			fmt.Fprintf(&msg, "  PARENT  %s: %q -> %q (a reparent changes the URN: alias it)\n", name, w, g)
		}
	}

	for _, name := range got.Resources {
		if w, g := want.Options[name], got.Options[name]; w != g && slices.Contains(want.Resources, name) {
			fmt.Fprintf(&msg, "  OPTIONS %s: %q -> %q\n", name, w, g)
		}
	}

	msg.WriteString("If the change is intended, review it like a migration and rerun with UPDATE_GOLDEN=1.")

	t.Fatal(msg.String())
}

// parentKey renders a parent URN ("urn:pulumi:<stack>::<project>::<type>::<name>",
// the type being a "$"-joined chain of ancestors) as "<own type> <name>", the
// form of the resources list. The stack resource is "pulumi:pulumi:Stack".
func parentKey(urn string) string {
	if urn == "" {
		return ""
	}

	parts := strings.SplitN(urn, "::", 4)
	if len(parts) != 4 {
		return urn
	}

	typ, name := parts[2], parts[3]
	if typ == stackType {
		return typ
	}

	if i := strings.LastIndex(typ, "$"); i >= 0 {
		typ = typ[i+1:]
	}

	return typ + " " + name
}

// setDiff returns the members of a that b lacks.
func setDiff(a, b []string) []string {
	var out []string

	for _, item := range a {
		if !slices.Contains(b, item) {
			out = append(out, item)
		}
	}

	return out
}
