# 0002 — Scopes and outputs are the caller's

**Status:** Accepted

## Context

The code this was extracted from discovered scopes under a directory whose
name was written into it, and wrote stack outputs to a place whose layout was
written into it too. Both are true of exactly one repository.

## Decision

- The scopes directory has no built-in default. It comes from a flag, an
  environment variable, a file the caller commits, or the `Options` a
  program built on `pulumicli` passes. With none, the command says so and
  exits.
- Output persistence is an interface, `pulumistep.OutputHooks`, asked once
  per stack. The library implements none; a nil answer means "persist nothing
  for this stack", which is also how a caller makes enrolment explicit.

## Consequences

- A stranger's first run fails with instructions rather than finding nothing
  and reporting success.
- A repository that wants its outputs written somewhere writes a twenty-line
  `main` instead of forking the tool.
- The component contract's C13 (estate facts are inputs, never defaults) holds
  for the flags and for the Go API.
