package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/urfave/cli/v3"
)

type (
	// CheckFunc is a pluggable health check function for the doctor subcommand.
	CheckFunc func(ctx context.Context, logger *slog.Logger, cfg Config) error
)

// BuildPipelineCommands returns one *cli.Command per scope in cfg.Scopes.
// Each scope command has subcommands: list, diff, deploy, destroy, refresh, doctor.
func BuildPipelineCommands(
	cfg MultiConfig,
	logger *slog.Logger,
	checkFn CheckFunc,
	metadataFn ListMetadataFunc,
) []*cli.Command {
	commands := make([]*cli.Command, 0, len(cfg.Scopes))

	for i := range cfg.Scopes {
		scope := &cfg.Scopes[i]
		scopeCfg := Config{
			Name:      cfg.Name + " " + scope.Name,
			Usage:     scope.Usage,
			Root:      cfg.Root,
			Steps:     scope.Steps,
			BeforeRun: scope.BeforeRun,
		}

		commands = append(commands, &cli.Command{
			Name:     scope.Name,
			Usage:    scope.Usage,
			Commands: buildScopeActions(scopeCfg, logger, checkFn, metadataFn),
		})
	}

	return commands
}

// BuildListCommand returns a CLI command that lists all pipeline/scope names.
func BuildListCommand(cfg MultiConfig) *cli.Command {
	return &cli.Command{
		Name:    "list",
		Aliases: []string{"ls"},
		Usage:   "List all pipelines",
		Action: func(_ context.Context, _ *cli.Command) error {
			_, _ = fmt.Fprintf(os.Stdout, "\n%-20s %s\n", "PIPELINE", "DESCRIPTION")
			_, _ = fmt.Fprintf(os.Stdout, "%-20s %s\n", "--------", "-----------")

			for i := range cfg.Scopes {
				_, _ = fmt.Fprintf(os.Stdout, "%-20s %s\n",
					cfg.Scopes[i].Name, cfg.Scopes[i].Usage)
			}

			_, _ = fmt.Fprintln(os.Stdout)

			return nil
		},
	}
}

// buildScopeActions creates the standard set of subcommands for a scope config.
func buildScopeActions(
	cfg Config,
	logger *slog.Logger,
	checkFn CheckFunc,
	metadataFn ListMetadataFunc,
) []*cli.Command {
	return []*cli.Command{
		{
			Name:    "list",
			Aliases: []string{"ls"},
			Usage:   "List all steps in execution order",
			Action: func(_ context.Context, _ *cli.Command) error {
				return ListSteps(cfg, metadataFn)
			},
		},
		{
			Name:  "graph",
			Usage: "Generate interactive DAG visualization (HTML)",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "Write HTML to this path instead of opening browser"},
			},
			Action: func(_ context.Context, cmd *cli.Command) error {
				return GraphCommand(cfg, cmd.String("output"))
			},
		},
		{
			Name:  "diff",
			Usage: "Show what would change without deploying",
			Flags: append([]cli.Flag{
				&cli.BoolFlag{Name: "all", Usage: "Run all steps"},
				&cli.BoolFlag{Name: "exclusive", Aliases: []string{"e"}, Usage: "Run only listed steps (no dependency resolution)"},
				&cli.BoolFlag{Name: "detail", Usage: "Show detailed resource-level diff"},
				&cli.BoolFlag{Name: "show-providers", Usage: "Show pulumi:providers:* version-only updates (suppressed by default)"},
				&cli.BoolFlag{Name: "expect-no-changes", Usage: "Fail if any step reports a change (for alias migrations)"},
			}, timeoutFlag()),
			Action: func(ctx context.Context, cmd *cli.Command) error {
				return RunWith(ctx, logger, cfg, ActionDryRun, cmd.Args().Slice(), RunOptions{
					Exclusive:       cmd.Bool("exclusive"),
					All:             cmd.Bool("all"),
					Detail:          cmd.Bool("detail"),
					ShowProviders:   cmd.Bool("show-providers"),
					ExpectNoChanges: cmd.Bool("expect-no-changes"),
					StepTimeout:     stepTimeoutFrom(cmd),
				})
			},
		},
		mutatingCommand(cfg, logger, "deploy", "Deploy changes", ActionExecute),
		mutatingCommand(cfg, logger, "destroy", "Tear down resources", ActionDestroy),
		mutatingCommand(cfg, logger, "refresh", "Refresh state", ActionSync),
		{
			Name:  "doctor",
			Usage: "Verify health of all stacks/resources",
			Action: func(ctx context.Context, _ *cli.Command) error {
				if checkFn == nil {
					logger.InfoContext(ctx, "no health check configured")

					return nil
				}

				return checkFn(ctx, logger, cfg)
			},
		},
	}
}

// mutatingCommand builds deploy, destroy and refresh, which share their flags
// and their gates.
func mutatingCommand(cfg Config, logger *slog.Logger, name, usage string, action Action) *cli.Command {
	return &cli.Command{
		Name:  name,
		Usage: usage,
		Flags: append(actionFlags(), timeoutFlag(),
			&cli.BoolFlag{
				Name:  "allow-stale-checkout",
				Usage: "Proceed although the checkout is dirty, behind its upstream or unverifiable (logged loudly)",
			}),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return RunWith(ctx, logger, cfg, action, cmd.Args().Slice(), RunOptions{
				Exclusive:          cmd.Bool("exclusive"),
				All:                cmd.Bool("all"),
				StepTimeout:        stepTimeoutFrom(cmd),
				AllowStaleCheckout: cmd.Bool("allow-stale-checkout"),
			})
		},
	}
}

// timeoutFlag is the per-step timeout flag; 0 turns the bound off.
func timeoutFlag() cli.Flag {
	return &cli.DurationFlag{
		Name:  "step-timeout",
		Value: DefaultStepTimeout,
		Usage: "Fail a step that runs longer than this (0 disables)",
	}
}

// stepTimeoutFrom maps the flag onto RunOptions: the flag's 0 means "off",
// which RunOptions spells as a negative value.
func stepTimeoutFrom(cmd *cli.Command) time.Duration {
	d := cmd.Duration("step-timeout")
	if d == 0 {
		return -1
	}

	return d
}

// actionFlags returns the common flags for action subcommands.
func actionFlags() []cli.Flag {
	return []cli.Flag{
		&cli.BoolFlag{Name: "all", Usage: "Run all steps"},
		&cli.BoolFlag{Name: "exclusive", Aliases: []string{"e"}, Usage: "Run only listed steps (no dependency resolution)"},
	}
}
