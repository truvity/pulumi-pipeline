package urngolden

import (
	"path/filepath"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type (
	// Stacks pins the stacks of one deploy scope: each stack's URNs, as the
	// scope's own dispatch (by c.Stack()) registers them, held to
	// <Dir>/urns-<stack>.yaml. The URN is the identity a stack's state holds,
	// so a name that changes is a replace, and this is what says so before a
	// preview does.
	Stacks struct {
		// Project is the scope's Pulumi project (Pulumi.yaml); it is part of
		// every URN.
		Project string
		// Names are the stacks to pin.
		Names []string
		// Target is the deploy main's own dispatch (by c.Stack()).
		Target pulumi.RunFunc
		// Dir holds the goldens; "testdata" when empty.
		Dir string
		// Extra answers mock calls (Recorder.Extra); nil for none. When Run
		// is nil, AWSCall(Extra) is used, so the common AWS reads are answered
		// too.
		Extra func(args pulumi.MockCallArgs) (resource.PropertyMap, bool)
		// Run, when set, runs one stack and returns its golden, in place of
		// the default (RunProjectHooks with AWSCall(Extra) and AWSState). A
		// caller that wraps the program or checks more than the URNs sets it.
		Run func(t *testing.T, project, stack string, program pulumi.RunFunc,
			extra func(args pulumi.MockCallArgs) (resource.PropertyMap, bool)) *Golden
	}
)

// Check runs every stack and holds it to its golden. setup runs first (fake
// AWS, provider seams), since t.Setenv is per test; nil means none.
// UPDATE_GOLDEN=1 rewrites the goldens.
func (s Stacks) Check(t *testing.T, setup func(t *testing.T)) {
	t.Helper()

	dir := s.Dir
	if dir == "" {
		dir = "testdata"
	}

	if setup != nil {
		setup(t)
	}

	run := s.Run
	if run == nil {
		run = func(t *testing.T, project, stack string, program pulumi.RunFunc,
			extra func(args pulumi.MockCallArgs) (resource.PropertyMap, bool),
		) *Golden {
			t.Helper()

			return RunProjectHooks(t, project, stack, program, AWSCall(extra), AWSState)
		}
	}

	for _, stack := range s.Names {
		Assert(t, filepath.Join(dir, "urns-"+stack+".yaml"), run(t, s.Project, stack, s.Target, s.Extra))
	}
}
