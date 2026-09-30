# Reference

## Command line

```text
pulumi-pipeline [--root DIR] [--scopes-dir DIR] <command>
```

| Global flag | Meaning |
| --- | --- |
| `--root DIR` | The checkout root. Default: the nearest ancestor of the working directory holding `.git`. |
| `--scopes-dir DIR` | The directory, relative to the root, whose subdirectories are scopes. Must precede the scope name. See below for the other ways to name it. |

The scopes directory is taken from the first of: `--scopes-dir`; the
environment variable `PULUMI_PIPELINE_SCOPES_DIR`; `scopes_dir:` in
`.pulumi-pipeline.yaml` at the root; the default a program built on
`pulumicli` compiled in. With none, the command exits 1 saying so.

| Command | Meaning |
| --- | --- |
| `list` (`ls`) | The scopes and how many stacks each holds. |
| `<scope> list` | The scope's steps in execution order, with project, stack and backend read from the stack files. |
| `<scope> graph [-o FILE]` | An HTML view of the scope's DAG. |
| `<scope> diff [steps…]` | Preview. |
| `<scope> deploy [steps…]` | Apply. |
| `<scope> refresh [steps…]` | Refresh state from the provider. |
| `<scope> destroy [steps…]` | Tear down. |
| `<scope> doctor` | Report stack locks and pending operations; exit 1 if any stack has one. |

Every action command takes step names, or `--all`. Without `--exclusive`,
naming a step also runs what it depends on (for `destroy`, what depends on
it). With `--exclusive` (`-e`), only the named steps run. With neither
names nor `--all` the command exits 1.

### diff

| Flag | Meaning |
| --- | --- |
| `--all` | Every step. |
| `--exclusive`, `-e` | Only the named steps. |
| `--detail` | A resource-level summary after the run. |
| `--show-providers` | Keep `pulumi:providers:*` blocks whose only change is the plugin version. They are hidden by default because they drown real drift. This filters what is printed; the preview always runs in full. |
| `--expect-no-changes` | Fail any step whose preview reports a change. Only `same` and `read` are quiet. A provider-version bump counts as a change here, since it is one. |
| `--step-timeout D` | Per-step clock (see below). |

`diff` is continue-on-error: it runs every step, prints a failure summary and
exits 1 if any failed.

### deploy, refresh, destroy

| Flag | Meaning |
| --- | --- |
| `--all`, `--exclusive`, `-e` | As above. |
| `--step-timeout D` | Per-step clock. |
| `--allow-stale-checkout` | (`deploy`, `destroy`, `refresh`; only the first two are gated.) Proceed although the checkout is dirty, behind its upstream or unverifiable. Each reason is logged at WARN. |

Environment: `CI=true` relaxes the checkout gate without the flag, skipping
only the upstream comparison (detached HEAD or no upstream) and logging each
skipped check at WARN; a dirty tree is still refused. A local shell that
exports `CI=true` bypasses the same checks. See
[safety](safety.md#the-checkout-gate).



These are fail-fast: the first failing step stops the run and the error is
`step <name>: <cause>`.

### The step clock

`--step-timeout` is a Go duration; the default is 30 minutes and `0`
disables it. It bounds each step separately, including the post-apply
refresh-preview. When it expires the step's context is cancelled, which
makes the Automation API stop the `pulumi` process and release the stack
lock. A step that has not returned 30 seconds after that is abandoned and
reported as timed out.

## Go API

### `pkg/pipeline`

| Symbol | Meaning |
| --- | --- |
| `Step`, `StepFunc`, `StepResult` | A step, the function it runs, and its optional change summary keyed by operation (`create`, `update`, `delete`, `replace`, `same`, …). |
| `Config`, `Scope`, `MultiConfig` | One scope's steps; a named scope; a set of scopes. `Config.Root` and `MultiConfig.Root` name the checkout root (default: the nearest ancestor with `go.mod`). |
| `ExecStep`, `ConfirmStep`, `GenerateStep`, `WithActions`, `WithDependsOn` | Step constructors and options. |
| `TypedRun`, `TypedProduce`, `TypedConsume`, `TypedTransform`, `Deferred[T]` | Steps whose typed outputs feed later steps and create the DAG edges. |
| `TopologicalSort`, `ResolveMultiSubgraph`, `ResolveMultiDependents` | DAG operations. |
| `Run`, `RunWith`, `RunOptions` | Execute a scope. `RunWith` takes the options: `Exclusive`, `All`, `Detail`, `ShowProviders`, `StepTimeout`, `ExpectNoChanges`, `AllowStaleCheckout`. `Run` is `RunWith` with the defaults. |
| `CheckCheckout` | The checkout gate as a function: returns the reasons a checkout is not safe to apply from. It is always the strict gate; the `CI=true` relaxation applies only inside `RunWith`. |
| `BuildPipelineCommands`, `BuildListCommand` | The command tree over a `MultiConfig`. |
| `DefaultStepTimeout` | 30 minutes. |

### `pkg/pulumistep`

| Symbol | Meaning |
| --- | --- |
| `PulumiStep(Config)` | A step running one stack: `diff` is `preview`, `deploy` is `up`, then the post-apply refresh-preview. |
| `Config` | `WorkDir` (relative to the root), `StackName`, optional `Name`, `OnOutput`, `OnDestroy`. |
| `Discover(DiscoverOptions)` | Scopes from a directory: `Root`, `ScopesDir`, `Hooks`. |
| `OutputHooks`, `OutputHooksFunc`, `OutputFunc` | Where a caller persists outputs. `OutputsFor(root, scope, stack)` returns a callback or nil. |
| `OutputsToMap` | Stack outputs as a map, secrets dropped. |
| `CheckStacks`, `ListMetadata` | The `doctor` check and the metadata columns of `<scope> list`. |

### `pkg/pulumicli`

`Main(ctx, args, Options) int` builds and runs the command. `Options`:
`Name`, `Usage`, `ScopesDir` (the default when nothing else names one),
`Hooks`, `Stdout`, `Stderr`. Exit code 0 on success, 1 on any error.

## Configuration file

`.pulumi-pipeline.yaml` at the checkout root:

```yaml
scopes_dir: deploy
```

## Environment

| Variable | Meaning |
| --- | --- |
| `PULUMI_PIPELINE_SCOPES_DIR` | The scopes directory, when no flag names one. |
| everything Pulumi reads | `PULUMI_BACKEND_URL`, `PULUMI_CONFIG_PASSPHRASE`, … — the steps are ordinary Automation API calls in the process's environment. |
