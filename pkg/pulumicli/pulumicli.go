// Package pulumicli assembles the command-line tool: scopes discovered from a
// directory, the pipeline's commands over them, and the caller's hooks. The
// cmd/pulumi-pipeline binary is a call to Main with no hooks; a repository
// that wants its stack outputs persisted somewhere writes its own main that
// calls Main with its own OutputHooks.
package pulumicli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"
	"go.yaml.in/yaml/v3"

	"github.com/truvity/pulumi-pipeline/pkg/pipeline"
	"github.com/truvity/pulumi-pipeline/pkg/pulumistep"
)

const (
	// ConfigFile is the optional file at the checkout root that names the
	// scopes directory.
	ConfigFile = ".pulumi-pipeline.yaml"

	// EnvScopesDir names the scopes directory when no flag does.
	EnvScopesDir = "PULUMI_PIPELINE_SCOPES_DIR"

	flagRoot      = "root"
	flagScopesDir = "scopes-dir"
)

type (
	// Options describes one tool built on the library.
	Options struct {
		// Name is the binary name shown in help. Default "pulumi-pipeline".
		Name string
		// Usage is the one-line description. Default is generic.
		Usage string
		// ScopesDir is the default scopes directory, used when neither the
		// flag, the environment nor the config file names one. Empty means one
		// of those is required.
		ScopesDir string
		// Hooks persists stack outputs. Nil persists nothing.
		Hooks pulumistep.OutputHooks
		// Stdout and Stderr default to the process's.
		Stdout, Stderr io.Writer
	}

	// fileConfig is ConfigFile's schema. Unknown keys are refused.
	fileConfig struct {
		ScopesDir string `yaml:"scopes_dir"`
		// Origin and OwnedStacks are the repository's ownership declaration
		// (pipeline.Ownership); absent, neither is checked.
		Origin      string   `yaml:"origin"`
		OwnedStacks []string `yaml:"owned_stacks"`
	}
)

// Main runs the tool against args (os.Args form, program name first) and
// returns the process exit code.
func Main(ctx context.Context, args []string, opts Options) int {
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil))

	if err := run(ctx, logger, args, opts); err != nil {
		logger.ErrorContext(ctx, "pipeline failed", slog.Any("error", err))

		return 1
	}

	return 0
}

func run(ctx context.Context, logger *slog.Logger, args []string, opts Options) error {
	if opts.Name == "" {
		opts.Name = "pulumi-pipeline"
	}

	if opts.Usage == "" {
		opts.Usage = "Multi-stack Pulumi pipeline runner"
	}

	// The scopes become subcommands, so they must be known before the
	// command tree exists, which is before urfave parses any flag. The two
	// flags that decide them are read here first and registered on the root
	// command as well, for help and for the parse that follows.
	pre := preparse(args)

	root, err := resolveRoot(pre.root)
	if err != nil {
		return err
	}

	fc, err := readConfigFile(root)
	if err != nil {
		return err
	}

	scopesDir, err := resolveScopesDir(fc, pre.scopesDir, opts.ScopesDir)
	if err != nil {
		return err
	}

	ownership := pipeline.Ownership{Origin: fc.Origin, Stacks: fc.OwnedStacks}
	if err := ownership.Validate(); err != nil {
		return fmt.Errorf("%s: %w", ConfigFile, err)
	}

	scopes, err := pulumistep.Discover(pulumistep.DiscoverOptions{Root: root, ScopesDir: scopesDir, Hooks: opts.Hooks})
	if err != nil {
		return fmt.Errorf("discovering scopes: %w", err)
	}

	cfg := pipeline.MultiConfig{
		Name:      opts.Name,
		Usage:     fmt.Sprintf("%s (scopes discovered from %s)", opts.Usage, scopesDir),
		Root:      root,
		Scopes:    scopes,
		Ownership: ownership,
	}

	cmd := &cli.Command{
		Name:   cfg.Name,
		Usage:  cfg.Usage,
		Writer: opts.Stdout,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: flagRoot, Usage: "Checkout root (default: the nearest ancestor holding .git)"},
			&cli.StringFlag{
				Name:  flagScopesDir,
				Usage: "Directory whose subdirectories are the scopes, relative to the root (env " + EnvScopesDir + ", then " + ConfigFile + ")",
			},
		},
		Commands: append(
			[]*cli.Command{pipeline.BuildListCommand(cfg)},
			pipeline.BuildPipelineCommands(cfg, logger, pulumistep.CheckStacks, pulumistep.ListMetadata())...,
		),
	}

	return cmd.Run(ctx, args)
}

