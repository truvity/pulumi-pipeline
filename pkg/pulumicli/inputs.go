package pulumicli

import (
	"context"
	"time"

	"github.com/truvity/pulumi-pipeline/pkg/pipeline"
)

// fileInput is one entry of the `inputs:` list in ConfigFile.
type fileInput struct {
	Name   string        `yaml:"name"`
	Path   string        `yaml:"path"`
	Source string        `yaml:"source"`
	MaxAge time.Duration `yaml:"max_age"`
	Stacks []string      `yaml:"stacks"`
}

// FileScheme is the source scheme of the built-in resolver.
const FileScheme = "file"

// FileInputResolver resolves a "file:<path>" source from a descriptor file.
// STUB: not implemented yet.
func FileInputResolver(_ string) pipeline.InputResolver {
	return pipeline.InputResolverFunc(func(_ context.Context, _ string) (pipeline.InputState, error) {
		return pipeline.InputState{}, nil
	})
}
