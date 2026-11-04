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

	fc, err := readConfigFile(root)
	require.NoError(t, err)

	t.Setenv(EnvScopesDir, "")

	got, err := resolveScopesDir(fc, "", "fallback")
	require.NoError(t, err)
	assert.Equal(t, "from-file", got, "the config file beats the caller's default")

	t.Setenv(EnvScopesDir, "from-env")

	got, err = resolveScopesDir(fc, "", "fallback")
	require.NoError(t, err)
	assert.Equal(t, "from-env", got, "the environment beats the file")

	got, err = resolveScopesDir(fc, "from-flag", "fallback")
	require.NoError(t, err)
	assert.Equal(t, "from-flag", got, "the flag beats everything")

	t.Setenv(EnvScopesDir, "")

	got, err = resolveScopesDir(fileConfig{}, "", "fallback")
	require.NoError(t, err)
	assert.Equal(t, "fallback", got)
}

func TestResolveScopesDir_NoDefaultIsAnError(t *testing.T) {
	t.Setenv(EnvScopesDir, "")

	_, err := resolveScopesDir(fileConfig{}, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--scopes-dir")
	assert.Contains(t, err.Error(), EnvScopesDir)
	assert.Contains(t, err.Error(), ConfigFile)
}

func TestReadConfigFile_RefusesUnknownKeys(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ConfigFile), []byte("scope_dir: typo\n"), 0o644))

	_, err := readConfigFile(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), ConfigFile)
}

func TestReadConfigFile_MissingAndEmptyAreTheZeroValue(t *testing.T) {
	fc, err := readConfigFile(t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, fileConfig{}, fc)

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ConfigFile), []byte("# nothing yet\n"), 0o644))

	fc, err = readConfigFile(root)
	require.NoError(t, err)
	assert.Equal(t, fileConfig{}, fc)
}

func TestReadConfigFile_OwnershipDeclaration(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ConfigFile), []byte(
		"scopes_dir: deploy\norigin: example-org/infra\nowned_stacks:\n  - net/prod\n  - apps/*\n"), 0o644))

	fc, err := readConfigFile(root)
	require.NoError(t, err)
	assert.Equal(t, fileConfig{
		ScopesDir:   "deploy",
		Origin:      "example-org/infra",
		OwnedStacks: []string{"net/prod", "apps/*"},
	}, fc)
}

func TestReadConfigFile_EmptyOwnedStacksIsRefused(t *testing.T) {
	for name, content := range map[string]string{
		"empty list":        "owned_stacks: []\n",
		"null":              "owned_stacks:\n",
		"entries commented": "scopes_dir: deploy\nowned_stacks:\n  # - net/prod\n",
		"explicit null":     "owned_stacks: null\n",
	} {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, ConfigFile), []byte(content), 0o644))

		_, err := readConfigFile(root)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "owned_stacks is empty", name)
	}
}

func TestMain_MalformedOwnershipIsRefusedForEveryCommand(t *testing.T) {
	root := scopesTree(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, ConfigFile), []byte("owned_stacks: [net]\n"), 0o644))

	t.Setenv(EnvScopesDir, "")

	var stderr bytes.Buffer

	code := Main(context.Background(), []string{"tool", "--root", root, "--scopes-dir", "deploy", "list"}, Options{Stderr: &stderr})
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "not scope/stack")
}

func TestMain_DeployOfAStackTheRepositoryDoesNotOwnIsRefused(t *testing.T) {
	root := scopesTree(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, ConfigFile), []byte("owned_stacks: [apps/dev]\n"), 0o644))

	t.Setenv(EnvScopesDir, "")

	var stderr bytes.Buffer

	// The ownership refusal comes before the checkout gate and before any
	// stack is selected, so this needs neither git nor pulumi.
	code := Main(context.Background(), []string{"tool", "--root", root, "--scopes-dir", "deploy", "net", "deploy", "prod"}, Options{Stderr: &stderr})
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "does not own")
	assert.Contains(t, stderr.String(), "net/prod")
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