type preparsed struct{ root, scopesDir string }

// preparse finds --root and --scopes-dir among the leading flags, in both the
// "--flag value" and "--flag=value" forms. It stops at the first argument that
// is not a flag, which is the scope name.
func preparse(args []string) preparsed {
	var p preparsed

	rest := args
	if len(rest) > 0 {
		rest = rest[1:]
	}

	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if !strings.HasPrefix(a, "-") {
			break
		}

		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")

		target := map[string]*string{flagRoot: &p.root, flagScopesDir: &p.scopesDir}[name]
		if target == nil {
			continue
		}

		if !hasValue && i+1 < len(rest) {
			i++
			value = rest[i]
		}

		*target = value
	}

	return p
}

func resolveRoot(flagValue string) (string, error) {
	if flagValue != "" {
		abs, err := filepath.Abs(flagValue)
		if err != nil {
			return "", fmt.Errorf("resolve --%s %q: %w", flagRoot, flagValue, err)
		}

		return abs, nil
	}

	root, err := pipeline.FindGitRoot()
	if err != nil {
		return "", fmt.Errorf("finding the checkout root (pass --%s): %w", flagRoot, err)
	}

	return root, nil
}

// readConfigFile reads ConfigFile at root. A missing file is the zero value;
// an unknown key is an error.
func readConfigFile(root string) (fileConfig, error) {
	var fc fileConfig

	data, err := os.ReadFile(filepath.Join(root, ConfigFile))
	if os.IsNotExist(err) {
		return fc, nil
	}

	if err != nil {
		return fc, fmt.Errorf("read %s: %w", ConfigFile, err)
	}

	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)

	if err := dec.Decode(&fc); err != nil && !errors.Is(err, io.EOF) {
		return fc, fmt.Errorf("%s: %w", ConfigFile, err)
	}

	// An empty owned_stacks decodes exactly as an absent one, and absent
	// turns the ownership check off. Someone who writes the key empty (or
	// comments out every entry) most likely means "owns nothing", the
	// opposite, so the key's presence is checked on its own and an empty
	// one refused.
	if len(fc.OwnedStacks) == 0 && hasKey(data, "owned_stacks") {
		return fc, fmt.Errorf("%s: owned_stacks is empty; list the stacks this repository owns, "+
			"or remove the key to turn the ownership check off", ConfigFile)
	}

	return fc, nil
}

// hasKey reports whether the top-level mapping of the YAML document in data
// holds key, whatever its value, null included.
func hasKey(data []byte, key string) bool {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return false
	}

	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return false
	}

	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return true
		}
	}

	return false
}

// resolveScopesDir applies the precedence flag, environment, config file,
// caller default. There is deliberately no built-in default: a directory name
// that works for one repository would quietly find nothing in the next.
func resolveScopesDir(fc fileConfig, flagValue, fallback string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}

	if v := os.Getenv(EnvScopesDir); v != "" {
		return v, nil
	}

	if fc.ScopesDir != "" {
		return fc.ScopesDir, nil
	}

	if fallback != "" {
		return fallback, nil
	}

	return "", fmt.Errorf("no scopes directory: pass --%s, set %s, or put `scopes_dir:` in %s", flagScopesDir, EnvScopesDir, ConfigFile)
}
