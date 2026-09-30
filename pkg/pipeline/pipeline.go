// Package pipeline provides a generic DAG-based pipeline orchestrator.
//
// It models step execution as a directed acyclic graph (DAG) rather than a flat list,
// supporting multiple step types (Pulumi, Exec, Confirm) with per-step action
// capabilities, dependency resolution, and CLI generation.
//
// The core package has zero dependencies on Pulumi, Helm, or Kustomize runtime packages.
// Step-type-specific logic lives in sub-packages: pulumistep/.
package pipeline

import (
	"context"
	"log/slog"
)

type (
	// Action represents the operation mode for a pipeline step.
	Action int

	// ActionSet declares which actions a step supports via a bitmask.
	ActionSet uint8

	// StepKind classifies a step for display and filtering.
	StepKind int

	// StepResult holds optional structured output from a step execution.
	// Pulumi steps populate ChangeSummary; non-Pulumi steps return nil.
	StepResult struct {
		StepName      string         // step that produced this result
		ChangeSummary map[string]int // keyed by operation: "create", "update", "delete", "replace", "same"
	}

	// StepFailure records a step that failed during a continue-on-error run.
	// Only ActionDryRun (diff) runs steps to completion after a failure;
	// deploy/destroy/refresh remain fail-fast and never produce these.
	StepFailure struct {
		StepName string
		Err      error
	}

	// StepFunc is the function signature for pipeline steps.
	// Returns an optional StepResult (nil for steps without structured output) and an error.
	StepFunc func(ctx context.Context, logger *slog.Logger, repoRoot string, action Action) (*StepResult, error)

	// Step represents a single pipeline step as a node in the execution DAG.
	Step struct {
		Name      string    // unique step identifier
		Kind      StepKind  // classification for display/filtering
		Fn        StepFunc  // function to execute
		Actions   ActionSet // which actions this step supports
		DependsOn []string  // names of steps this step depends on (DAG edges)
		WorkDir   string    // optional: relative path from repo root
		StackName string    // optional: identifier for display/metadata
	}

	// BeforeRunFunc is an optional pre-flight callback invoked before step execution.
	// It receives the repo root and the action being performed.
	// Returning an error aborts the pipeline before any step runs.
	BeforeRunFunc func(ctx context.Context, logger *slog.Logger, repoRoot string, action Action) error

	// Config defines a pipeline scope.
	Config struct {
		Name      string // CLI binary name (e.g., "deploy-infra")
		Usage     string // CLI usage description
		Root      string // checkout root; empty means the nearest ancestor holding go.mod
		Steps     []Step
		BeforeRun BeforeRunFunc // optional pre-flight hook (e.g., render CRDs)
	}

	// Scope defines a named group of steps that becomes a CLI subcommand.
	Scope struct {
		Name      string // CLI subcommand name (e.g., "network", "apps")
		Usage     string // CLI usage description
		Steps     []Step
		BeforeRun BeforeRunFunc // optional pre-flight hook (e.g., render CRDs)
	}

	// MultiConfig defines a CLI with multiple scope subcommands.
	MultiConfig struct {
		Name   string // CLI binary name (e.g., "deploy")
		Usage  string // CLI usage description
		Root   string // checkout root handed to every scope; empty means the nearest ancestor holding go.mod
		Scopes []Scope
	}

	// ListMetadataFunc is an optional hook for enriching ListSteps output
	// with per-step metadata (e.g., Pulumi project name, backend info).
	ListMetadataFunc func(repoRoot string, step *Step) (project, stack, backend string)
)

const (
	// ActionDryRun shows what would change (diff / preview).
	ActionDryRun Action = iota
	// ActionExecute applies changes (deploy / up).
	ActionExecute
	// ActionSync reconciles state with actual system (refresh).
	ActionSync
	// ActionDestroy tears down resources.
	ActionDestroy
)

const (
	// ActionPreview is a backward-compatible alias for ActionDryRun.
	ActionPreview = ActionDryRun
	// ActionUp is a backward-compatible alias for ActionExecute.
	ActionUp = ActionExecute
	// ActionRefresh is a backward-compatible alias for ActionSync.
	ActionRefresh = ActionSync
)

const (
	// StepPulumi is a Pulumi stack operation.
	StepPulumi StepKind = iota
	// StepExec is a Go function (universal escape hatch).
	StepExec
	// StepConfirm is an interactive confirmation gate.
	StepConfirm
)

const (
	// CanDryRun indicates a step supports ActionDryRun.
	CanDryRun ActionSet = 1 << iota
	// CanExecute indicates a step supports ActionExecute.
	CanExecute
	// CanSync indicates a step supports ActionSync.
	CanSync
	// CanDestroy indicates a step supports ActionDestroy.
	CanDestroy

	unknownStr = "unknown"
)

// String returns the human-readable name for an Action.
func (a Action) String() string {
	switch a {
	case ActionDryRun:
		return "dry-run"
	case ActionExecute:
		return "execute"
	case ActionSync:
		return "sync"
	case ActionDestroy:
		return "destroy"
	default:
		return unknownStr
	}
}

// String returns the name for a StepKind.
func (k StepKind) String() string {
	switch k {
	case StepPulumi:
		return "pulumi"
	case StepExec:
		return "exec"
	case StepConfirm:
		return "confirm"
	default:
		return unknownStr
	}
}

// AllActions returns an ActionSet containing all four actions.
func AllActions() ActionSet {
	return NewActionSet(ActionDryRun, ActionExecute, ActionSync, ActionDestroy)
}

// DefaultExecActions returns the default ActionSet for ExecStep (DryRun + Execute).
func DefaultExecActions() ActionSet {
	return NewActionSet(ActionDryRun, ActionExecute)
}

// DefaultConfirmActions returns the default ActionSet for ConfirmStep (DryRun + Execute).
func DefaultConfirmActions() ActionSet {
	return NewActionSet(ActionDryRun, ActionExecute)
}

// NewActionSet creates an ActionSet from the given actions.
func NewActionSet(actions ...Action) ActionSet {
	var s ActionSet

	for _, a := range actions {
		s |= 1 << uint8(a)
	}

	return s
}

// Supports returns true if the ActionSet includes the given action.
func (s ActionSet) Supports(action Action) bool {
	return s&(1<<uint8(action)) != 0
}
