package urngolden_test

import (
	"path/filepath"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/truvity/pulumi-pipeline/pkg/urngolden"
)

type pair struct {
	pulumi.ResourceState
}

func program(ctx *pulumi.Context) error {
	var group pair

	if err := ctx.RegisterComponentResource("test:index:Pair", "pair", &group); err != nil {
		return err
	}

	var res pulumi.CustomResourceState

	return ctx.RegisterResource("test:index:Thing", "thing", pulumi.Map{}, &res,
		pulumi.Parent(&group), pulumi.Protect(true))
}

func TestRunAndAssert(t *testing.T) {
	got := urngolden.RunProject(t, "demo", "dev", program)

	if got.Count != 2 {
		t.Fatalf("count = %d, want 2: %v", got.Count, got.Resources)
	}

	path := filepath.Join(t.TempDir(), "urns.yaml")

	t.Setenv("UPDATE_GOLDEN", "1")
	urngolden.Assert(t, path, got)
	t.Setenv("UPDATE_GOLDEN", "")

	urngolden.Assert(t, path, urngolden.RunProject(t, "demo", "dev", program))

	if got.Options["test:index:Thing thing"] != "protect" {
		t.Fatalf("options = %v", got.Options)
	}
}
