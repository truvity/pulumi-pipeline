package pulumicli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/truvity/pulumi-pipeline/pkg/pipeline"
)

var inputContent = []byte("tenant: acme\n")

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)

	return "sha256:" + hex.EncodeToString(sum[:])
}

const inputsYAML = `inputs:
  - name: tenant-inputs
    path: inputs/tenant-inputs.yaml
    source: file:descriptors/tenant-inputs.json
    max_age: 24h
`

// inputsTree is a scopes tree with the input file, its descriptor and the
// declaration. It is not a git checkout: the inputs gate runs before the
// checkout gate, so a refusal from the first never reaches the second.
func inputsTree(t *testing.T, descriptorDigest string, publishedAgo time.Duration) string {
	t.Helper()

	root := scopesTree(t)

	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}

	write("inputs/tenant-inputs.yaml", string(inputContent))
	write("descriptors/tenant-inputs.json", fmt.Sprintf(`{"digest": %q, "published_at": %q, "revision": "14"}`,
		descriptorDigest, time.Now().Add(-publishedAgo).UTC().Format(time.RFC3339)))
	write(ConfigFile, inputsYAML)

	t.Setenv(EnvScopesDir, "")

	return root
}

func runTool(t *testing.T, opts Options, args ...string) (int, string) {
	t.Helper()

	var stderr bytes.Buffer

	opts.Stderr = &stderr

	return Main(context.Background(), append([]string{"tool"}, args...), opts), stderr.String()
}

func TestReadConfigFile_InputsDeclaration(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ConfigFile), []byte(inputsYAML+"    stacks: [\"net/*\"]\n"), 0o644))

	fc, err := readConfigFile(root)
	require.NoError(t, err)
	assert.Equal(t, []fileInput{{
		Name:   "tenant-inputs",
		Path:   "inputs/tenant-inputs.yaml",
		Source: "file:descriptors/tenant-inputs.json",
		MaxAge: 24 * time.Hour,
		Stacks: []string{"net/*"},
	}}, fc.Inputs)
}

func TestReadConfigFile_EmptyInputsIsRefused(t *testing.T) {
	for name, content := range map[string]string{
		"empty list":        "inputs: []\n",
		"null":              "inputs:\n",
		"entries commented": "scopes_dir: deploy\ninputs:\n  # - name: x\n",
		"explicit null":     "inputs: null\n",
	} {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, ConfigFile), []byte(content), 0o644))

		_, err := readConfigFile(root)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "inputs is empty", name)
	}
}

func TestReadConfigFile_UnknownInputKeyIsRefused(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ConfigFile), []byte(
		"inputs:\n  - name: x\n    path: x.yaml\n    source: file:d.json\n    max_age: 1h\n    maxage: 2h\n"), 0o644))

	_, err := readConfigFile(root)
	require.Error(t, err)
}

func TestMain_MalformedInputsAreRefusedForEveryCommand(t *testing.T) {
	for name, content := range map[string]string{
		"no max_age":       "inputs:\n  - {name: x, path: x.yaml, source: 'file:d.json'}\n",
		"max_age too low":  "inputs:\n  - {name: x, path: x.yaml, source: 'file:d.json', max_age: 10s}\n",
		"max_age a number": "inputs:\n  - {name: x, path: x.yaml, source: 'file:d.json', max_age: 3600}\n",
		"duplicate name": "inputs:\n  - {name: x, path: x.yaml, source: 'file:d.json', max_age: 1h}\n" +
			"  - {name: x, path: y.yaml, source: 'file:d.json', max_age: 1h}\n",
		"path escapes": "inputs:\n  - {name: x, path: ../x.yaml, source: 'file:d.json', max_age: 1h}\n",
		"no scheme":    "inputs:\n  - {name: x, path: x.yaml, source: d.json, max_age: 1h}\n",
	} {
		root := scopesTree(t)
		require.NoError(t, os.WriteFile(filepath.Join(root, ConfigFile), []byte(content), 0o644))
		t.Setenv(EnvScopesDir, "")

		code, stderr := runTool(t, Options{}, "--root", root, "--scopes-dir", "deploy", "list")
		assert.Equal(t, 1, code, name)
		assert.Contains(t, stderr, ConfigFile, name)
	}
}

func TestMain_DeployWithStaleInputIsRefused(t *testing.T) {
	root := inputsTree(t, digestOf([]byte("some other bytes")), time.Hour)

	code, stderr := runTool(t, Options{}, "--root", root, "--scopes-dir", "deploy", "net", "deploy", "prod")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "external inputs are not current")
	assert.Contains(t, stderr, "tenant-inputs (inputs/tenant-inputs.yaml)")
	assert.Contains(t, stderr, "revision 14")
	assert.NotContains(t, stderr, "git checkout", "the inputs are checked before the checkout")
}

func TestMain_DeployOnACurrentInputGoesOnToTheCheckoutGate(t *testing.T) {
	root := inputsTree(t, digestOf(inputContent), time.Hour)

	code, stderr := runTool(t, Options{}, "--root", root, "--scopes-dir", "deploy", "net", "deploy", "prod")
	assert.Equal(t, 1, code)
	assert.NotContains(t, stderr, "external inputs")
	assert.Contains(t, stderr, "git checkout", "this tree is not a checkout, which is the next gate")
}

