// Package yamlutils provides deterministic YAML serialization with sorted keys
// and atomic file writes. Used for stack outputs written to committed files
// and any other code that needs stable, reproducible YAML output.
package yamlutils

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"go.yaml.in/yaml/v3"
)

const (
	yamlIndent = 2
)

// MarshalSorted produces deterministic YAML output from an arbitrary value.
// Maps are sorted by key at all nesting levels. The output is suitable for
// committing to git — deterministic ordering prevents spurious diffs.
func MarshalSorted(v any) ([]byte, error) {
	// First marshal to a yaml.Node tree, then sort all mapping keys.
	var node yaml.Node
	if err := node.Encode(v); err != nil {
		return nil, fmt.Errorf("encode to yaml node: %w", err)
	}

	sortNode(&node)

	return marshalIndented(&node)
}

// MarshalNode produces deterministic YAML output from a pre-built yaml.Node tree.
// Sorts all mapping keys recursively. Use this when you build the node tree
// manually (a lockfile with a specific field order).
func MarshalNode(node *yaml.Node) ([]byte, error) {
	sortNode(node)

	return marshalIndented(node)
}

// marshalIndented encodes a yaml.Node with 2-space indentation.
func marshalIndented(node *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(yamlIndent)

	if err := enc.Encode(node); err != nil {
		return nil, fmt.Errorf("encode yaml: %w", err)
	}

	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close yaml encoder: %w", err)
	}

	return buf.Bytes(), nil
}

// WriteFileAtomic writes data to a file atomically via temp+rename.
// The file is created with the given permissions. If a header comment is
// provided, it is prepended to the data.
func WriteFileAtomic(path string, data []byte, perm os.FileMode, header string) error {
	dir := filepath.Dir(path)

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".yaml-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	tmpName := tmp.Name()

	defer func() {
		// Clean up temp file on any error path.
		_ = os.Remove(tmpName)
	}()

	if header != "" {
		if _, writeErr := tmp.WriteString(header); writeErr != nil {
			_ = tmp.Close()

			return fmt.Errorf("write header: %w", writeErr)
		}
	}

	if _, writeErr := tmp.Write(data); writeErr != nil {
		_ = tmp.Close()

		return fmt.Errorf("write data: %w", writeErr)
	}

	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("sync temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %s → %s: %w", tmpName, path, err)
	}

	return nil
}

// sortNode recursively sorts all mapping nodes by key.
func sortNode(node *yaml.Node) {
	if node == nil {
		return
	}

	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			sortNode(child)
		}

	case yaml.MappingNode:
		sortMappingNode(node)

		// Recurse into values.
		for i := 1; i < len(node.Content); i += 2 {
			sortNode(node.Content[i])
		}

	case yaml.SequenceNode:
		for _, child := range node.Content {
			sortNode(child)
		}
	}
}

// sortMappingNode sorts a mapping node's key-value pairs by key.
func sortMappingNode(node *yaml.Node) {
	const minSortableMapPairs = 4 // 2 YAML map pairs = 4 content nodes
	if len(node.Content) < minSortableMapPairs {
		return
	}

	n := len(node.Content) / 2

	type (
		pair struct {
			key *yaml.Node
			val *yaml.Node
		}
	)

	pairs := make([]pair, n)
	for i := range n {
		pairs[i] = pair{
			key: node.Content[i*2],
			val: node.Content[i*2+1],
		}
	}

	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].key.Value < pairs[j].key.Value
	})

	for i, p := range pairs {
		node.Content[i*2] = p.key
		node.Content[i*2+1] = p.val
	}
}

// FlattenOutputMap converts a Pulumi auto.OutputMap (map[string]auto.OutputValue)
// to a plain map[string]any by stripping the secret metadata wrapper.
// Nested maps are recursively flattened.
func FlattenOutputMap(outputs map[string]any) map[string]any {
	result := make(map[string]any, len(outputs))

	for k, v := range outputs {
		result[k] = flattenValue(v)
	}

	return result
}

func flattenValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		// Check if this is a Pulumi OutputValue wrapper with "value" and "secret" keys.
		if _, hasValue := val["value"]; hasValue {
			const secretPairKeyCount = 2 // "secret" + one other key
			if secret, hasSecret := val["secret"]; hasSecret && len(val) == secretPairKeyCount {
				if _, isBool := secret.(bool); isBool {
					return flattenValue(val["value"])
				}
			}
		}

		result := make(map[string]any, len(val))
		for k, inner := range val {
			result[k] = flattenValue(inner)
		}

		return result

	case []any:
		result := make([]any, len(val))
		for i, inner := range val {
			result[i] = flattenValue(inner)
		}

		return result

	default:
		return v
	}
}
