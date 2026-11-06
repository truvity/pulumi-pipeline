# 0005 — A deploy verifies the inputs it reads from elsewhere

**Status:** Accepted

## Context

Some of what a program reads is not produced from its own checkout. A
publisher outside the repository (another team's pipeline, a platform
service) emits a non-secret file, and the program consumes it: names it must
not claim twice, identifiers it must agree on. The repository may keep a copy
next to the program so a person can read it, but that copy is advisory. The
published artifact is the source.

Nothing in the runner knew this. A deploy whose copy was old, edited by hand,
or whose publisher had gone quiet applied exactly as a deploy on a current
copy would, and the preview of it looked like any other. Applied against
out-of-date names, two writers can end up owning one record.

The checkout gate ([0001](0001-gates-belong-to-the-runner.md)) proves the
checkout is current with its own remote. It cannot say anything about a file
whose truth lives somewhere else.

## Decision

- A repository may declare external inputs (`inputs:` in
  `.pulumi-pipeline.yaml`, or `pipeline.ExternalInput`): a name, the local
  path of the copy, an opaque source reference with a scheme, a required
  `max_age`, and optionally the stacks it feeds (the patterns of
  [0004](0004-a-repository-declares-its-origin-and-stacks.md)).
- Before the first step of a `deploy` the runner hashes the local copy
  (sha256 of the raw bytes), asks the resolver registered for the source's
  scheme for the published digest and time, and refuses when the digests
  differ, when the publication is older than `max_age`, when the lookup
  fails, or when no resolver is registered for the scheme. It hashes the copy
  again before every step, so a file changed mid-run is not applied.
- The identity of an input is the digest of its published bytes. A publisher
  that pins by content (an OCI digest, for one) gives the same string.
- The runner does no fetching. A resolver is a one-method interface supplied
  by the caller, so no registry client enters the engine. The one built-in
  scheme, `file:`, reads a descriptor from disk and is for tests and
  fixtures.
- `diff` logs each problem at WARN and still runs; `diff --expect-no-changes`
  refuses, because a proof of no change made on stale input proves nothing.
  `destroy` and `refresh` are not gated.
- `--allow-stale-inputs` on `deploy` waives `max_age` and nothing else, and
  logs each waived problem at WARN. A digest mismatch, a missing or escaping
  file, a failed lookup and a missing resolver have no override. `CI=true` and
  `--allow-stale-checkout` relax nothing here.
- The source reference is never printed, because it may carry a credential.
  A resolver's error text is printed with the source removed.
- Nothing declared, nothing checked: existing callers are unchanged.

## Alternatives not taken

- **No override at all, as with ownership.** Safest, but an outage at the
  publisher then blocks every deploy, including the one that would fix a
  different problem. Age is the one condition that is about the publisher's
  health rather than the file's contents, so it alone can be waived by a
  person who has read the refusal.
- **An override that also covers a digest mismatch.** A mismatch means the
  file is not the published one; waiving it is applying a copy nobody
  published.
- **Optional `max_age`, or a library default.** An optional age turns the
  freshness half of the gate off by omission, and a default of any length is
  an estate fact the library has no business choosing
  ([0002](0002-estate-facts-are-inputs.md)).
- **Gate `destroy` and `refresh` too.** Tearing a stack down or reconciling
  its state does not put the program's reading of the input into the world.
  They also have to work when the publisher is the thing that is down.
- **Ship the registry client in the library.** It brings a large dependency
  tree into an engine that has none and fixes one publisher's protocol for
  every caller.
- **Define the descriptor's wire format here.** The runner needs three
  things (digest, time, revision); how a publisher conveys them is the
  resolver's business, so the format stays opaque.

## Consequences

- A deploy on a copy that is not the published one is refused before any step
  runs, whoever edited it and however it got there.
- A repository that declares an input must also supply a resolver, in a
  `main` of its own. Without one every deploy is refused, which is the point:
  an input that cannot be verified is not verified.
- The gate adds one lookup to a deploy, and a hash of each covered input per
  step. A publisher that is slow or down slows or blocks deploys of the
  stacks the input covers; narrowing `stacks` limits that.
- The check is only as good as the resolver. A resolver that reads a file
  committed beside the input verifies nothing; the built-in `file:` scheme is
  that, by design, and is for tests.
