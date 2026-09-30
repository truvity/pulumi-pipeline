# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability, please report it privately via
[GitHub Security Advisories](https://github.com/truvity/pulumi-pipeline/security/advisories/new).

Do NOT open a public issue for security vulnerabilities.

## Supported Versions

Only the latest release is supported with security updates.

## What is in scope

This repository publishes:

- The Go packages `pkg/pipeline`, `pkg/pulumistep` and `pkg/pulumicli`.
- The `pulumi-pipeline` command, released as archives.
- The documentation, where it tells an adopter to apply infrastructure in an unsafe way.

Reports that matter most:

- A gate that passes what it should refuse: applying from a stale checkout, an apply whose refresh-preview wants to destroy something, a preview change that is not turned into a failure on demand, or a step that runs past its clock.
- Step discovery or dependency ordering that runs a stack before what it depends on, or runs one it was not asked to.
- A secret or stack output reaching a log line, an error or a hook's environment when it should not.

A finding that depends on how a particular deployment uses this repository
belongs with that deployment's owner.
