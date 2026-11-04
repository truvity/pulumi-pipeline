# Safety

Each refusal below is on by default, except the two that act on a
repository's own declaration of what it is and owns, which are off until it
declares. Each says what it saw and names the way past it when there is one,
and each exists because the failure it prevents happened or was one step
away.

## The checkout gate

**Refuses:** `deploy` and `destroy` from a checkout whose working tree has
uncommitted or untracked paths, whose branch is behind its upstream, or
whose state cannot be verified (not a git checkout, a detached HEAD, no
upstream, a fetch that failed).

**Why.** An apply is the one command whose result is not the output but
the world. Run from a checkout that is ninety-nine commits behind, it
converges the world on a program that other people already changed, and the
change it silently reverts looks, in the preview, like a plain update. The
same is true of an uncommitted edit: what was applied is in nobody's
history.

**How it decides.** It fetches the upstream's remote first. Comparing with
an already-fetched remote-tracking ref proves nothing: the tree looks
current right up to the moment someone asks the remote. Being *ahead* of the
upstream is allowed — unpushed work is the ordinary state of a change being
tested — and is not reported. `diff` and `refresh` are not gated: they show
or reconcile state and do not put a program's reading of the world into it.

**In CI.** With `CI=true` in the environment (the literal string `true`;
every major CI system exports it) the gate skips the checks that cannot hold
on a runner: a detached HEAD, which is how a runner checks out, has no
upstream, so there is nothing to be "behind". Each skipped check is logged at
WARN as `CHECKOUT CHECK SKIPPED (CI=true)`. Everything that still holds is
still enforced: a dirty tree is refused, a checkout that is not a git
checkout is refused, and a branch that *does* have an upstream (a runner that
checks a branch out) is still fetched and compared. The runner's own
pipeline is what proves a detached commit is the intended one.

*Caveat.* The relaxation is triggered by an environment variable, not by
proof that the process is on a runner: a local shell that exports `CI=true`
bypasses the same upstream checks. Do not export it in a developer shell or a
profile. A dirty tree is refused either way.

**Past it.** `--allow-stale-checkout`. Every reason it overrides is logged at
WARN as `PROCEEDING FROM A CHECKOUT THAT IS NOT PROVEN CURRENT`. It is a flag
for a person, not for a script.

## The origin check

**Refuses:** `deploy` and `destroy` from a checkout whose remote is not a
clone of the repository it declares as its origin (`origin:` in
`.pulumi-pipeline.yaml`, or `Ownership.Origin`). Off until a repository
declares one.

**Why.** Once a program and its stack files exist in more than one
repository (a fork, a copy that seeded a new repository, a clone of the
repository before it was renamed or split), a checkout that is clean and
current proves nothing about whether it is the *right* checkout. The
freshness checks above compare a checkout with its own remote; a fork is
perfectly current with itself.

**How it decides.** It takes the remote the freshness check compares
against (the current branch's; `origin` when HEAD is detached or the branch
has none), reads its URL with `git remote get-url`, and compares the
repository path in it (`owner/name`) with the declaration, ignoring case.
The host is not compared, so an SSH host alias works. A remote that is a
local path, or no remote at all, cannot be identified and is refused. The
URL is never printed, since it may carry a credential.

**Past it.** None: neither `--allow-stale-checkout` nor `CI=true` relaxes
it. A runner checks out the repository it was triggered from, so the check
holds there. Run from a clone of the declared repository, point the remote
at it (`git remote set-url`), or correct the declaration.

## Stack ownership

**Refuses:** `deploy` and `destroy` of any stack, as `scope/stack`, that
the repository's declaration (`owned_stacks:` in `.pulumi-pipeline.yaml`,
or `Ownership.Stacks`) does not cover. Off until a repository declares the
stacks it owns.

**Why.** Scopes and stacks are discovered from the files in the tree, so a
stack file copied into a second repository is a second, equally runnable
way to apply the same stack. Two repositories applying one stack converge it
on whichever program ran last.

**How it decides.** Every step the run would execute is checked before the
first one starts, including the dependencies that naming a step pulls in.
A declared entry is a `path.Match` pattern over `scope/stack`, so
`network/*` covers a whole scope. Steps that are not stacks (exec and
confirm steps) are not checked. `diff` and `refresh` are not gated. A
malformed declaration is an error for every command.

**Past it.** None. Apply the stack from the repository that owns it, or add
it to the declaration of this one if it does.

## The post-apply refresh-preview

**Fails:** a `deploy` step whose `up` succeeded, when a `preview --refresh` of
the same stack straight afterwards plans any `delete`, `replace`,
`create-replacement` or `delete-replaced`.

**Why.** A clean `up` says the changes it planned were made. It does not say
the stack now matches its program. A refresh asks the provider what exists
*now*, so a resource that was deleted out from under the state, or one whose
recorded and real properties differ in a way that forces replacement, shows
up here and not in the apply. The next person to run the stack would
otherwise destroy what the apply appeared to have built.

**What you see.** The step fails with the operations and counts, the apply
is *not* rolled back (there is nothing safe to roll back to), and the
pipeline stops. Read the preview, fix the program or the state, and run
again. There is no flag to skip this check: a stack that legitimately plans
a replacement after every apply has a bug worth finding.

## `--expect-no-changes`

**Fails:** a `diff` step whose preview reports anything but `same` and
`read`.

**Why.** Renaming a resource or moving it under a component is done with an
alias, and its whole promise is that state moves and the resource does not.
The only proof is a preview that changes nothing. Without a gate, the proof
is a person reading a long diff at the end of a long day.

It is deliberately blunt: a provider-version bump is a change and fails
it. Pin the provider for the duration of the migration.

## The step clock

**Fails:** any step that runs longer than `--step-timeout` (30 minutes by
default).

**Why.** An earlier runner hung after a preview had finished — the work was
done and the process never returned — and nothing in the pipeline had a
clock, so the run sat there until someone noticed. The step's context is
cancelled at the deadline, which is what stops a step that honours it; a
step that does not return within a grace period is abandoned, because a
runner that waits forever on a step that has already finished its work is the
incident itself.

**After a timeout.** An apply that was cut off may leave the stack locked.
`<scope> doctor` reports locks and pending operations; `pulumi cancel` clears
a lock.

## Continue-on-error is for previews only

`diff` keeps going after a failure so that one broken stack does not hide
drift in the others, and exits 1 with a summary. Every action that changes
something stops at the first failure.

## What is not gated

Without a declaration, nothing checks that the checkout is the repository
that owns its stacks (see the origin check and stack ownership above).
Nothing checks that you are pointed at the backend, account or environment
you meant. Those are the caller's, and the pipeline has no way to know what
you meant. Put that check in a `BeforeRun` hook on the scope.
