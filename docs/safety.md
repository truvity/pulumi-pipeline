# Safety

Each refusal below is on by default, says what it saw, and names the way
past it when there is one. Each exists because the failure it prevents
happened.

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

**Past it.** `--allow-stale-checkout`. Every reason it overrides is logged at
WARN as `PROCEEDING FROM A CHECKOUT THAT IS NOT PROVEN CURRENT`. It is a flag
for a person, not for a script.

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

Nothing checks that you are pointed at the backend, account or environment
you meant. Those are the caller's, and the pipeline has no way to know what
you meant. Put that check in a `BeforeRun` hook on the scope.
