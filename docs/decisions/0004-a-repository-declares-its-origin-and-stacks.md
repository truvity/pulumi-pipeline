# 0004 — A repository declares its origin and the stacks it owns

**Status:** Accepted

## Context

The checkout gate ([0001](0001-gates-belong-to-the-runner.md)) proves that a
checkout is clean and level with its own remote. It cannot tell a checkout of
the right repository from a checkout of a fork, of a copy that seeded a new
repository, or of a repository that has since been renamed or split: each of
those is current with itself. And because scopes and stacks are discovered
from the files in the tree, a stack file that exists in two repositories can
be applied from either, and the stack converges on whichever program ran
last.

## Decision

- A repository may declare its origin (`owner/name`) and the stacks it owns
  (`scope/stack`, each half a `path.Match` pattern): `origin:` and
  `owned_stacks:` in `.pulumi-pipeline.yaml`, or `pipeline.Ownership` for a
  program that builds its own configuration.
- With an origin declared, `deploy` and `destroy` refuse a checkout whose
  remote (the one the freshness check compares against) is not a clone of
  it. The repository path in the remote URL is compared, case-insensitively;
  the host is not, so an SSH host alias works.
- With stacks declared, `deploy` and `destroy` refuse a run that would
  execute any stack the declaration does not cover, before the first step.
- Neither has an override, and `CI=true` relaxes neither.
- Nothing declared, nothing checked: both are opt-in, so every existing
  caller is unchanged until it declares.

## Alternatives not taken

- **Compare the hosting service's numeric repository id.** It survives a
  rename, which a path does not. But nothing in a checkout records it: the
  only way to learn the checkout's id is to ask the service's API, which
  needs a credential for a private repository and a network call that the
  gate would then depend on. A path read from the remote URL is available
  offline and is what a person sees. A rename is handled by updating the
  declaration and the remotes (`git remote set-url`), which the refusal
  names.
- **Let `--allow-stale-checkout` cover them.** That flag is for a checkout
  that is the right one but not proven current. A wrong repository or a
  stack someone else owns is not a state a person can mean to apply from;
  the way past is to change the declaration, in review.
- **Restrict discovery to the owned stacks.** A stack that silently
  disappears from `list` and `diff` hides the duplication instead of
  refusing it; `diff` of a stack another repository owns is harmless and
  useful while moving it.

## Consequences

- A repository that splits, or a copy that becomes a new repository, is
  refused until its declaration says what it is now.
- The declaration lives in the tree it describes. It catches copies and
  forks, which carry a declaration naming the repository they came from; it
  is not a defence against someone who edits it, which review is.
- Steps that are not stacks are not checked, and neither are `diff` and
  `refresh`.
