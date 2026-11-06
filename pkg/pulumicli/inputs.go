package pulumicli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/truvity/pulumi-pipeline/pkg/pipeline"
)

// FileScheme is the source scheme of the built-in resolver.
const FileScheme = "file"

// maxDescriptorBytes bounds a descriptor file; a real one is a few lines.
const maxDescriptorBytes = 1 << 20

type (
	// fileInput is one entry of the `inputs:` list in ConfigFile.
	fileInput struct {
		Name   string        `yaml:"name"`
		Path   string        `yaml:"path"`
		Source string        `yaml:"source"`
		MaxAge time.Duration `yaml:"max_age"`
		Stacks []string      `yaml:"stacks"`
	}

	// descriptor is the JSON a "file:" source points at.
	descriptor struct {
		Digest      string    `json:"digest"`
		PublishedAt time.Time `json:"published_at"`
		Revision    string    `json:"revision"`
	}
)

// declaredInputs is the `inputs:` list as the library's declaration.
func (fc fileConfig) declaredInputs() []pipeline.ExternalInput {
	if len(fc.Inputs) == 0 {
		return nil
	}

	out := make([]pipeline.ExternalInput, len(fc.Inputs))
	for i, in := range fc.Inputs {
		out[i] = pipeline.ExternalInput{Name: in.Name, Path: in.Path, Source: in.Source, MaxAge: in.MaxAge, Stacks: in.Stacks}
	}

	return out
}

// inputResolvers is the built-in "file" resolver with the caller's on top;
// a caller's entry for a scheme replaces the built-in one.
func inputResolvers(root string, callers map[string]pipeline.InputResolver) map[string]pipeline.InputResolver {
	out := map[string]pipeline.InputResolver{FileScheme: FileInputResolver(root)}
	for scheme, r := range callers {
		out[scheme] = r
	}

	return out
}

// FileInputResolver resolves a "file:<path>" source from a descriptor on
// disk: JSON with "digest" ("sha256:" and 64 hex digits), "published_at"
// (RFC 3339) and an optional "revision". A relative path is taken from root.
//
// It exists for tests and fixtures. A descriptor committed next to the input
// it describes proves nothing about the publisher, so a deployment that
// declares a "file:" source gets no protection from the gate; production
// supplies a resolver that asks the publisher (Options.InputResolvers).
func FileInputResolver(root string) pipeline.InputResolver {
	return pipeline.InputResolverFunc(func(_ context.Context, source string) (pipeline.InputState, error) {
		path, ok := strings.CutPrefix(source, FileScheme+":")
		if !ok || path == "" {
			return pipeline.InputState{}, errors.New("not a file: source")
		}

		if !filepath.IsAbs(path) {
			path = filepath.Join(root, filepath.FromSlash(path))
		}

		f, err := os.Open(path)
		if err != nil {
			return pipeline.InputState{}, errors.New("the descriptor cannot be opened")
		}

		defer func() { _ = f.Close() }()

		data, err := io.ReadAll(io.LimitReader(f, maxDescriptorBytes+1))
		if err != nil || len(data) > maxDescriptorBytes {
			return pipeline.InputState{}, errors.New("the descriptor cannot be read")
		}

		var d descriptor

		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()

		if err := dec.Decode(&d); err != nil {
			return pipeline.InputState{}, fmt.Errorf("the descriptor is not valid: %w", err)
		}

		if _, err := dec.Token(); !errors.Is(err, io.EOF) {
			return pipeline.InputState{}, errors.New("the descriptor holds more than one JSON value")
		}

		return pipeline.InputState{Digest: d.Digest, PublishedAt: d.PublishedAt, Revision: d.Revision}, nil
	})
}
