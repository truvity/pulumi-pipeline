# Doctrine

This repository is held to the [component contract](https://github.com/truvity/policy/blob/master/docs/contracts/component.md);
this page says only what is particular to it and links there rather than
restating a rule.

## The pipeline runs Pulumi; it does not wrap it

Every step is a call the Automation API already offers on a stack you could
run by hand. There is no state of its own, no lock of its own, no plan file.
What it adds is *order* (a DAG over stacks), *breadth* (one command for many
stacks) and *refusal* (the gates). A failure that is not one of those three is
Pulumi's and is shown as Pulumi shows it.

## Steps are discovered, not registered

A stack exists because its `Pulumi.<stack>.yaml` is committed. A second list
that says which stacks the pipeline knows about is a list that drifts from
the first, and a stack the pipeline forgot is a stack nobody previews. The
filesystem is the registry.

## Persistence is the caller's

The library writes no file and calls no API with a stack's outputs. It hands
them, secrets removed, to a hook the caller supplies. What to store, where,
and whether a given stack opts in are estate decisions, and a mechanism
library that made them would carry one estate's answer into the next.

## Refuse by default; override in the open

The checkout gate and the post-apply check refuse without being asked,
because the person they protect is the one who did not think to ask. Where an
override exists it is a named flag that logs what it let through. Where it
does not (the post-apply check) it is because the only reason to want it is
a stack that is wrong.

## The engine has no Pulumi dependency; the step does

`pkg/pipeline` knows steps, edges and actions and imports no Pulumi package,
so the DAG, the typed steps and every gate except the post-apply check are
tested without one. `pkg/pulumistep` is the only place the Automation API
appears, and its tests run a real stack on a local file backend — no cloud,
no provider plugin, no network beyond module downloads.

## Decisions

See [docs/decisions/](decisions/).
