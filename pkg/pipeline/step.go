package pipeline

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

type (
	// ExecOption configures an ExecStep.
	ExecOption func(*Step)
)

// WithActions overrides the default ActionSet for an ExecStep.
func WithActions(actions ActionSet) ExecOption {
	return func(s *Step) {
		s.Actions = actions
	}
}

// WithDependsOn sets the step's dependencies.
func WithDependsOn(deps ...string) ExecOption {
	return func(s *Step) {
		s.DependsOn = deps
	}
}

// ExecStep creates a Step with Kind StepExec that delegates to the provided StepFunc.
// Default ActionSet: DryRun + Execute. Override with WithActions.
func ExecStep(name string, fn StepFunc, opts ...ExecOption) Step {
	s := Step{
		Name:    name,
		Kind:    StepExec,
		Fn:      fn,
		Actions: DefaultExecActions(),
	}

	for _, opt := range opts {
		opt(&s)
	}

	return s
}

// ConfirmStep creates an interactive confirmation gate step.
// Kind: StepConfirm, ActionSet: DryRun + Execute.
//
// Behavior per action:
//   - DryRun: log the confirmation message without prompting
//   - Execute: prompt on stdin, return error if declined
//   - Sync/Destroy: skip without error (not in ActionSet)
func ConfirmStep(name, message string) Step {
	return Step{
		Name:    name,
		Kind:    StepConfirm,
		Fn:      confirmStepFn(message),
		Actions: DefaultConfirmActions(),
	}
}

// confirmStepFn returns a StepFunc that prompts the user for confirmation.
func confirmStepFn(message string) StepFunc {
	return func(ctx context.Context, logger *slog.Logger, _ string, action Action) (*StepResult, error) {
		if action == ActionDryRun {
			logger.InfoContext(ctx, "would prompt for confirmation",
				slog.String("message", message),
			)

			return nil, nil
		}

		if action != ActionExecute {
			// Sync and Destroy: skip without error.
			return nil, nil
		}

		if _, err := fmt.Fprintln(os.Stderr, message); err != nil {
			return nil, fmt.Errorf("write confirmation message: %w", err)
		}

		if _, err := fmt.Fprint(os.Stderr, "[y/N] "); err != nil {
			return nil, fmt.Errorf("write confirmation prompt: %w", err)
		}

		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return nil, fmt.Errorf("read confirmation input: %w", err)
			}

			return nil, fmt.Errorf("confirmation aborted: no input")
		}

		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if answer != "y" && answer != "yes" {
			return nil, fmt.Errorf("confirmation declined by user")
		}

		return nil, nil
	}
}

// GenerateStep creates an ExecStep with a "generate/" name prefix.
// This is a convenience wrapper for backward compatibility with the old
// pulumiautomation.GenerateStep API. The step has Kind StepExec and
// default ActionSet (DryRun + Execute).
func GenerateStep(name string, fn StepFunc) Step {
	return ExecStep("generate/"+name, fn)
}
