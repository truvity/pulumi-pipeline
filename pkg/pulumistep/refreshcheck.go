package pulumistep

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
)

// destructiveOps are the preview operations a stack that was just applied must
// not be planning: an apply that leaves the next preview wanting to delete or
// replace something did not converge, and the next person to run it destroys
// what the apply appeared to build.
var destructiveOps = []apitype.OpType{
	apitype.OpDelete,
	apitype.OpReplace,
	apitype.OpCreateReplacement,
	apitype.OpDeleteReplaced,
}

// destructiveChanges lists the destructive operations in a change summary as
// "op=count", sorted, or nil when there are none.
func destructiveChanges(changes map[apitype.OpType]int) []string {
	var found []string

	for _, op := range destructiveOps {
		if n := changes[op]; n > 0 {
			found = append(found, fmt.Sprintf("%s=%d", op, n))
		}
	}

	sort.Strings(found)

	return found
}

// verifyAfterApply runs a refresh-preview after a successful apply and fails if
// it plans to delete or replace anything.
//
// The preview refreshes first, so it compares the program with what the
// provider says exists NOW, not with what the state remembers. That is the
// point: state written by the apply just agrees with itself.
func verifyAfterApply(ctx context.Context, logger *slog.Logger, stack auto.Stack, stackName string) error {
	var progress bytes.Buffer

	prev, err := stack.Preview(ctx,
		optpreview.Refresh(),
		optpreview.ProgressStreams(&progress),
		optpreview.ErrorProgressStreams(io.Discard),
	)
	if err != nil {
		_, _ = fmt.Fprint(os.Stderr, progress.String())

		return fmt.Errorf("post-apply refresh-preview of %s: %w", stackName, err)
	}

	if found := destructiveChanges(prev.ChangeSummary); len(found) > 0 {
		_, _ = fmt.Fprint(os.Stdout, progress.String())

		return fmt.Errorf("post-apply refresh-preview of %s plans to destroy: %s "+
			"(the apply completed; the stack does not match its program)",
			stackName, strings.Join(found, " "))
	}

	logger.InfoContext(ctx, "post-apply refresh-preview clean", slog.String("stack", stackName))

	return nil
}
