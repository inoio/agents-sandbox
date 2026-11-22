# GitHub CLI in the KVM guest — design

> Status: design, awaiting approval. Small change. When built, the durable how-to folds into
> `.operator-shared/guides/kvm-guest.md` and this design is retired.

## Intent and why

The in-guest agent works on this repo, which is tracked on GitHub: issues, PRs, and the changelog/PR workflow all run
through `gh`. The guest is provisioned with the agent toolchain (`opencode2`, `tmux`, `shellcheck`,
`golangci-lint`) but **not** `gh`, and carries no GitHub credential — so the agent cannot read or comment on issues
and PRs, and work leaves the guest by `git fetch` over SSH only (`guides/kvm-guest.md`, `T5`). This change installs
`gh` in the guest and signs it in from a host-environment `GITHUB_PAT` on `make kvm-agent-up`, so the agent can use
GitHub directly and open its own PRs.

## Goals

- **G1 — `gh` is present.** The guest's cloud-init installs `gh`, pinned to the same version as the dev image.
- **G2 — signed in from the host env.** `make kvm-agent-up` reads `GITHUB_PAT` from the host environment and runs
  `gh auth login --with-token`, `gh auth setup-git`, and the SSH→HTTPS `url.*.insteadOf` rewrites in the guest, so the
  agent can also push and open PRs.
- **G3 — the token never rides argv.** The PAT reaches the guest over SSH **stdin**; it appears in no host command
  line.
- **G4 — absent token, unchanged behavior.** With `GITHUB_PAT` unset or empty, `make kvm-agent-up` behaves exactly as
  today: no gh contact, no failure.
- **G5 — idempotent and re-run safe.** Re-running refreshes the token; the live-session reattach path also re-signs-in
  without syncing over the agent's work.
- **G6 — a clean guest git identity.** `agent_provision` copies only the host's `user.name` and `user.email` into the
  guest, not the whole `~/.gitconfig` (credential helpers, aliases, `includeIf`, and signing config stay behind).

## Non-goals

- **No sign-in on `make kvm-agent` or `make kvm-agent-provision`.** Only `kvm-agent-up` wires it; a standalone
  subcommand can come later if wanted.
- **No install path for an existing guest.** The guest is disposable and one-shot; a guest booted before this change
  gets `gh` on the next `make kvm-down && make kvm-up` (the sign-in step then fails loudly with that instruction).
- **No secret mechanism.** The PAT rides the same acknowledged channel as the model key: a host-environment value
  handed to the guest. It is a same-trust-domain dev guest.

## Approaches

**Installing `gh`:**

1. **Pinned release `.deb` from GitHub, in cloud-init — chosen.** Mirrors `.agents-sandbox/Dockerfile` and
   `docs/recipes/git-access.md` (`gh_<version>_linux_<arch>.deb` → `dpkg -i`). One line, no apt key/source management,
   the version pinned with the dev image.
2. **GitHub's apt repository.** Stays current with `apt upgrade`, but adds a keyring, a sources file, and a repo pin
   for no benefit here (the guest is one-shot).
3. **`apt install gh` from the distro.** Availability on the guest codename is uncertain and the version is unpinned.

**Authenticating `gh`:**

1. **`gh auth login --with-token` + `gh auth setup-git` + `insteadOf`, over SSH stdin — chosen.** Persistent guest
   state (`~/.config/gh/hosts.yml`), the exact recipe the project documents for sandboxes, and it enables push/PR
   creation. The user picked this over sign-in-only.
2. **Inline `GH_TOKEN=…` on the agent command.** No persistent sign-in, the token lands in the host process list, and
   it is not "signed in"; rejected.

## The how (as decided)

### Install in cloud-init (`write_seed_files`)

- Add a `GH_VERSION` default read from `.agents-sandbox/Dockerfile`'s `ARG GH_VERSION=` (fallback to a literal),
  overridable via `GH_VERSION` — one source of truth with the dev image, the same pattern `GOLANGCI_LINT_VERSION`
  already uses against the Makefile.
- Build a `gh_install` fragment (like `opencode2_install`) with the pinned `.deb` download and `dpkg -i`, added to
  `runcmd` with the other agent-toolchain lines (after the checkout, so a failure cannot sit ahead of it).
- Document `GH_VERSION` in the script header.

### Sign-in (`gh_auth` in `scripts/kvm-guest.sh`)

