package urngolden_test

import (
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/truvity/pulumi-pipeline/pkg/urngolden"
)

func thing(ctx *pulumi.Context) error {
	var res pulumi.CustomResourceState

	return ctx.RegisterResource("test:index:Thing", "thing", pulumi.Map{}, &res)
}

// A restructure that only re-partitions passes: the target stack registers what
// the legacy stack did, and the moves file lists the URN to move.
func TestLayoutCheck(t *testing.T) {
	layout := urngolden.Layout{
		Project:      "demo",
		Legacy:       map[string]pulumi.RunFunc{"old": thing},
		Restructured: map[string][]string{"new": {"old"}},
		Target:       thing,
		Dir:          t.TempDir(),
		Note:         " (a note)",
	}

	t.Setenv("UPDATE_GOLDEN", "1")
	layout.Check(t, func(*testing.T) {})
	t.Setenv("UPDATE_GOLDEN", "")

	layout.Check(t, func(*testing.T) {})
}
