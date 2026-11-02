# AGENTS.md

## Project

`agents-sandbox`: a launcher that runs coding agents (opencode, pi, ...) inside a microsandbox VM, binding a host directory as `/workspace` and
persisting user state via a home directory volume.

## Code style

- Follow idiomatic Go (Effective Go / Go Proverbs).
- Write clear, self-documenting code (ex. extract a function and name it clearly).
- Use self-documenting, non-abbreviated identifiers (```additionResult := add(1,2)```). 
- Keep inline comments concise and avoid them when the code can instead be refactored to be self-documenting.
- Start/stay concrete and simple. Add abstractions when the code gives you a reason to.
- Keep units/files small and focused on a clear responsibility.
- When principles conflict, prioritize: KISS → YAGNI → SOLID → DRY

## Constraints

- Target platforms: Linux (KVM) and macOS (Apple Silicon).
- Secrets are only passed to VMs via msb's secret mechanism.

## Design decisions

- One ephemeral microsandbox VM per project, multiple clients can attach to the vm & connect
  to the server.
- One release branch per minor line (`release/<major>.<minor>`): new work lands on `main`, a
  released line's fixes land on its release branch, and the line's patch releases are tagged there.
  See `.operator-shared/specs/release-branching.md`.

## Development

You are dogfooding the project, you are not on the host, but in an agents-sandbox VM. Filesystem layout:

- `/workspace` bind mount of the host CWD, mounted rw. When working there, don't commit/revert/stash etc yourself: 
  There's potential for parallel edits by other agents/humans. Always ask the user how to finalize the session.
- `~/.local/share/opencode/worktree/` git worktrees of `/workspace`, created by opencode. If that's your CWD, finalize 
  the session by pushing a PR to github and deleting the worktree.

Some sessions run on the host instead: there is no `/workspace`, and `/dev/kvm` is writable. There you can start VMs and
run `make test-msb` yourself, so runtime behavior (network policy, DNS, harness startup) can be verified against real
VMs instead of being handed to the user. Issues labelled `ready-for-human` for that reason are workable on the host.

## System tools

Prefer these installed CLI tools over other tools or custom solutions:

| When you need to...                        | Use                                             |
|--------------------------------------------|-------------------------------------------------|
| Search file contents                       | `rg` (recursive by default)                     |
| Find files by name/pattern                 | `fdfind`                                        |
| Parse/transform JSON or YAML               | `jq` / `yq` (never brittle text parsing)        |
| Transform text streams, perform mass edits | `awk` / `sed` / `recode`                        |
| Run independent commands in parallel       | `parallel`                                      |
| Hit HTTP endpoints / download              | `curl` / `wget`                                 |
| Inspect network/ports/sockets              | `ip` / `ss` / `nc`                              |
| Resolve DNS / check connectivity           | `dig` / `nslookup` / `ping`                     |
| Inspect a file or tree                     | `file` / `tree`                                 |
| Sync directories/files                     | `rsync`                                         |
| Read docs / manuals                        | `man`                                           |
| Create/extract archives                    | `tar` / `zip` / `unzip` / `xz` / `lz4` / `zstd` |

Don't install additional tools yourself without permission.

## Project toolchain

(see .agents-sandbox/Dockerfile)

- go, gofmt, golangci-lint, gcc (for CGO), zig (for cross-compilation)
- msb (microsandbox cli) - since /dev/kvm is not functional in the VM, you can't actually start VMs yourself. Must be
  tested manually by the user, unless you run on the host (see Development)
- docker

Common development commands (run from the Go module root):

- `make check` — run fmt, lint, test targets. Execute this when finalizing work.
- `go run ./cmd/agents-sandbox --dry-run` — build and run locally without producing a binary or starting interactively
  (skips launching the agent)
- `make test`/`go test ./...` — run all tests. `make test` passes `-tags microsandbox_ffi_path`, which keeps the
  SDK's ~36 MB native library out of every test binary; a bare `go test ./...` links it and is slower. See
  `.operator-shared/guides/test-runtime-cost.md`.
- `make compile-all` — compile every test binary including `integration`-tagged files without a docker daemon (catches
  tagged-only compile regressions; run by CI on every PR). Its `-run='^$'` output reads `[no tests to run]` for every
  package — that is compile-only, not skipped tests.
- `make test-msb` — run the `msb`-tagged harness smoke tests, which start every registered harness to its full-screen
  TUI in a real microsandbox VM. Host-only (needs KVM, a docker daemon, and network) and excluded from
  `make check`/`make test` via the `msb` build tag; `make compile-all` still compiles it. Run it (or a single test via
  `CGO_ENABLED=1 go test -tags msb -run <Test> ./internal/sandbox/msb/`) whenever you are on the host. `make lint`
  skips `msb`-tagged files; lint them with `golangci-lint run --build-tags msb <pkg>`.
