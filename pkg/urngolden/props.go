package urngolden

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type (
	propsRecorder struct {
		mu    sync.Mutex
		props map[string]any
		dup   []string
	}
)

// NewResource implements pulumi.MockResourceMonitor. Every output a
// program may read back is answered with a value derived from the resource
// type and name, so a policy document built from an ARN is the same
// document in every run.
func (r *propsRecorder) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	key := args.TypeToken + " " + args.Name

	r.mu.Lock()
	if _, seen := r.props[key]; seen {
		r.dup = append(r.dup, key)
	}

	if !strings.HasPrefix(args.TypeToken, "pulumi:providers:") {
		r.props[key] = args.Inputs.Mappable()
	}
	r.mu.Unlock()

	state := args.Inputs.Copy()
	state["arn"] = resource.NewProperty("arn:mock:" + args.TypeToken + ":" + args.Name)
	state["keyId"] = resource.NewProperty("keyid-" + args.Name)

	return args.Name + "-id", state, nil
}

// Call implements pulumi.MockResourceMonitor.
func (r *propsRecorder) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return args.Args.Copy(), nil
}

// RunProps runs program under mocks and returns the inputs every resource
// was registered with, keyed "<type> <name>" (a parent is not part of the
// key: a resource that moves into a component keeps its inputs). Together
// with the URN golden it says that moving a program onto a library changed
// no property of any resource.
func RunProps(t *testing.T, project, stack string, program pulumi.RunFunc) map[string]any {
	t.Helper()

	rec := &propsRecorder{props: map[string]any{}}

	if err := pulumi.RunErr(program, pulumi.WithMocks(project, stack, rec)); err != nil {
		t.Fatalf("program: %v", err)
	}

	if len(rec.dup) > 0 {
		t.Fatalf("the program registers %v twice", rec.dup)
	}

	return rec.props
}

// AssertProps holds got to the JSON file at path (UPDATE_GOLDEN=1 writes it).
func AssertProps(t *testing.T, path string, got map[string]any) {
	t.Helper()

	raw, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("encode properties: %v", err)
	}

	raw = append(raw, '\n')

	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}

		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (UPDATE_GOLDEN=1 writes it): %v", err)
	}

	if bytes.Equal(want, raw) {
		return
	}

	var wantMap map[string]any
	if err := json.Unmarshal(want, &wantMap); err != nil {
		t.Fatalf("parse golden %s: %v", path, err)
	}

	for key, w := range wantMap {
		g, ok := got[key]
		if !ok {
			t.Errorf("REMOVED %s", key)
			continue
		}

		wj, _ := json.Marshal(w)
		gj, _ := json.Marshal(g)

		if !bytes.Equal(wj, gj) {
			t.Errorf("PROPERTIES of %s differ:\n  want %s\n  got  %s", key, wj, gj)
		}
	}

	for key := range got {
		if _, ok := wantMap[key]; !ok {
			t.Errorf("ADDED %s", key)
		}
	}

	t.Fatalf("the properties differ from %s; an in-place diff on a live resource is not a move", path)
}
