package pipeline

import (
	"context"
	"log/slog"
	"time"
)

type (
	// ExternalInput is a file the deploy reads that is not produced from the
	// checkout: it is published elsewhere, and a copy committed here is only
	// a cache of it. Declaring one makes a deploy refuse to run on a copy that
	// is not the published one or that was published too long ago.
	ExternalInput struct {
		// Name identifies the input in messages: [a-z0-9-]+, unique.
		Name string
		// Path is the local copy, relative to the checkout root.
		Path string
		// Source is the opaque reference the published state is resolved
		// from, "scheme:...". It is never printed.
		Source string
		// MaxAge is the longest a deploy may trail the publication. Required.
		MaxAge time.Duration
		// Stacks narrows the input to the stacks it feeds, as "scope/stack"
		// patterns (see Ownership.Stacks). Empty: every stack.
		Stacks []string
	}

	// InputState is what a publisher says about an input: the digest of the
	// published bytes, when it was published and an opaque revision.
	InputState struct {
		// Digest is "sha256:" and 64 hex digits, over the raw bytes.
		Digest      string
		PublishedAt time.Time
		Revision    string
	}

	// InputResolver reads the published state of an input. The library does
	// no fetching of its own; a caller supplies a resolver per source scheme.
	InputResolver interface {
		Resolve(ctx context.Context, source string) (InputState, error)
	}

	// InputResolverFunc adapts a function to an InputResolver.
	InputResolverFunc func(ctx context.Context, source string) (InputState, error)

	// inputGuard is what a passed gate leaves behind for the per-step check.
	inputGuard struct{}
)

// Resolve calls f.
func (f InputResolverFunc) Resolve(ctx context.Context, source string) (InputState, error) {
	return f(ctx, source)
}

// ValidateInputs reports a malformed declaration.
func ValidateInputs(_ []ExternalInput) error { return nil }

// requireInputs is the gate. STUB: not implemented yet.
func requireInputs(
	_ context.Context, _ *slog.Logger, _ Config, _ string, _ []Step, _ Action, _ RunOptions,
) (*inputGuard, error) {
	return nil, nil
}

// recheck re-hashes the guarded inputs. STUB.
func (g *inputGuard) recheck(_ string) error { return nil }

// redactSource removes source from msg. STUB.
func redactSource(msg, _ string) string { return msg }
