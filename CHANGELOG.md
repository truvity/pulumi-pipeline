# Changelog

The first release is v0.1.0 (its heading carries no date until the tag is cut).
Each hand-cut tag gets one heading.
Entries are written for someone deciding whether to bump: what changed for
them, and for anything breaking, what to do.

## v0.3.0

- `pkg/gitutils`: small git helpers (repository root with the `GIT_DIR`
  hook trap handled, branch, tag, commit, dirty checks) with a typed
  `ErrNotInRepo`. Additive.
- `pkg/yamlutils`: deterministic YAML with sorted keys and atomic file writes,
  moved out of the consuming estate. Additive.

## v0.2.0

- Gate: under `CI=true` the checkout gate skips the upstream comparison
  (a runner's detached HEAD has none) instead of refusing, logging each
  skipped check at WARN. A dirty tree, a non-git directory and a branch
  that has an upstream are refused or compared exactly as before, and
  `--allow-stale-checkout` still overrides everything. Nothing to do on a
  runner that exports `CI=true`, which now deploys from a detached HEAD
  without the flag; if you had scripted the flag in CI, you can drop it.
  Caveat: a local shell that exports `CI=true` bypasses the same checks.
  See `docs/decisions/0003-ci-relaxes-the-checkout-gate.md`.

## v0.1.0

- The pipeline engine (`pkg/pipeline`): steps with per-action support, a DAG
  with topological order and transitive dependency and dependent resolution,
  typed steps whose outputs feed later steps, a graph view, and the
  `list`, `graph`, `diff`, `deploy`, `refresh`, `destroy` and `doctor`
  commands per scope.
- The Pulumi step (`pkg/pulumistep`): the Automation API as a step, the
  provider-version-only diff filter, a health check that finds stack locks
  and pending operations, and scope discovery from a directory of programs.
- `pkg/pulumicli` and the `pulumi-pipeline` command: scopes come from
  `--scopes-dir`, `PULUMI_PIPELINE_SCOPES_DIR` or `.pulumi-pipeline.yaml`,
  never from a built-in path; `OutputHooks` is where a caller persists
  stack outputs.
- Gate: `deploy` and `destroy` refuse a dirty checkout or one behind its
  upstream (after fetching it), or one that cannot be verified;
  `--allow-stale-checkout` overrides and warns.
- Gate: every successful apply is followed by a refresh-preview, and the
  step fails if that preview plans any delete or replace.
- Gate: `diff --expect-no-changes` fails any step whose preview reports a
  change.
- Gate: every step runs under `--step-timeout` (30 minutes by default; 0
  disables), and a step that ignores its cancellation is abandoned rather
  than waited on.
