package yamlutils_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	yamlutils "github.com/truvity/pulumi-pipeline/pkg/yamlutils"
)

func TestMarshalSorted(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"zebra": "last",
		"alpha": "first",
		"nested": map[string]any{
			"z-key": "z-val",
			"a-key": "a-val",
		},
	}

	data, err := yamlutils.MarshalSorted(input)
	require.NoError(t, err)

	expected := `alpha: first
nested:
  a-key: a-val
  z-key: z-val
zebra: last
`
	assert.Equal(t, expected, string(data))
}

func TestMarshalSortedDeterministic(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"c": 3,
		"a": 1,
		"b": map[string]any{
			"y": 2,
			"x": 1,
		},
	}

	// Marshal multiple times — output must be identical.
	var results []string

	for range 5 {
		data, err := yamlutils.MarshalSorted(input)
		require.NoError(t, err)

		results = append(results, string(data))
	}

	for i := 1; i < len(results); i++ {
		assert.Equal(t, results[0], results[i], "marshal %d differs from marshal 0", i)
	}
}

func TestWriteFileAtomic(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "output.yaml")

	err := yamlutils.WriteFileAtomic(path, []byte("key: value\n"), 0o644, "# header\n")
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	assert.Equal(t, "# header\nkey: value\n", string(data))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

func TestWriteFileAtomicNoHeader(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "output.yaml")

	err := yamlutils.WriteFileAtomic(path, []byte("key: value\n"), 0o644, "")
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	assert.Equal(t, "key: value\n", string(data))
}

func TestFlattenOutputMap(t *testing.T) {
	t.Parallel()

	// Simulate Pulumi auto.OutputMap structure.
	input := map[string]any{
		"plain-key": map[string]any{
			"value":  "plain-value",
			"secret": false,
		},
		"secret-key": map[string]any{
			"value":  "secret-value",
			"secret": true,
		},
		"nested": map[string]any{
			"value": map[string]any{
				"inner": "data",
			},
			"secret": false,
		},
		"regular": "just-a-string",
	}

	result := yamlutils.FlattenOutputMap(input)

	assert.Equal(t, "plain-value", result["plain-key"])
	assert.Equal(t, "secret-value", result["secret-key"])
	assert.Equal(t, map[string]any{"inner": "data"}, result["nested"])
	assert.Equal(t, "just-a-string", result["regular"])
}

func TestFlattenOutputMapWrappedSlice(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"list-key": map[string]any{
			"value":  []any{"a", "b", "c"},
			"secret": false,
		},
	}

	result := yamlutils.FlattenOutputMap(input)

	assert.Equal(t, []any{"a", "b", "c"}, result["list-key"])
}

func TestFlattenOutputMapNonCanonicalWrapper(t *testing.T) {
	t.Parallel()

	// A map with "value" and "secret" keys plus extra keys should NOT be unwrapped.
	input := map[string]any{
		"extra-keys": map[string]any{
			"value":  "should-not-unwrap",
			"secret": false,
			"extra":  "key",
		},
	}

	result := yamlutils.FlattenOutputMap(input)

	expected := map[string]any{
		"value":  "should-not-unwrap",
		"secret": false,
		"extra":  "key",
	}
	assert.Equal(t, expected, result["extra-keys"])
}

func TestFlattenOutputMapNonBoolSecret(t *testing.T) {
	t.Parallel()

	// A map with "value" and "secret" keys where secret is not a bool should NOT be unwrapped.
	input := map[string]any{
		"non-bool-secret": map[string]any{
			"value":  "should-not-unwrap",
			"secret": "not-a-bool",
		},
	}

	result := yamlutils.FlattenOutputMap(input)

	expected := map[string]any{
		"value":  "should-not-unwrap",
		"secret": "not-a-bool",
	}
	assert.Equal(t, expected, result["non-bool-secret"])
}

func TestFlattenOutputMapNestedWrappedSlice(t *testing.T) {
	t.Parallel()

	// Nested wrapped slices should be recursively flattened.
	input := map[string]any{
		"nested-list": map[string]any{
			"value": []any{
				map[string]any{
					"value":  "inner-val",
					"secret": true,
				},
				"plain-item",
			},
			"secret": false,
		},
	}

	result := yamlutils.FlattenOutputMap(input)

	assert.Equal(t, []any{"inner-val", "plain-item"}, result["nested-list"])
}
