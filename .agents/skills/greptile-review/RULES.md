# Correctness rule families

A probe each for one class of correctness defect. For every changed hunk, ask every probe of the
value it changes and of the code that consumes that value. A rule fires only when you can name a
concrete triggering input and the code path it breaks — the spec is the authority, so point at it
rather than restating it. `corpus.tsv` holds the mined examples; the PR numbers below cite them.

## 1. Equivalent representation

**Probe.** Does the check compare the *concept* against one spelling of it? Can the same thing be
written another way — case, `a/../b` vs `b`, a symlink vs its target, a raw vs an interpreted
string literal, CRLF vs LF, a trailing newline present or absent, a quoted vs a bare CSV field?
Is the comparison made on the raw bytes or on a normalized form, and do the reader and the writer
normalize the same way?

**Breaks like.** A guard that lowercases keys while the reader is case-sensitive; a path looked up
after `filepath.Abs` that was recorded un-cleaned; `\n` trimmed before hashing so two different
files collide. The bypass needs no exploit — just the other spelling.

**Spec.** `.operator-shared/specs/configuration/project-config-trust.md`,
`.operator-shared/specs/network-and-secrets/`.

**Corpus.** PR604 "Key case bypasses approval", "Different bytes share approval" (P1/P2); PR608
"Changed scripts bypass approval" (P1); PR590 "Raw strings escape the guard" (P2).

## 2. Bounded input

**Probe.** Is every read of a file, stream, line, list, or map that a user or the agent controls
bounded — including the buffer that accumulates an *unfinished* unit? When a bound trips, does it
fail loudly, or is the item silently treated as absent/skipped?

**Breaks like.** A 17 MiB file over a 16 MiB cap is dropped from the projection but still built; a
`pending` buffer that holds an unterminated line grows without limit; an oversized report is
silently not posted. Treating "rejected" as "missing" turns the bound into a bypass.

**Spec.** `internal/configread` (the 16 MiB cap); `.operator-shared/specs/state/state-model.md`.

**Corpus.** PR604 "Large files skip approval" (P1), "Unapproved files exhaust host memory" (P1);
PR650 "Build output grows without bounds" (P1).

## 3. Gate ordering

**Probe.** If a check gates an action, can an earlier stage drop, skip, or rename the item before
the guarded read — so the gate never sees it? Does the gate observe the item at its *source*, or
only at a point a prior step can bypass?

**Breaks like.** A file deleted after approval never reaches the guarded read, so discovery skips
it and the violation stays nil; the image builds before the environment files are read, so a later
refusal cannot stop the build. Ordering is the bug.

**Spec.** `.operator-shared/specs/vm/lifecycle.md`.

**Corpus.** PR608 "Deleted files bypass refusal", "Failed reads bypass startup stop",
"Image-build promise is too strong" (P1/P2).

## 4. Test asserts the behaviour

**Probe.** Would this test fail if the behaviour it names were removed or reverted? Does the
assertion cover the named behaviour, not a neighbour of it? Does the fixture seed the exact state
the bug needs, at the right time? Does a guard test check the *body* and *order* of what it
inspects, not just that a call appears? Can a hand-maintained registry fail when a new member is
added? Does a build-tag move, skip, or exclusion hide a whole class?

**Breaks like.** A cleanup test that asserts the body ran but not that reaping happened; a keep-alive
test whose fake command exits on cancel, so "stop" can be broken and still pass; an AST guard that
counts installer calls even when they run after `m.Run()`; a regression test that seeds the volume
*before* the snapshot the race needs. A test that cannot fail is not verification.

**Spec.** `.operator-shared/specs/testing/test-isolation.md`, `.../test-coverage.md`.

**Corpus.** PR242 "Late setup passes the check", "New families are not discovered" (P2); PR513
"Cleanup goes unchecked" (P2); the whole "Test misses …" family in `corpus.tsv`.

## 5. Mutation exclusion

**Probe.** For every mutation suppression in the diff (`gomutants:disable*`), construct the mutation
it suppresses and confirm no test catches it. If a test does, the exclusion is hiding a regression
that future mutation sweeps will never see.

**Breaks like.** An `EXPRESSION_REMOVE` that also covers removal of an `err != nil` guard; an
`INTEGER_INCREMENT` exclusion on a value that is load-bearing at 1.

**Spec.** `.operator-shared/specs/ci/mutation-testing.md`.

**Corpus.** PR320 "Exclusion hides required error guard", "Exclusion hides unsafe capacity change",
"CPU boundary checks get skipped" (P2).

## 6. Prose matches code

**Probe.** Every claim about ordering, scope, defaults, guarantees, or "no longer" in the diff —
CHANGELOG, specs, brain, GLOSSARY, guides, doc comments, docs — does the code hold it? Does a doc
name the same signals/fields/limits the code uses? Is a heuristic stated as a guarantee?

**Breaks like.** "The check covers every file the launcher reads" when one path is unbounded; a
troubleshooting list naming signals the classifier deliberately rejects; "always exits 0" over a
script that exits 2; a comment describing a test that asserts something else.

**Spec.** `.operator-shared/specs/conventions/deprecation-policy.md`, `STYLEGUIDE.md`.

**Corpus.** PR649 "Partial failures are described incorrectly" (P2), PR608 "Image-build promise is
too strong" (P2), PR190 "Release note overstates the fix" (P2).

## 7. Cross-boundary propagation

**Probe.** When a value or process crosses a boundary — a child process, a container, a network
namespace, a mount, `PATH`, the environment, a CA store, a port — does the receiver get
everything it needs? Does the change add a hop that drops one of them?

