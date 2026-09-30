package pulumistep

import (
	"context"
	"log/slog"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
)

// OutputsToMap converts Pulumi stack outputs to map[string]any, filtering secrets.
// This is the bridge between the Pulumi Automation API and cfg's Save functions.
func OutputsToMap(outputs auto.OutputMap) map[string]any {
	result := make(map[string]any, len(outputs))

	for key, val := range outputs {
		if val.Secret {
			continue // never persist secrets to cfg/gen
		}

		result[key] = val.Value
	}

	return result
}

// callOnOutput retrieves stack outputs and calls the OnOutput callback.
func callOnOutput(ctx context.Context, logger *slog.Logger, stack auto.Stack, onOutput func(context.Context, *slog.Logger, map[string]any) error) error {
	outputs, err := stack.Outputs(ctx)
	if err != nil {
		return err
	}

	return onOutput(ctx, logger, OutputsToMap(outputs))
}

// callOnOutputAfterDiff is callOnOutput for a preview. A stack that has never
// been deployed has no outputs, and a preview cannot give it any, so there is
// nothing to sync: skip instead of failing. Without this, enrolling a new
// stack in the gen-sync hook before its first deploy (so that deploy writes
// the file) made every `pipeline <scope> diff` of that stack fail. A deploy
// keeps the strict path — it produces outputs, and producing none is a real
// fault the gen writer should report.
func callOnOutputAfterDiff(
	ctx context.Context,
	logger *slog.Logger,
	stack auto.Stack,
	onOutput func(context.Context, *slog.Logger, map[string]any) error,
) error {
	outputs, err := stack.Outputs(ctx)
	if err != nil {
		return err
	}

	mapped := OutputsToMap(outputs)
	if !hasOutputsToSync(mapped) {
		logger.InfoContext(ctx, "stack has no outputs yet (never deployed); gen sync skipped for this preview")

		return nil
	}

	return onOutput(ctx, logger, mapped)
}

// hasOutputsToSync reports whether a preview has anything for the gen hook.
func hasOutputsToSync(outputs map[string]any) bool {
	return len(outputs) > 0
}