A new function `gh_auth <ip>`, called from `agent_up` **after `agent_provision`** — `agent_provision` rewrites the
guest `~/.gitconfig` with the minimal user block, which would otherwise wipe the credential helper and rewrites — and
once for both the fresh and reattach paths, immediately before `agent_in`:

```
if [ -n "${GITHUB_PAT:-}" ]; then
  printf '%s\n' "$GITHUB_PAT" | ssh … "bash -lc 'gh auth login --with-token && gh auth setup-git && git config --global url.https://github.com/.insteadOf git@github.com: && git config --global --add url.https://github.com/.insteadOf ssh://git@github.com/'"
fi
```

- Unset/empty `GITHUB_PAT` → return early, no guest contact, no log noise.
- A failure dies with a clear message naming the likely cause (a guest built before this change has no `gh`).
- The remote command needs no inner quotes (the URLs are shell-safe unquoted), so it embeds cleanly in the existing
  single-quoted-over-double-quoted SSH pattern.
- The secret is **not** named `GH_TOKEN`/`GITHUB_TOKEN`: `gh auth login --with-token` refuses to store a token under
  either name (`docs/recipes/git-access.md`).

`agent_up` becomes: `ensure_running → wait_for_cloud_init →` (session check; reattach without sync **or** `sync_in` +
`agent_provision`) `→ gh_auth → agent_in`, so there is exactly one `gh_auth` call on either path.

### Git identity: only `user.name` and `user.email`

`agent_provision` today rsyncs the host's whole `~/.gitconfig` into the guest and then turns commit/tag signing off
(two `git config --global` calls). Replace that with a minimal identity: read the host's effective global `user.name`
and `user.email`, and when at least one is set, write a fresh guest `~/.gitconfig` carrying only:

```
[user]
	name = <host name>
	email = <host email>
```

Streamed to the guest over SSH **stdin** (`ssh … 'cat > ~/.gitconfig'`), so a name with spaces or shell
metacharacters needs no quoting. Writing the file fresh drops the host's credential helpers, aliases, `includeIf`, and
signing config — signing is then off by default, so the two `commit.gpgsign`/`tag.gpgsign false` calls go away. A host
with neither key keeps the guest's own identity (nothing is written). The `gh_auth` step runs after this and appends
its credential-helper and rewrite sections to the same file.

### `make kvm-agent-up`

Forward `GITHUB_PAT` explicitly (`GITHUB_PAT="$$GITHUB_PAT" …`) rather than relying on make's default export of
environment variables, and extend the target comment. `LITELLM_API_KEY` keeps its required-and-inline handling; the
PAT is optional and script-side.

### Invariant change

The guest agent can now **push** and open PRs. The "the guest never pushes / no GitHub credential" statements in
`guides/kvm-guest.md` and `guides/kvm-agent-testing.md` (`T5`, known limitations) are updated, and the superseded
non-goal "No GitHub/PR delivery in v1" in `plans/2026-11-22-kvm-guest-agent-design.md` is noted. `kvm-sync-back` stays
the no-push return path (a host without `GITHUB_PAT` is unchanged).

### Tests, docs, changelog

- `test/scripts/kvm_guest_test.go`: the render case asserts the `gh` download URL and pinned version in the seed;
  `agent-up` cases assert that setting `GITHUB_PAT` issues the `gh auth login --with-token` SSH command (and
  `setup-git`), that it is absent when the PAT is unset, and that the token reaches the fake `ssh` on **stdin** (the
  fake `ssh` records stdin when the harness asks for it). The `agent-provision` git-identity case becomes: only the
  host's `user.name`/`user.email` reach the guest (on stdin), and the host's other config and the `gpgsign` calls are
  gone.
- `guides/kvm-guest.md`: note `gh` is installed and signed in from `GITHUB_PAT` on `kvm-agent-up`; rewrite the
  credentials and never-pushes passages; describe the identity copy as name/email only.
- `guides/kvm-agent-testing.md`: add `gh --version` to `T1`, update `T2` (identity), `T3`/`T5`, and the known
  limitations.
- `changelog.d/<PR>.added.md`; update `.operator-shared/catalog.md` with this design + its plan.

## Left to the builder

- The exact `dpkg` vs `apt-get install` call for the `.deb` (either is fine; `dpkg -i` matches the recipe).
- Whether `gh_auth` should retry once on a transient API error.
- Whether the stdin capture in the fixture harness needs a new `fakecmd` knob or an existing one suffices.
