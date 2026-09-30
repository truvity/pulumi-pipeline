# Adoption

## Prerequisites

- The `pulumi` CLI and the language plugin your programs use, on `PATH`.
- A backend you are logged in to, or `PULUMI_BACKEND_URL` set, and whatever
  your secrets provider needs. The pipeline never chooses either.
- A `git` checkout with an upstream, if you will use `deploy` or `destroy`
  (see [the checkout gate](safety.md#the-checkout-gate)).

## Lay the repository out

One directory per Pulumi program under a single parent, with the stacks you
want managed committed as `Pulumi.<stack>.yaml` files beside the program's
`Pulumi.yaml`:

```text
deploy/<scope>/Pulumi.yaml
deploy/<scope>/Pulumi.<stack>.yaml
```

A subdirectory with no stack file is not a scope. `Pulumi.yaml` itself is the
project file and is never mistaken for a stack.

## Name the scopes directory

Any one of these, in this order of precedence:

1. `--scopes-dir deploy` (must come before the scope name).
2. `PULUMI_PIPELINE_SCOPES_DIR=deploy`.
3. `scopes_dir: deploy` in `.pulumi-pipeline.yaml` at the checkout root.
   Unknown keys in that file are an error.

There is no default. A tool that is run from a script must not depend on a
directory called `deploy` existing.

`--root` names the checkout root; the default is the nearest ancestor of the
working directory that holds a `.git` entry.

## The first run

```sh
pulumi-pipeline list                      # the scopes it found
pulumi-pipeline <scope> list              # the steps, in execution order
pulumi-pipeline <scope> doctor            # stack locks and pending operations
pulumi-pipeline <scope> diff --all        # a preview of everything
```

`diff` is safe to run anywhere: it does not go through the checkout gate,
and it continues past a failing step so one broken stack does not hide drift
in the rest.

## Turn the gates into habit

- Run `diff --all --expect-no-changes` in CI on a schedule to find drift, and
  before an alias migration to prove the aliases moved state without touching
  the resource (see [reference](reference.md#diff)).
- Leave `--step-timeout` at its default until you have measured your slowest
  stack, then set it to a comfortable multiple.
- In CI, rely on `CI=true` (set by your CI system) rather than
  `--allow-stale-checkout`: it skips only the upstream comparison a detached
  HEAD cannot satisfy and still refuses a dirty tree. Never export `CI=true`
  in a developer shell.
- Do not script `--allow-stale-checkout`. It exists for the person who has
  read the refusal and means it.

## Persisting stack outputs

Write a `main` of your own that calls `pulumicli.Main` with an `OutputHooks`
value (see the README's worked example). The hook is called after a deploy
with the stack's non-secret outputs, and after a diff for a stack that
already has outputs. A stack for which `OutputsFor` returns nil persists
nothing, which is how a caller makes enrolment explicit: return a callback
only for the stacks that opted in.

## Upgrading

At v0 a minor release may change the API. Every change that requires
action is a **Breaking:** bullet in the [CHANGELOG](../CHANGELOG.md) that
names the step to take; read every entry between your pin and the target.
There are no breaking upgrades yet.
