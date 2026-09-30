# 0001 — Safety gates belong to the runner

**Status:** Accepted

## Context

A pipeline of many stacks is run by many people and by automation, and the
failures that hurt are not in any one stack: applying from a checkout that had
fallen behind its remote, applying a program that then wanted to destroy what
it had just built, a preview that was supposed to prove an alias migration
changed nothing and was read by eye instead, and a runner that waited forever
on a step that had already finished.

Each of these can be prevented by a convention. Each of them was, until the
person who knew the convention was not the person running it.

## Decision

The four checks are the runner's, on by default, in the code that executes
steps rather than in the callers around it:

1. `deploy` and `destroy` refuse a dirty, behind or unverifiable checkout.
2. A successful apply is followed by a refresh-preview that must plan no
   delete or replace.
3. `diff --expect-no-changes` turns any reported change into a failure.
4. Every step runs under a clock.

The first has a loud override, the third and fourth are flags with defaults,
and the second has none.

## Consequences

- A caller cannot forget a gate, and a new caller gets them without reading
  this page.
- The engine (`pkg/pipeline`) carries gates 1, 3 and 4, and stays free of any
  Pulumi dependency; gate 2 is in `pkg/pulumistep` because it needs a stack.
- The checkout gate shells out to `git`. A caller that runs the pipeline
  outside a git checkout must pass `--allow-stale-checkout` and accepts the
  warning.
- The post-apply check has no override. A stack that plans a replacement
  straight after its own apply is a stack to fix; if a real case turns up,
  this decision is revisited rather than worked around.
