# KVM-guest git against the host — design

> Status: design, approved 2026-11-22; revised the same day after PR #831 (`feat/kvm-guest-gh`) merged
> into `main`. Implemented in PR #843; the durable how-to is folded into
> `.operator-shared/guides/kvm-guest.md` ("Git transport"), so this design is retired.

## Intent and why

The KVM guest's git is a clone of GitHub that the host only reaches **one way and destructively**:
`make kvm-sync` rsyncs the host's working tree over it (`--delete`, no `.git`), and work returns with
`make kvm-sync-back`, a host-side `git fetch` out of the guest. There is no remote pointing at the host,
so the agent cannot `git pull`/`git push` against the machine it is actually working with.

The agent should instead treat the host as its git remote. That removes the destructive rsync, removes
the `--delete`-over-a-live-session hazard, and lets the host's worktrees see the agent's branches
directly.

## Goals

- **G1 — the guest's one git remote resolves to the host.** The guest checkout keeps its single remote
  (`origin`) and its recorded URL, and a URL rewrite routes every git operation on it to the host
  repository. `git fetch`, `git pull`, and `git push` act on the host with no `kvm-*` wrapper.
- **G2 — host→guest sync is a git pull, not rsync.** `make kvm-sync` stops copying a working tree and
  becomes a guest-side `git fetch` + branch switch + fast-forward. No `--delete`, no clobbering the
  agent's tree, safe to run more than once.
- **G3 — the guest can push.** The agent pushes its branches into the host repository; the host's
  worktrees see them as local branches without a fetch.
- **G4 — the host's checked-out branches stay protected.** Pushing a branch that a host worktree has
  checked out is refused by git, preserving the guard `kvm-sync-back` deliberately keeps.
- **G5 — no new always-on host service, and `gh` keeps working.** The transport reuses the guest→host
  SSH that already works; the guest's `gh` stays installed and signed in for issues and PRs.

## Non-goals

