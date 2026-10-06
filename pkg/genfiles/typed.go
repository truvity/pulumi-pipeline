package genfiles

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

type (
	// Validatable is a generated file's data type: it checks itself.
	Validatable interface {
		Validate() error
	}

	// ptrValidatable binds *T to Validatable so generic functions work with
	// pointer receivers.
	ptrValidatable[T any] interface {
		*T
		Validatable
	}
)

// Parse unmarshals data into T and validates it.
func Parse[T any, PT ptrValidatable[T]](path string, data []byte) (*T, error) {
	var out T
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if err := PT(&out).Validate(); err != nil {
		return nil, fmt.Errorf("validate %s: %w", path, err)
	}

	return &out, nil
}

// Marshal validates val and returns its YAML.
func Marshal[T any, PT ptrValidatable[T]](path string, val *T) ([]byte, error) {
	if err := PT(val).Validate(); err != nil {
		return nil, fmt.Errorf("validate before save %s: %w", path, err)
	}

	data, err := yaml.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", path, err)
	}

	return data, nil
}

// Canonical converts raw key-value outputs (a Pulumi stack's, say) to T by a
// YAML round trip, validates it, and returns T's YAML: the canonical form.
func Canonical[T any, PT ptrValidatable[T]](path string, data map[string]any) ([]byte, error) {
	raw, err := yaml.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal map for %s: %w", path, err)
	}

	var out T
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("unmarshal into type for %s: %w", path, err)
	}

	if err := PT(&out).Validate(); err != nil {
		return nil, fmt.Errorf("validate %s: %w", path, err)
	}

	canonical, err := yaml.Marshal(&out)
	if err != nil {
		return nil, fmt.Errorf("remarshal %s: %w", path, err)
	}

	return canonical, nil
}
