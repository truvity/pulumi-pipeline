package pipeline

import (
	"context"
)

type (
	// ctxKey namespaces context values set by this package.
	ctxKey int
)

const (
	// showProvidersKey carries the --show-providers diff display flag.
	showProvidersKey ctxKey = iota
)

// WithShowProviders returns a context carrying the --show-providers diff
// display flag. Step implementations (e.g. pulumistep) read it via
// ShowProviders to decide whether to print pulumi:providers:* version-only
// updates in full or suppress them as noise.
func WithShowProviders(ctx context.Context, show bool) context.Context {
	return context.WithValue(ctx, showProvidersKey, show)
}

// ShowProviders reports whether full pulumi:providers:* diff output should
// be shown. Defaults to false (version-only provider updates suppressed)
// when the context carries no value, e.g. in tests that build a bare
// context.Background().
func ShowProviders(ctx context.Context) bool {
	show, _ := ctx.Value(showProvidersKey).(bool)

	return show
}