- **No new GitHub credential, and no guest→GitHub git push.** The guest's git targets the host; GitHub
  is not a git remote in the guest. `gh` (already installed and signed in by #831) stays for reading and
  commenting on issues and PRs, but creating a PR — which needs its branch on GitHub — is done from the
  host, not the guest.
- **No change to the `msb` suites, `make kvm-run`, `prime`, or the guest toolchain.**
- **No secret-proxy reproduction.** Credentials remain the explicit env-var channel.

## Anti-goals

- A guest credential that is a general shell on the host. The key is confined to git.
- Two divergent sync mechanisms (rsync and git) for the same direction.
- Copying uncommitted host work into the guest. The rewrite moves committed refs; the working tree is
  the agent's own.
- A second remote. The guest keeps one.

## Constraints

- **Base is `origin/main` with #831 merged.** `agent_provision` already writes only `user.name`/
  `user.email`; `gh_install` and `gh_auth` exist; `sync_in` is still the rsync. This design changes no
  identity provisioning.
- **Host-only**, x86_64-only, never in CI or the dev VM; the pure parts are covered by the existing
  fixture harness in `test/scripts` (faked `virsh`/`ssh`/`rsync`/`git`).
- **Bare `.git` + worktrees on the host** (confirmed layout; `git rev-parse --path-format=absolute
  --git-common-dir` yields the bare repo). A plain clone is tolerated: `--git-common-dir` returns its
  `.git` gitdir, which also serves as a remote path (verified).
- **The guest is disposable.** `make kvm-down` destroys the domain and its disk; the key must be stable
  across rebuilds so the host's `authorized_keys` does not accumulate lines.
- **The host's `sshd` accepts the guest's key at the bridge gateway.** `guides/kvm-guest.md` already
  notes guest→host SSH is one of the few paths that survives the Docker `ip filter` firewall state.
- Follow the repo gates: `make shellcheck`, `make fmt`, `make lint`, `make check`.

## Approaches

Three ways to let the guest reach the host as a git remote. The client must initiate, so the guest must
reach the host; only host→guest SSH exists today.

1. **Guest→host SSH (chosen).** The guest holds a key; the host authorizes it, restricted to
   `git-shell`. No new service; symmetric with `kvm-sync-back`; works in the broken-egress firewall
   state; both pull and push.
2. **Host `git daemon` on the bridge.** Unauthenticated, push-enabled, cleartext; a new always-on
   service. Rejected: more moving parts and a weaker access story than a git-shell-restricted key.
3. **Keep it host-initiated.** The host pushes into the guest and fetches back. Rejected: the agent
   never runs `git pull`/`git push`.

**Decided sub-approaches:**

- **Key ownership — host-generated.** The host generates one keypair (in a host-global state dir) and
  injects the private half into the guest. Stable across `kvm-down`/`kvm-up`; one `authorized_keys`
  line.
- **Remote form — a repository-local URL rewrite, keeping the single `origin` remote.** The guest's
  `origin` keeps the repository URL recorded by the boot clone, and the bootstrap adds
  `url."ssh://<host-user>@<gateway><repo-path>".insteadOf <repo-url>` in the guest's **repository**
  config (`~/agents-sandbox/.git/config`, not `--global`) so every git operation on `origin` goes to
  the host. Repository-local, beside `core.sshCommand`, because `agent-provision` truncates the guest's
  `~/.gitconfig`; a `--global` rewrite would be wiped by it, silently sending the agent's pushes back to
  GitHub. Chosen over `git remote set-url origin <host>` because it keeps `origin`'s recorded URL
  (visible to anything that reads `.git/config`) and reuses the rewrite mechanism #831 already
  established, rather than deleting it. **Verified caveat:** `git remote get-url` and `git remote -v`
  *apply* the rewrite and print the host, so `gh`'s remote-based repository inference sees the host; use
  `gh --repo <owner/repo>` or `GH_REPO`.
- **Bootstrap seed — keep the cloud-init clone.** The guest still clones `REPO_URL` at first boot as an
  object seed (the host is not reachable until the key is installed, which happens after boot).

**Relationship to #831 (merged):** #831's `gh` install, `gh auth login --with-token`, and
`gh auth setup-git` are kept. Its two `url.https://github.com/.insteadOf` entries (which made the
guest's SSH-shaped GitHub remotes authenticate over HTTPS) are removed — the guest's git no longer
talks to GitHub, and the host rewrite replaces them.

## The how (as decided)

### Host-side key and authorization

- `ssh-keygen -t ed25519 -N '' -C agents-sandbox-kvm-guest -f "$KVM_GUEST_STATE/id_kvm_guest"` when the
  key is absent. The key lives in a **host-global** state dir (`KVM_GUEST_STATE`, default
  `${XDG_STATE_HOME:-$HOME/.local/state}/agents-sandbox/kvm-guest`), not `ARTIFACT_DIR`: the domain
  (`VM_NAME`) is shared across worktrees while `ARTIFACT_DIR` is per-worktree (`$PWD/.kvm-guest`).
- Append the public key to `~/.ssh/authorized_keys` once, marker-tagged and `git-shell`-restricted:

  ```
  command="git-shell -c \"$SSH_ORIGINAL_COMMAND\"",restrict ssh-ed25519 AAAA… agents-sandbox-kvm-guest
  ```

  Idempotent by its options prefix plus the `agents-sandbox-kvm-guest` comment, not by the marker
  alone: a marker-only check would skip the write after a key rotation and break auth, while the
  comment keeps the match rotation-safe without matching another git-shell key that shares the prefix.
  The prefix is required so a line a pre-fix run merged this options block onto (which does not begin
  with it) is preserved. Verified: git sends `git-upload-pack '<path>'` /
  `git-receive-pack '<path>'`, which is exactly the form `git-shell` accepts.
- **Known limitation:** `git-shell` confines the *command* but not the *path* — verified that a
  git-shell session reaches `git-upload-pack` for any path the host user can read. The guest therefore
  has git access to any repository that account owns. Acceptable for the same-trust dev guest; stated in
  the guide, not mitigated.

### Remote bootstrap (new `ensure_guest_git_remote`, called by `sync_in`)

1. Resolve the bridge gateway from `virsh net-dumpxml default` (its `<ip address='…'>`), next to the
   existing `default_bridge()`; do not hardcode the address.
2. Resolve the host repository path with `git -C "$repo_root" rev-parse --path-format=absolute
   --git-common-dir`.
3. Generate/authorize the host key and install the private half in the guest.
4. In the guest: `git config url."ssh://<host-user>@<gateway><repo-path>".insteadOf "$REPO_URL"`
   (repository-local in `~/agents-sandbox/.git/config`, so `agent-provision`'s `~/.gitconfig` write does
   not wipe it), record the host URL in `kvmguest.hostUrl` and remove the previous `url.<host>` section
   first (two `insteadOf` rules for the same URL would let a stale one shadow the new one after a moved
   gateway/repo, verified), pin `branch.<REPO_BRANCH>.remote`/`merge` to `origin`, set
   `push.autoSetupRemote` (so a new branch's plain `git push` works), and set `core.sshCommand` to use
   the guest key. `<host-user>` is the account running the script, not `VM_USER`.
5. **Preflight guest→host git** (mirroring the existing egress probe): from the guest,
   `cd ~/agents-sandbox && git ls-remote origin`. A bare `ssh … true` cannot be the probe: the key is
   forced through `git-shell`, which rejects any command but `git-upload-pack`/`git-receive-pack`, so
   `ssh … true` would fail on a correctly configured host (verified). `ls-remote` runs a full
   `git-upload-pack` handshake, which `git-shell` accepts. On failure, die with a diagnostic pointing at
   the host's `openssh-server` and `git-shell`, instead of letting the first `git fetch` fail with an
   opaque ssh error and no fallback transport.

### `kvm-sync` (replaces the rsync)

`sync_in` becomes: ensure the remote (above), then in the guest `git -C ~/agents-sandbox fetch origin
--prune`, `git checkout <REPO_BRANCH>`, and `git merge --ff-only origin/<REPO_BRANCH>`. A diverged guest
branch fails loudly rather than being clobbered. Uncommitted host work is no longer copied, and
`SOURCE_DIR` goes with the rsync.

### Push

The agent pushes with plain `git push` (the guest sets `push.autoSetupRemote`, so a new branch's first
push creates it on the host and sets its upstream). It lands in the host's repository; a branch checked
out in a host worktree is refused (`! [remote rejected] … (branch is currently checked out)` —
verified), so the host's current branches stay protected and integration is a host-side
merge/checkout, as today. A bare repository's own `HEAD` is not protected this way: git allows a push
to the branch it points at when no worktree has it checked out.

### `kvm-sync-back`

Kept as the host-initiated fallback for when the guest cannot push. The guide presents push as primary.

### `gh`

Unchanged: `gh_install` in cloud-init and `gh_auth` on `agent-up` (`gh auth login --with-token`,
`gh auth setup-git`) stay. The two `url.https://github.com/.insteadOf` configs in `gh_auth` are removed.
The guest uses `gh --repo`/`GH_REPO` for repository context.

### Tests, docs, changelog

- Extend `test/scripts/kvm_guest_test.go`; register any new host command (`ssh-keygen`) in
  `main_test.go`; extend `kvmGuestVirshHandler`'s `net-dumpxml` fixture with the gateway `<ip>`. Replace
  the rsync-sync assertions with assertions on the guest-side git commands forwarded over the faked
  `ssh`, and add cases for the rewrite config, the `authorized_keys` guard, the preflight, and the
  `gh_auth` change.
- Update `.operator-shared/guides/kvm-guest.md` (transport section, workflow, prerequisites),
  `.operator-shared/guides/kvm-agent-testing.md` (T5 becomes a guest push), `CONTRIBUTING.md`, the
  catalog description, and a `changelog.d/<PR>.added.md` fragment.

## Left to the builder

- Whether `kvm-sync` keeps its name; whether `kvm-sync-back` is removed.
- The exact guest key filename and whether the bootstrap is its own `kvm-guest.sh` subcommand.
- Whether `down` prunes the marker-tagged `authorized_keys` line (the key survives `down`).
- The precise `ssh` invocation that installs the private key into the guest.
