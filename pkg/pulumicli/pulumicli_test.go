package pulumicli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreparse(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want preparsed
	}{
		{"none", []string{"tool", "list"}, preparsed{}},
		{"separate values", []string{"tool", "--root", "/r", "--scopes-dir", "deploy", "net", "list"}, preparsed{"/r", "deploy"}},
		{"equals form", []string{"tool", "--root=/r", "--scopes-dir=deploy", "list"}, preparsed{"/r", "deploy"}},
		{"single dash", []string{"tool", "-scopes-dir", "deploy", "list"}, preparsed{"", "deploy"}},
		{"stops at the scope name", []string{"tool", "net", "--scopes-dir", "late"}, preparsed{}},
		{"unknown flags are skipped", []string{"tool", "--other", "--scopes-dir", "d", "list"}, preparsed{"", "d"}},
		{"flag without value at the end", []string{"tool", "--scopes-dir"}, preparsed{}},
		{"program name only", []string{"tool"}, preparsed{}},
		{"no args", nil, preparsed{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, preparse(tt.args))
		})
	}
}

func TestResolveScopesDir_Precedence(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ConfigFile), []byte("scopes_dir: from-file\n"), 0o644))

	t.Setenv(EnvScopesDir, "")

	got, err := resolveScopesDir(root, "", "fallback")
	require.NoError(t, err)
	assert.Equal(t, "from-file", got, "the config file beats the caller's default")

	t.Setenv(EnvScopesDir, "from-env")

	got, err = resolveScopesDir(root, "", "fallback")
	require.NoError(t, err)
	assert.Equal(t, "from-env", got, "the environment beats the file")

	got, err = resolveScopesDir(root, "from-flag", "fallback")
	require.NoError(t, err)
	assert.Equal(t, "from-flag", got, "the flag beats everything")

	t.Setenv(EnvScopesDir, "")

	got, err = resolveScopesDir(t.TempDir(), "", "fallback")
	require.NoError(t, err)
	assert.Equal(t, "fallback", got)
}

func TestResolveScopesDir_NoDefaultIsAnError(t *testing.T) {
	t.Setenv(EnvScopesDir, "")

	_, err := resolveScopesDir(t.TempDir(), "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--scopes-dir")
	assert.Contains(t, err.Error(), EnvScopesDir)
	assert.Contains(t, err.Error(), ConfigFile)
}

func TestResolveScopesDir_ConfigFileRefusesUnknownKeys(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ConfigFile), []byte("scope_dir: typo\n"), 0o644))

	t.Setenv(EnvScopesDir, "")

	_, err := resolveScopesDir(root, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), ConfigFile)
}

func scopesTree(t *testing.T) string {
	t.Helper()

	root := t.TempDir()

	for _, f := range []string{"deploy/net/Pulumi.yaml", "deploy/net/Pulumi.prod.yaml", "deploy/apps/Pulumi.dev.yaml"} {
		p := filepath.Join(root, f)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, nil, 0o644))
	}

	return root
}

func TestMain_ListsDiscoveredScopes(t *testing.T) {
	root := scopesTree(t)

	t.Setenv(EnvScopesDir, "")

	var stderr bytes.Buffer

	code := Main(context.Background(), []string{"tool", "--root", root, "--scopes-dir", "deploy", "--help"}, Options{Stderr: &stderr, Stdout: &bytes.Buffer{}})
	assert.Equal(t, 0, code, stderr.String())

	code = Main(context.Background(), []string{"tool", "--root", root, "--scopes-dir", "deploy", "net", "list"}, Options{Stderr: &stderr})
	assert.Equal(t, 0, code, stderr.String())
}

func TestMain_NoScopesDirIsRefusedWithInstructions(t *testing.T) {
	t.Setenv(EnvScopesDir, "")

	var stderr bytes.Buffer

	code := Main(context.Background(), []string{"tool", "--root", t.TempDir(), "list"}, Options{Stderr: &stderr})
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "no scopes directory")
}

func TestMain_CallerDefaultAndMissingDirectory(t *testing.T) {
	t.Setenv(EnvScopesDir, "")

	var stderr bytes.Buffer

	code := Main(context.Background(), []string{"tool", "--root", t.TempDir(), "list"}, Options{ScopesDir: "nowhere", Stderr: &stderr})
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "read nowhere")
}
