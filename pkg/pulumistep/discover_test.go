package pulumistep

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFiles(t *testing.T, root string, files ...string) {
	t.Helper()

	for _, f := range files {
		p := filepath.Join(root, f)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, nil, 0o644))
	}
}

func TestDiscover_ScopesAndStacksFromDirectory(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root,
		"deploy/net/Pulumi.yaml", "deploy/net/Pulumi.prod.yaml", "deploy/net/Pulumi.dev.yaml",
		"deploy/apps/Pulumi.yaml", "deploy/apps/Pulumi.dev.yaml",
		"deploy/empty/Pulumi.yaml", // a project file alone is not a stack
		"deploy/README.md",         // a file is not a scope
	)

	scopes, err := Discover(DiscoverOptions{Root: root, ScopesDir: "deploy"})
	require.NoError(t, err)

	require.Len(t, scopes, 2)
	assert.Equal(t, "apps", scopes[0].Name)
	assert.Equal(t, "1 stacks in deploy/apps", scopes[0].Usage)
	assert.Equal(t, "net", scopes[1].Name)
	assert.Equal(t, "2 stacks in deploy/net", scopes[1].Usage)

	require.Len(t, scopes[1].Steps, 2)
	assert.Equal(t, "dev", scopes[1].Steps[0].Name)
	assert.Equal(t, "prod", scopes[1].Steps[1].Name)
	assert.Equal(t, "deploy/net", scopes[1].Steps[0].WorkDir)
}

func TestDiscover_HooksSeeEveryStackAndNilMeansNothing(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, "d/a/Pulumi.x.yaml", "d/a/Pulumi.y.yaml")

	type call struct{ scope, stack string }

	var calls []call

	hook := OutputHooksFunc(func(gotRoot, scope, stack string) OutputFunc {
		assert.Equal(t, root, gotRoot)

		calls = append(calls, call{scope, stack})

		if stack == "y" {
			return nil // this stack persists nothing
		}

		return func(context.Context, *slog.Logger, map[string]any) error { return nil }
	})

	scopes, err := Discover(DiscoverOptions{Root: root, ScopesDir: "d", Hooks: hook})
	require.NoError(t, err)
	require.Len(t, scopes, 1)

	assert.Equal(t, []call{{"a", "x"}, {"a", "y"}}, calls)

	// A step with a hook and one without are both valid steps.
	assert.Len(t, scopes[0].Steps, 2)
}

func TestDiscover_Errors(t *testing.T) {
	_, err := Discover(DiscoverOptions{Root: t.TempDir()})
	require.Error(t, err)

	_, err = Discover(DiscoverOptions{Root: t.TempDir(), ScopesDir: "missing"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read missing")
}

func TestParseBackendInfo(t *testing.T) {
	assert.Equal(t, "ci@eu-example-1", parseBackendInfo("awskms://alias/example-state?awssdk=v2&region=eu-example-1&profile=ci@admin"))
	assert.Equal(t, "?", parseBackendInfo(""))
	assert.Equal(t, "?", parseBackendInfo("passphrase"))
	assert.Equal(t, "?", parseBackendInfo("awskms://alias/x?region=eu-example-1"))
}