**Breaks like.** A node shim on the wrong `PATH` position shadows the real node; `docker run`
passes only `HOME`, dropping the caller's exported variables; a fresh image gets neither the CA
cert nor `NODE_EXTRA_CA_CERTS`; `--network host` that does not cover the rootless case; a worktree
directory that is not mounted.

**Spec.** `.operator-shared/specs/runner-image/login-shell-path.md`,
`.operator-shared/specs/network-and-secrets/`.

**Corpus.** PR642 "`pi` cannot start", "Node loses exported variables", "HTTPS requests fail",
"Scripts use the wrong checkout" (all P1).

## 8. Race / ordering / concurrency

**Probe.** Shared timers, package globals, concurrent sessions, retries, interrupts, lock
ownership: does the change preserve mutual exclusion and state ordering? Does a read-then-write
span a window another writer can enter? Does a cancellation or retry leave a half-applied state?

**Breaks like.** Two sessions answering the same prompt overwrite each other; a retry overwrites a
manual edit; an interrupted copy looks complete; a live lease disappears; concurrent clones share
a home. Global test timers that make the suite flaky count here too.

**Spec.** `.operator-shared/specs/state/state-model.md`,
`.operator-shared/specs/vm/session-reaper.md`.

**Corpus.** PR142 "Simultaneous dismissals stop sessions" (P1), PR596 "Reuse runs delete active
VMs" (P1), PR595 "Tests race over shared timers" (P1).

## 9. Persistence / compatibility

**Probe.** Can an older binary drop records a newer one wrote, or a newer binary misread old
state? Is the schema version bumped so an older writer refuses rather than silently dropping? Does
a migration avoid overwriting newer data with an answer it read earlier?

**Breaks like.** A version-2 field list left unchanged so an older launcher erases the new records;
a migration that replaces a newer dismissal with an older unanswered marker; a saved setting that
stays overridden after a raise.

**Spec.** `.operator-shared/specs/state/state-model.md`, `.../upgrade-state.md`,
`.operator-shared/specs/volumes/`.

**Corpus.** PR585 "Older launchers erase disk records" (P1), PR244 "Saved home becomes
unreachable" (P1), PR142 "Existing homes get overwritten" (P1).

## 10. Shell portability

**Probe.** Does every shell script run on the oldest supported shell — bash 3.2 on macOS: no
`mapfile`, no associative arrays, no `${var,,}`? Is an exit status captured before a pipeline,
command substitution, or `printf` masks it? Are paths resolved absolute before they are tested?
Are temp dirs cleaned on every exit? Does a redirect or a non-TTY path skip a consent step?

**Breaks like.** `mapfile` on macOS bash 3.2 aborts the report; `printf "$(cmd)"` swallows the
failure so the target list is silently empty; `--git-path` returns a relative path tested against
the caller's CWD; a preview that writes local refs.

**Spec.** The `scripts/*.sh` + `scripts/test-*.sh` convention; see `scripts/`.

**Corpus.** PR618 "macOS checks stop at mapfile" (P1), "Another checkout's rebase is missed" (P1),
PR645 "File listing errors hide failures" (P2).

## 11. CI gating

**Probe.** Does a required check run on the events that matter (follow-up pushes, the merge queue)?
Can it be satisfied by an empty, deleted, or reverted input? Does a job-level `if`, a skipped
dependency, or an interpolated `${{ }}` name a value that disables or injects into it?

**Breaks like.** A slug check that also fires on *deleted* fragments, blocking the release PR; a
required check that a follow-up push never starts; a workflow that interpolates a tag name into a
shell command.

**Spec.** `.operator-shared/specs/ci/ci-pipeline.md`, `.../quality-gates.md`.

**Corpus.** PR618 "Release cleanup fails CI" (P1), PR368 "Follow-up pushes lose checks" (P1),
PR319 "PR sweeps never start" (P1).

## 12. Parser correctness

**Probe.** For a format the repo owns — Dockerfile, CSV cache specs, YAML, config snippets — does
the change round-trip? Heredoc continuation and terminators, quoted fields, CRLF, empty and spaced
strings, embedded quotes: is each handled the way the format says, on both the read and write side?

**Breaks like.** A continuation line swallows a later stage; a quote-stripped CSV field is
re-serialized unquoted so a scope collapses; CRLF stops the build; a spaced profile string fails
validation.

**Spec.** `.operator-shared/specs/runner-image/image-composition.md`, `.operator-shared/adr/0001`.

**Corpus.** PR638 "Quoted scopes lose build separation" (P2), PR632 "Quoted cache paths fail" (P2),
PR554 "Memory edit breaks YAML" (P1).

## 13. Resource reclamation

**Probe.** Every temp file, dir, handle, port, volume, and lock the change creates — is it released
on all exit paths, including error and interrupt? Does a cleanup assertion actually observe the
release?

**Breaks like.** A leaked handle or temp dir on the failure path; a failed cleanup that leaves a
usable partial; a probe that holds a container response open.

**Spec.** `.operator-shared/specs/state/state-model.md`, `.operator-shared/specs/volumes/`.

**Corpus.** PR191 "Test leaves a handle open" (P2), PR592 "Previews leave temporary directories"
(P2), PR596 "Concurrent tests delete shared files" (P2).

## 14. Error classification

**Probe.** Does a user-facing message or classifier attribute the failure to a cause the evidence
establishes? Is the evidence scoped to the failing unit, or to a wider window that can contain an
unrelated signal?

**Breaks like.** `Failed to fetch` read as "cannot reach the network" when a 404 from a reachable
mirror prints it too; a resolver warning tolerated by one step misreporting a later step's failure;
a stderr classification that drops the useful detail.

**Spec.** `.operator-shared/specs/runner-image/image-build.md`.

**Corpus.** PR650 "Download errors wrongly blame DNS" (P2), PR318 "Docker failures lose useful
details" (P2).
