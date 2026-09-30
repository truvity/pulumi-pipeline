# 0003 — CI relaxes the checkout gate

**Status:** Accepted

## Context

The checkout gate ([0001](0001-gates-belong-to-the-runner.md)) refuses a
`deploy` or `destroy` from a checkout that is dirty, behind its upstream, or
cannot be verified. A CI runner checks out a detached HEAD at the commit under
test: there is no branch, so no upstream, so the gate refused every CI run
unless the pipeline passed `--allow-stale-checkout`, a flag documented as being
for a person who has read the refusal. Scripting it into every pipeline
disables the gate entirely, including the check that does hold on a runner.

## Decision

- When the environment variable `CI` is exactly `true`, the gate skips the
  checks that cannot hold on a runner: the comparison with an upstream, which
  is unavailable on a detached HEAD or an unpublished branch. Each skipped
  check is logged at WARN (`CHECKOUT CHECK SKIPPED (CI=true)`) with the
  reason.
- It still refuses a dirty tree and a directory that is not a git checkout,
  and it still fetches and compares a branch that does have an upstream.
- `--allow-stale-checkout` is unchanged and overrides everything.
- The exported function `CheckCheckout` stays strict; only `RunWith` reads the
  environment.
- Only the literal `true` counts: `1`, `TRUE` and `yes` do not.

## Consequences

- A pipeline deploys from a detached HEAD without a flag, and the gate is still
  on for the dirty tree, the one check a runner can fail.
- The trigger is an environment variable, not proof of a runner. A local shell
  that exports `CI=true` bypasses the upstream checks; this is documented, and
  a dirty tree is refused regardless. We accept it: the alternative
  (recognising specific CI systems) is a list that is wrong by construction,
  and the flag it replaces bypassed more.
- On a runner the gate no longer proves the commit is the tip of anything; that
  is the pipeline's job (what it checks out and when).
