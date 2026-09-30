# pulumi-pipeline

**A DAG of Pulumi stacks with the refusals a shared infrastructure repository learns the hard way.**

Runs many Pulumi stacks as one pipeline: steps discovered from the stack
files a repository already commits, dependency edges between them, one
command for `diff`, `deploy`, `refresh`, `destroy` and a health check —
and gates that refuse to apply from a stale checkout, that fail an apply
whose refresh-preview wants to destroy something, that turn any preview
change into a failure on demand, and that put a clock on every step.

| What | Where |
| --- | --- |
| Go module `github.com/truvity/pulumi-pipeline` | `go get github.com/truvity/pulumi-pipeline@v0.1.0` |
| `pkg/pipeline` — the engine: steps, DAG, typed steps, run, gates, CLI commands | Go package, no Pulumi dependency |
| `pkg/pulumistep` — the Automation API step, scope discovery, output hooks, health check | Go package |
| `pkg/pulumicli` — the command line, assembled from the two above | Go package |
| `pulumi-pipeline` — the command | release archives, or `go install github.com/truvity/pulumi-pipeline/cmd/pulumi-pipeline@v0.1.0` |

## Who it is for

A repository that holds several Pulumi programs, each with several stacks,
and wants to preview and apply them from one place instead of `cd`-ing into
each directory. It assumes the `pulumi` CLI and the language plugin of your
programs are on `PATH`, that stacks live in a backend you have already
logged in to (or named through `PULUMI_BACKEND_URL`), and that every stack
you want managed has a committed `Pulumi.<stack>.yaml`.

It deliberately does not install Pulumi, choose a backend or a secrets
provider, write your stack outputs anywhere (a hook is how you do that), or
know what your programs deploy. It is not a replacement for `pulumi` — every
step is the Automation API's `preview`, `up`, `refresh` or `destroy` on a
stack you could run by hand.

## The model

Three nouns.

A **step** is one unit of work with a name, the actions it supports (diff,
deploy, refresh, destroy) and edges to the steps it depends on. A Pulumi
step is one stack of one program.

A **scope** is a named group of steps that becomes a subcommand. The
command discovers one scope per subdirectory of the scopes directory you
name, and one step per `Pulumi.<stack>.yaml` in it. Nothing is registered:
adding a stack file is enough.

A **gate** is a refusal the runner applies around your steps: on the
checkout before a mutating run, on each step's clock, on a preview's
result, and on the refresh-preview after an apply. Gates are on by default;
the ones that can be overridden say so and log loudly when they are. The
checkout gate relaxes on its own under `CI=true` (a runner's detached HEAD
has no upstream) but still refuses a dirty tree; a local shell exporting
`CI=true` bypasses the same checks ([safety](docs/safety.md#the-checkout-gate)).

Steps that carry no edges run in name order; steps that do run in
topological order (reverse for `destroy`). A dry run continues past a
failing step and reports every failure at the end; every other action stops
at the first.

## Install and a worked example

```sh
go install github.com/truvity/pulumi-pipeline/cmd/pulumi-pipeline@v0.1.0
```

A repository laid out like this:

```text
deploy/
  network/
    Pulumi.yaml
    Pulumi.dev.yaml
    Pulumi.prod.yaml
    main.go
  apps/
    Pulumi.yaml
    Pulumi.dev.yaml
    main.go
```

is driven with:

```sh
pulumi-pipeline --scopes-dir deploy list                    # the scopes
pulumi-pipeline --scopes-dir deploy network list            # its steps
pulumi-pipeline --scopes-dir deploy network diff --all      # preview every stack
pulumi-pipeline --scopes-dir deploy network deploy dev      # apply one stack
pulumi-pipeline --scopes-dir deploy network diff --all --expect-no-changes
```

Name the directory once instead of on every call: `export
PULUMI_PIPELINE_SCOPES_DIR=deploy`, or commit `.pulumi-pipeline.yaml`
containing `scopes_dir: deploy`. The flag wins over the environment, which
wins over the file; there is no built-in default, because a directory name
that fits one repository finds nothing in the next.

To persist stack outputs, write a `main` of your own:

```go
func main() {
	os.Exit(pulumicli.Main(context.Background(), os.Args, pulumicli.Options{
		Name:      "infra",
		ScopesDir: "deploy",
		Hooks: pulumistep.OutputHooksFunc(func(root, scope, stack string) pulumistep.OutputFunc {
			return func(ctx context.Context, logger *slog.Logger, outputs map[string]any) error {
				return writeOutputs(root, scope, stack, outputs) // yours
			}
		}),
	}))
}
```

Secret outputs never reach the hook.

## Consumers

The estate this was extracted from drives its infrastructure repository
through `pkg/pulumicli` with an output hook of its own (the Go library
surface). No other consumer is recorded at v0; a repository that adopts it
adds a line here.

## Neighbours

- [github-structure](https://github.com/truvity/github-structure) — a Pulumi
  engine for a GitHub organization. It is a program this runner can run; it
  has no dependency on it.
- [policy](https://github.com/truvity/policy) — the contracts this repository
  is held to (`docs/contracts/component.md`).
- [ci-workflows](https://github.com/truvity/ci-workflows) — the shared CI and
  release workflows this repository calls.

## Documentation

- [docs/adoption.md](docs/adoption.md) — installing, laying out a repository,
  turning the gates on, and every breaking upgrade.
- [docs/reference.md](docs/reference.md) — every command, flag, environment
  variable, Go type and exit behaviour.
- [docs/safety.md](docs/safety.md) — each refusal and the failure that earned
  it.
- [docs/doctrine.md](docs/doctrine.md) — why it is shaped this way.
- [docs/decisions/](docs/decisions/) — the decisions, one page each.

## The rule that makes this repository public

Mechanism only. A scopes directory, an output store, a backend, a
credential and every name in them are the caller's: a flag, an environment
variable, a file the caller commits, or a hook the caller writes. Nothing
here defaults to one estate's value.
[`hack/leak-canary.sh`](hack/leak-canary.sh) enforces the mechanical half
over tracked files and runs in the gate.

## Status

v0: the API may still move in a minor release, and every move is a
`Breaking:` bullet in the [CHANGELOG](CHANGELOG.md). The engine and the
Pulumi step are the code an estate has run for months; the four gates
(checkout, post-apply refresh-preview, `--expect-no-changes`, step timeout)
are new and are exercised by the tests here, including against a real
Pulumi stack on a local file backend. Nothing in the tests touches a cloud.

## Development

```sh
devbox shell        # pins every tool, including the pulumi CLI the tests drive
just check          # build, test, lint, leak-canary
just vuln           # reachable Go advisories (its own workflow in CI, not in check)
```

The tests need `pulumi` and `pulumi-language-go` on `PATH` and fail, rather
than skip, without them. They use a `file://` backend in a temporary
directory and a tiny Go program that registers component resources, which
the engine tracks itself, so there is no provider and no network beyond
module downloads.

## Releasing

Releases are a `v*` tag, which the shared release workflow turns into the
Go module version and a GitHub Release with the `pulumi-pipeline` archives.
Automatic patch releases are not armed; the first release and every minor
and major are hand-cut tags.

## Licence

MIT. See [LICENSE](LICENSE).
