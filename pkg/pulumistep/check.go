package pulumistep

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"

	"github.com/truvity/pulumi-pipeline/pkg/pipeline"
)

type (
	// CheckResult holds the health status of a single stack.
	CheckResult struct {
		Stack            string
		Exists           bool
		UpdateInProgress bool
		PendingOps       int
		ResourceCount    int
	}
)

// CheckStacks verifies the health of all Pulumi stacks in the config.
// Returns an error if any stack has issues that would block deployment.
func CheckStacks(ctx context.Context, logger *slog.Logger, cfg pipeline.Config) error {
	repoRoot, err := cfg.RootDir()
	if err != nil {
		return fmt.Errorf("find repo root: %w", err)
	}

	var (
		results []CheckResult
		issues  int
	)

	seen := make(map[string]bool)

	for i := range cfg.Steps {
		s := &cfg.Steps[i]
		if s.Kind != pipeline.StepPulumi {
			continue
		}

		// Deduplicate by (WorkDir, StackName).
		key := s.WorkDir + "/" + s.StackName
		if seen[key] {
			continue
		}

		seen[key] = true

		workDir := filepath.Join(repoRoot, s.WorkDir)
		result := checkStack(ctx, logger, workDir, s.StackName)
		results = append(results, result)

		if result.UpdateInProgress || result.PendingOps > 0 {
			issues++
		}
	}

	printCheckResults(results)

	if issues > 0 {
		return fmt.Errorf("%d stack(s) have issues — resolve before deploying", issues)
	}

	logger.InfoContext(ctx, "all stacks healthy")

	return nil
}

func printCheckResults(results []CheckResult) {
	_, _ = fmt.Fprintf(os.Stdout,
		"\n%-20s %-8s %-10s %-12s %s\n",
		"STACK", "EXISTS", "LOCKED", "PENDING", "RESOURCES")
	_, _ = fmt.Fprintf(os.Stdout,
		"%-20s %-8s %-10s %-12s %s\n",
		"-----", "------", "------", "-------", "---------")

	for i := range results {
		r := &results[i]

		exists := "no"
		if r.Exists {
			exists = "yes"
		}

		locked := "no"
		if r.UpdateInProgress {
			locked = "YES ⚠️"
		}

		pending := "0"
		if r.PendingOps > 0 {
			pending = fmt.Sprintf("%d ⚠️", r.PendingOps)
		}

		resources := "-"
		if r.Exists {
			resources = fmt.Sprintf("%d", r.ResourceCount)
		}

		_, _ = fmt.Fprintf(os.Stdout,
			"%-20s %-8s %-10s %-12s %s\n",
			r.Stack, exists, locked, pending, resources)
	}

	_, _ = fmt.Fprintln(os.Stdout)
}

func checkStack(ctx context.Context, logger *slog.Logger, workDir, stackName string) CheckResult {
	result := CheckResult{Stack: stackName}

	stack, err := auto.SelectStackLocalSource(ctx, stackName, workDir)
	if err != nil {
		logger.WarnContext(ctx, "stack not found",
			slog.String("stack", stackName),
			slog.Any("error", err),
		)

		return result
	}

	result.Exists = true

	info, err := stack.Info(ctx)
	if err != nil {
		logger.WarnContext(ctx, "failed to get stack info",
			slog.String("stack", stackName),
			slog.Any("error", err),
		)

		return result
	}

	result.UpdateInProgress = info.UpdateInProgress

	if info.ResourceCount != nil {
		result.ResourceCount = *info.ResourceCount
	}

	result.PendingOps = countPendingOperations(ctx, logger, stack, stackName)

	return result
}

func countPendingOperations(ctx context.Context, logger *slog.Logger, stack auto.Stack, stackName string) int {
	exported, err := stack.Export(ctx)
	if err != nil {
		logger.WarnContext(ctx, "failed to export stack state",
			slog.String("stack", stackName),
			slog.Any("error", err),
		)

		return 0
	}

	var deployment apitype.DeploymentV3
	if err := json.Unmarshal(exported.Deployment, &deployment); err != nil {
		logger.WarnContext(ctx, "failed to parse stack deployment",
			slog.String("stack", stackName),
			slog.Any("error", err),
		)

		return 0
	}

	return len(deployment.PendingOperations)
}
