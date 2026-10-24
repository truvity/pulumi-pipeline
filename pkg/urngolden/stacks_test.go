package urngolden_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/truvity/pulumi-pipeline/pkg/urngolden"
)

func TestStacksCheck(t *testing.T) {
	dir := t.TempDir()
	stacks := urngolden.Stacks{
		Project: "demo",
		Names:   []string{"dev", "prod"},
		Target:  program,
		Dir:     dir,
	}

	t.Setenv("UPDATE_GOLDEN", "1")
	stacks.Check(t, nil)
	t.Setenv("UPDATE_GOLDEN", "")

	for _, name := range []string{"urns-dev.yaml", "urns-prod.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("golden not written: %v", err)
		}
	}

	setups := 0

	stacks.Check(t, func(*testing.T) { setups++ })

	if setups != 1 {
		t.Fatalf("setup ran %d times, want 1", setups)
	}
}

func TestAWSStateAndCall(t *testing.T) {
	prog := func(ctx *pulumi.Context) error {
		var role pulumi.CustomResourceState

		return ctx.RegisterResource("aws:iam/role:Role", "r", pulumi.Map{"name": pulumi.String("x")}, &role)
	}

	got := urngolden.RunProjectHooks(t, "demo", "dev", prog, urngolden.AWSCall(nil), urngolden.AWSState)
	if got.Count != 1 {
		t.Fatalf("count = %d: %v", got.Count, got.Resources)
	}
}

func TestAWSCallUnknownFallsThrough(t *testing.T) {
	if _, ok := urngolden.AWSCall(nil)(pulumi.MockCallArgs{Token: "other:x/y:y"}); ok {
		t.Fatal("unknown token answered")
	}
}