func TestMain_AllowStaleInputsWaivesAgeOnly(t *testing.T) {
	t.Run("age is refused, then waived by the flag", func(t *testing.T) {
		root := inputsTree(t, digestOf(inputContent), 49*time.Hour)

		code, stderr := runTool(t, Options{}, "--root", root, "--scopes-dir", "deploy", "net", "deploy", "prod")
		assert.Equal(t, 1, code)
		assert.Contains(t, stderr, "older than max_age 24h")
		assert.Contains(t, stderr, "--allow-stale-inputs")

		code, stderr = runTool(t, Options{}, "--root", root, "--scopes-dir", "deploy", "net", "deploy", "--allow-stale-inputs", "prod")
		assert.Equal(t, 1, code)
		assert.NotContains(t, stderr, "external inputs are not current")
		assert.Contains(t, stderr, "PROCEEDING ON AN EXTERNAL INPUT PAST ITS MAX AGE")
		assert.Contains(t, stderr, "git checkout", "it goes on to the next gate")
	})

	t.Run("a digest mismatch is not waived", func(t *testing.T) {
		root := inputsTree(t, digestOf([]byte("some other bytes")), 49*time.Hour)

		code, stderr := runTool(t, Options{}, "--root", root, "--scopes-dir", "deploy", "net", "deploy", "--allow-stale-inputs", "prod")
		assert.Equal(t, 1, code)
		assert.Contains(t, stderr, "is not the published")
	})

	t.Run("only deploy has the flag", func(t *testing.T) {
		root := inputsTree(t, digestOf(inputContent), time.Hour)

		for _, cmd := range []string{"destroy", "refresh", "diff"} {
			code, stderr := runTool(t, Options{}, "--root", root, "--scopes-dir", "deploy", "net", cmd, "--allow-stale-inputs", "prod")
			assert.Equal(t, 1, code, cmd)
			assert.Contains(t, stderr, "allow-stale-inputs", cmd)
		}
	})
}

func TestMain_CallerResolverIsUsedForItsScheme(t *testing.T) {
	root := scopesTree(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "inputs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "inputs", "tenant-inputs.yaml"), inputContent, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ConfigFile), []byte(
		"inputs:\n  - {name: tenant-inputs, path: inputs/tenant-inputs.yaml, source: 'oci://registry.example/acme', max_age: 24h}\n"), 0o644))
	t.Setenv(EnvScopesDir, "")

	opts := Options{InputResolvers: map[string]pipeline.InputResolver{
		"oci": pipeline.InputResolverFunc(func(_ context.Context, _ string) (pipeline.InputState, error) {
			return pipeline.InputState{Digest: digestOf([]byte("moved on")), PublishedAt: time.Now(), Revision: "9"}, nil
		}),
	}}

	code, stderr := runTool(t, opts, "--root", root, "--scopes-dir", "deploy", "net", "deploy", "prod")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "is not the published")
	assert.Contains(t, stderr, "revision 9")

	t.Run("without it the scheme is refused", func(t *testing.T) {
		code, stderr := runTool(t, Options{}, "--root", root, "--scopes-dir", "deploy", "net", "deploy", "prod")
		assert.Equal(t, 1, code)
		assert.Contains(t, stderr, `no resolver for the scheme "oci"`)
	})
}

func TestFileInputResolver(t *testing.T) {
	root := t.TempDir()
	good := digestOf(inputContent)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	write := func(name, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(content), 0o644))
	}

	write("ok.json", fmt.Sprintf(`{"digest": %q, "published_at": %q, "revision": "14"}`, good, now.Format(time.RFC3339)))
	write("unknown.json", fmt.Sprintf(`{"digest": %q, "published_at": %q, "extra": 1}`, good, now.Format(time.RFC3339)))
	write("badtime.json", fmt.Sprintf(`{"digest": %q, "published_at": "yesterday"}`, good))
	write("notjson.json", "digest: x\n")
	write("trailing.json", fmt.Sprintf(`{"digest": %q, "published_at": %q} {}`, good, now.Format(time.RFC3339)))

	r := FileInputResolver(root)

	t.Run("a descriptor, relative to the root", func(t *testing.T) {
		st, err := r.Resolve(context.Background(), "file:ok.json")
		require.NoError(t, err)
		assert.Equal(t, pipeline.InputState{Digest: good, PublishedAt: now, Revision: "14"}, st)
	})

	t.Run("an absolute path", func(t *testing.T) {
		st, err := r.Resolve(context.Background(), "file:"+filepath.Join(root, "ok.json"))
		require.NoError(t, err)
		assert.Equal(t, good, st.Digest)
	})

	for _, name := range []string{"missing.json", "unknown.json", "badtime.json", "notjson.json", "trailing.json"} {
		t.Run("refuses "+name, func(t *testing.T) {
			_, err := r.Resolve(context.Background(), "file:"+name)
			require.Error(t, err)
		})
	}

	t.Run("refuses another scheme", func(t *testing.T) {
		_, err := r.Resolve(context.Background(), "oci://registry.example/x")
		require.Error(t, err)
	})
}