- `make lint`/`golangci-lint run` — run the linter. ALWAYS use! DON'T use `go vet`!
- `make fmt`/`golangci-lint fmt` — format all files. ALWAYS use! DON'T manually rewrite / use `go fmt`!
- `go mod tidy` — sync `go.mod`/`go.sum` (run after adding/removing imports).
- `make build` - build binary to `./agents-sandbox`

Use the linter as a guide for code style: Run it after every major edit, for smaller edits run it after at most 3 edits.

## Superpowers

Always use your superpowers for appropriate tasks, never skip user approval.

## Testing

- Default to TDD - writing tests first, validating they compile and fail, implementing changes, validating passing tests
- Make sure that new/changed CLI commands/flags are thoroughly tested in the cmd/agents-sandbox/cli_*_test.go tests
- Also write valuable unit tests for internal functionality with every implementation.

## Documentation

- User-facing docs follow [`STYLEGUIDE.md`](STYLEGUIDE.md).
- When changing or adding behavior, keep `README.md` and `docs` directory (except `docs/superpowers`) in sync and
  current, and add a `changelog.d/<slug>.<type>.md` fragment (see `CONTRIBUTING.md`).
- Linking style: root-level docs (README.md, SECURITY.md, ...) link into `docs/` with rendered HTML links
  (`https://inoio.github.io/agents-sandbox/<page>.html`, or `.../<dir>/` for an `index.md` page); non-docs relative links
  must resolve to a repo file. Pages under `docs/` link to each other with Jekyll `{% link %}` tags. `make check` runs
  `ci/check-docs.sh` (and its tests in `ci/test-check-docs.sh`) to enforce this.
- When you struggled with something non-obvious, propose to the user to document it in `AGENTS.md`.
- The Jekyll build (used by `make docs-serve` and GitHub Pages) minifies output HTML onto a single line. An inline
  `<script>` in `docs/_includes/` that starts with a `//` line comment silently disables the whole script (the comment
  swallows the rest of the line). Use `/* ... */` or no comment inside inline scripts in the docs. If inline docs JS
  "doesn't work in the browser", check the minified output for this before touching logic.

## Current limitations

- No SSH keys in the VM, git cmds against remotes won't work. Use github-cli (gh).
- GitHub only runs `pull_request` workflows when the PR is mergeable. While a PR has merge conflicts, the `ci`
  workflows (`lint`, `test`, codecov) silently do **not** run — they are absent, not failed. Rebase to resolve the
  conflict and they trigger on the next push.
- microsandbox injects a tls cert into the VM for egress inspection. This can cause docker image builds to fail with
  self-signed cert errors. Workaround (example base image):

  ```
  # 1. Build a CA-trusting replacement for debian:trixie-slim.
  mkdir -p /tmp/cabase && cd /tmp/cabase
  cp /usr/local/share/ca-certificates/microsandbox-ca.crt ./
  cat > Dockerfile <<'EOF'
  FROM debian:trixie-slim
  COPY microsandbox-ca.crt /usr/local/share/ca-certificates/microsandbox-ca.crt
  RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && \
  update-ca-certificates && rm -rf /var/lib/apt/lists/*
  EOF
  docker build -t debian:trixie-slim .
  
  # 2. Build the runner image as usual.
  go run ./cmd/agents-sandbox build -r
  ```

# Your personality

<voice>
Write as a knowledgeable colleague who has already done the work: plain, direct, specific.
Use flowing prose. State things with "is"; do not inflate with "serves as" or "stands as".
Open with the answer, not with praise or a preamble. Do not use flattery openers
("Great question!").
Do not close by restating what you just said.
Prefer one precise sentence over three vague ones.
</voice>


<avoid>
- Negative parallelism: "it's not just X, it's Y" / "not merely … but …".
- Rule-of-three triplets used for rhythm rather than content.
- Significance/legacy puffery: "pivotal", "underscores", "a testament to", "nestled".
- Trailing "-ing" analysis clauses: "…, highlighting its importance".
- Restating the answer in a closing summary.
</avoid>

<audience>
You are explaining this to an experienced engineer who knows the domain and dislikes filler.
Assume competence; skip basics unless asked. Keep jargon that carries meaning; drop it where a
plain word is clearer.
</audience>

<content_invariant>
Changing the register must not change the substance. Preserve every number, identifier, file path,
code block, citation, caveat, and statement of uncertainty exactly. If a style rule and a fact
conflict, the fact wins.
</content_invariant>

<length>
Match length to difficulty, not to a fixed target. One or two sentences for a simple question;
full reasoning for a complex one. Never pad to seem thorough, and never truncate to seem concise.
</length>

<example>
<question>Why is the build slow?</question>
<answer>The image layer is being rebuilt on every run. Two things cause it: the agent version is
re-resolved each time, and the Dockerfile ID changes whenever the profile script changes. Pin the
version and the layer cache holds.</answer>
<question>Is the cache safe to delete?</question>
<answer>Yes — it only holds build layers. Deleting it costs you the next rebuild's time.</answer>
</example>
