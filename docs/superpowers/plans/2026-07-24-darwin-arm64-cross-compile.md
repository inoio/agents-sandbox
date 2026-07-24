# darwin/arm64 cross-compilation via Zig — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:subagent-driven-development` (recommended) or
> `superpowers:executing-plans` to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Produce a prebuilt `opencode-msb-darwin-arm64` release artifact by
cross-compiling CGO with Zig (`zig cc`) in a custom Go+Zig builder image, and
ship two release assets (linux/amd64 + darwin/arm64) from the release branch.

**Architecture:** A new `ci/builder/Dockerfile` builds a `golang:1.26` image
with pinned Zig 0.16.0 on `PATH`. A kaniko `build-builder-image` job (prepare
stage) pushes it to the project container registry on Dockerfile changes /
manual trigger (never on the release branch). The release pipeline is refactored:
a single `version` job sources the version into a dotenv; a hidden
`.build-release-tmpl` template runs `go build` with `CC="zig cc -target …"`
per target; two concrete jobs (linux/amd64, darwin/arm64) produce signed
artifacts; the `release` job wires both as asset links.

**Tech Stack:** GitLab CI/CD, kaniko (`gcr.io/kaniko-project/executor:debug`),
Zig 0.16.0 (fallback 0.15.2), Go 1.26 + CGO, the microsandbox Go SDK's
per-platform embedded Rust dylib (build-tag selected).

## Global Constraints

- Target platforms: Linux (amd64) and macOS Apple Silicon (arm64). No
  linux/arm64 release artifact (documented only; SDK-supported, trivially
  enableable).
- CGO is genuinely required (microsandbox SDK `internal/ffi` is a cgo bridge);
  `CGO_ENABLED=0` excludes it. The Rust dylib is embedded per-platform and
  dlopen'd at runtime — not linked at build.
- Zig is pinned to **0.16.0**
  (`https://ziglang.org/download/0.16.0/zig-x86_64-linux-0.16.0.tar.xz`);
  fallback is 0.15.2 if a cgo regression appears.
- Builder image lives at `$CI_REGISTRY_IMAGE/ci-builder:latest`. It is pushed
  using the CI job token (`$CI_REGISTRY_USER` / `$CI_REGISTRY_PASSWORD`) — no
  extra auth key.
- **One-time bootstrap prerequisite:** the `:latest` builder image must exist in
  the registry before the first release-branch pipeline that consumes it.
  Trigger `build-builder-image` manually once (it also auto-runs on
  `ci/builder/Dockerfile` changes on non-release branches).
- Ad-hoc code signing is automatic and secret-free: Go's internal linker
  auto ad-hoc-signs any `darwin/arm64` binary (keyed off the target, not the
  host OS); the embedded Rust dylib already carries an embedded code signature.
  Gatekeeper will warn end users (ad-hoc, not notarized); bypass via
  right-click > Open or `xattr -d com.apple.quarantine`. Notarization is out of
  scope.
- The `Makefile` is unchanged; cross-compilation is CI-only.
- All `.gitlab-ci.yml` edits must remain valid YAML (`python3 -c` yaml load is
  the local structural check; `gitlab-ci-local` is not installed here).
- The full cross-compile and the pipeline only execute in GitLab CI; the final
  acceptance gate is a manual macOS Apple-Silicon runtime check of the released
  binary (automated CI cannot execute a darwin/arm64 Mach-O on the linux runner).

## File Structure

- **Create `ci/builder/Dockerfile`** — `FROM golang:1.26`, installs `xz-utils`,
  `ca-certificates`, `curl`, `file`, `python3`, downloads + extracts pinned Zig
  to `/usr/local`, prepends it to `PATH`. One responsibility: provide a
  reproducible Go+Zig cross-compile environment.
- **Create `ci/smoke-check-darwin-arm64.py`** — stdlib-only Mach-O parser that
  asserts a file is a 64-bit arm64 Mach-O carrying `LC_CODE_SIGNATURE` (load
  command `0x1d`) without executing it. Used by the darwin build job as the
  automated smoke signal (the binary cannot run on the linux runner).
- **Modify `.gitlab-ci.yml`** — add `prepare` stage; add `build-builder-image`
  (kaniko) + `version` + `.build-release-tmpl` + `build-release:linux-amd64` +
  `build-release:darwin-arm64` (and a commented-out `build-release:linux-arm64`);
  replace the monolithic `build-release`; rewire `release` to two asset links.
- **Unchanged:** `Makefile`, all Go sources under `cmd/` and `internal/`.

---

## Task 1: Zig builder Dockerfile + kaniko `build-builder-image` job

**Files:**
- Create: `ci/builder/Dockerfile`
- Modify: `.gitlab-ci.yml` (add `prepare` stage; add `build-builder-image` job)

**Interfaces:**
- Produces: a container image `$CI_REGISTRY_IMAGE/ci-builder:latest` containing
  `zig` (0.16.0) and `go` (1.26) on `PATH`, plus `file` and `python3` for
  downstream smoke checks. Consumed by the `image:` of `.build-release-tmpl`
  in Task 4.

- [ ] **Step 1: Create the builder Dockerfile**

Create `ci/builder/Dockerfile` with exactly:

```dockerfile
FROM golang:1.26

ARG ZIG_VERSION=0.16.0
ARG ZIG_TARBALL=zig-x86_64-linux-${ZIG_VERSION}.tar.xz

RUN apt-get update \
 && apt-get install -y --no-install-recommends xz-utils ca-certificates curl file python3 \
 && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL "https://ziglang.org/download/${ZIG_VERSION}/${ZIG_TARBALL}" -o "/tmp/${ZIG_TARBALL}" \
 && tar -xf "/tmp/${ZIG_TARBALL}" -C /usr/local \
 && rm "/tmp/${ZIG_TARBALL}"

ENV PATH="/usr/local/zig-x86_64-linux-${ZIG_VERSION}:${PATH}"

RUN zig version && go version
```

- [ ] **Step 2: Add the `prepare` stage**

In `.gitlab-ci.yml`, insert `prepare` into the `stages` list so the list reads:

```yaml
stages:
  - lint
  - deps
  - test
  - prepare
  - build
  - release
```

- [ ] **Step 3: Add the `build-builder-image` kaniko job**

Append to `.gitlab-ci.yml` (after the `.go-cache:` / hidden anchors section,
before `lint:`):

```yaml
build-builder-image:
  stage: prepare
  image:
    name: gcr.io/kaniko-project/executor:debug
    entrypoint: [""]
  rules:
    - if: $CI_COMMIT_BRANCH == "release"
      when: never
    - changes:
        - ci/builder/Dockerfile
    - when: manual
  script:
    - mkdir -p /kaniko/.docker
    - echo "{\"auths\":{\"$CI_REGISTRY\":{\"username\":\"$CI_REGISTRY_USER\",\"password\":\"$CI_REGISTRY_PASSWORD\"}}}" > /kaniko/.docker/config.json
    - /kaniko/executor --context "$CI_PROJECT_DIR" --dockerfile "ci/builder/Dockerfile" --destination "$CI_REGISTRY_IMAGE/ci-builder:latest"
```

- [ ] **Step 4: Verify YAML is still valid**

Run:
```bash
python3 -c "import yaml; yaml.safe_load(open('.gitlab-ci.yml'))"
```
Expected: no output, exit 0.

- [ ] **Step 5: Commit**

```bash
git add ci/builder/Dockerfile .gitlab-ci.yml
git commit -m "ci: add zig builder image and kaniko build-builder-image job"
```

---

## Task 2: Hoist the `version` job

**Files:**
- Modify: `.gitlab-ci.yml` (add `version` job in the `build` stage)

**Interfaces:**
- Produces: a `version.env` dotenv artifact report exposing `VERSION` to any job
  that `needs: [version]` with `artifacts: true`. Consumed by both
  `build-release:*` jobs (Task 4) and by `release` (Task 5).

- [ ] **Step 1: Add the `version` job**

Insert into `.gitlab-ci.yml` (in the `build` stage, immediately before the
`build:` job definition):

```yaml
version:
  stage: build
  extends: .go-image
  rules:
    - if: $CI_COMMIT_TAG
      when: never
    - if: $CI_COMMIT_BRANCH == "release"
  script:
    - VERSION="0.$(date -u +%Y.%-m.%-d)+${CI_COMMIT_SHORT_SHA}"
    - echo "VERSION=$VERSION" >> version.env
  artifacts:
    reports:
      dotenv: version.env
```

Notes: `extends: .go-image` (golang:1.26 / Debian) guarantees GNU `date` honors
the `%-m`/`%-d` no-zero-pad specifiers used by the existing release format
(e.g. `0.2026.7.24+<sha>`). This job is release-branch only — it has no
consumer elsewhere.

- [ ] **Step 2: Verify YAML is still valid**

Run:
```bash
python3 -c "import yaml; yaml.safe_load(open('.gitlab-ci.yml'))"
```
Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add .gitlab-ci.yml
git commit -m "ci: hoist release version into its own version job"
```

---

## Task 3: darwin/arm64 smoke-check script (with local verification)

**Files:**
- Create: `ci/smoke-check-darwin-arm64.py`

**Interfaces:**
- Produces: an executable check `python3 ci/smoke-check-darwin-arm64.py <file>`
  that exits 0 iff `<file>` is a 64-bit arm64 Mach-O carrying an
  `LC_CODE_SIGNATURE` load command; exits 1 with a stderr message otherwise;
  exits 2 on usage error. Consumed by the `build-release:darwin-arm64` job
  (Task 4). Pure stdlib; no test framework is added (the repo has no Python
  test suite — verification runs the script against the real embedded SDK
  dylib, a genuine arm64 signed Mach-O).

- [ ] **Step 1: Create the smoke-check script**

Create `ci/smoke-check-darwin-arm64.py` with exactly:

```python
#!/usr/bin/env python3
"""Smoke-check a darwin/arm64 opencode-msb binary without executing it.

Asserts the file is a 64-bit Mach-O for arm64 and carries an
LC_CODE_SIGNATURE load command (required for arm64 macOS execution).
Exits 0 on success, 1 on any check failure, 2 on usage error. Stdlib only.
"""
import struct
import sys

MH_MAGIC_64 = 0xFEEDFACF
CPU_TYPE_ARM64 = 0x0100000C
LC_CODE_SIGNATURE = 0x1D
HEADER_SIZE = 32  # mach_header_64


def check(path: str) -> str:
    with open(path, "rb") as f:
        data = f.read(HEADER_SIZE)
    if len(data) < HEADER_SIZE:
        return f"{path}: too small to be a Mach-O ({len(data)} bytes)"
    magic, cputype, _cpusubtype, _filetype, ncmds, sizeofcmds, _flags, _reserved = (
        struct.unpack_from("<8I", data, 0)
    )
    if magic != MH_MAGIC_64:
        return f"{path}: not a 64-bit Mach-O (magic=0x{magic:08X})"
    if cputype != CPU_TYPE_ARM64:
        return f"{path}: not arm64 (cputype=0x{cputype:08X})"
    with open(path, "rb") as f:
        f.seek(HEADER_SIZE)
        body = f.read(sizeofcmds)
    off = 0
    for _ in range(ncmds):
        if off + 8 > len(body):
            return f"{path}: truncated load commands"
        cmd, cmdsize = struct.unpack_from("<2I", body, off)
        if cmd == LC_CODE_SIGNATURE:
            return ""
        if cmdsize < 8:
            return f"{path}: malformed load command (cmdsize={cmdsize})"
        off += cmdsize
    return f"{path}: missing LC_CODE_SIGNATURE (no embedded ad-hoc signature)"


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print(f"usage: {argv[0]} <mach-o-binary>", file=sys.stderr)
        return 2
    msg = check(argv[1])
    if msg:
        print(msg, file=sys.stderr)
        return 1
    print(f"{argv[1]}: OK (64-bit arm64 Mach-O with LC_CODE_SIGNATURE)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
```

- [ ] **Step 2: Make it executable and byte-compile it**

Run:
```bash
chmod +x ci/smoke-check-darwin-arm64.py
python3 -m py_compile ci/smoke-check-darwin-arm64.py
```
Expected: no output, exit 0 (valid syntax).

- [ ] **Step 3: Positive check against the real embedded SDK dylib**

The microsandbox SDK embeds a genuine arm64 signed Mach-O dylib in the module
cache. Run the script against it:

```bash
DYLIB="$(go env GOMODCACHE)/github.com/superradcompany/microsandbox/sdk/go@v0.6.6/internal/bundle/bundles/libmicrosandbox_go_ffi-darwin-arm64.dylib"
python3 ci/smoke-check-darwin-arm64.py "$DYLIB"
```
Expected: a line ending in `OK (64-bit arm64 Mach-O with LC_CODE_SIGNATURE)`,
exit 0. (This dylib has filetype=DYLIB, but the script deliberately does not
assert filetype — only arm64 + signature — so it is a valid fixture. The real
built executable has filetype=EXEC and passes identically.)

- [ ] **Step 4: Negative check against a non-Mach-O**

Run:
```bash
python3 ci/smoke-check-darwin-arm64.py README.md
echo "exit=$?"
```
Expected: a stderr message `README.md: not a 64-bit Mach-O ...`, exit 1.

- [ ] **Step 5: Commit**

```bash
git add ci/smoke-check-darwin-arm64.py
git commit -m "ci: add darwin/arm64 Mach-O smoke-check script"
```

---

## Task 4: Release-build template + per-target jobs

**Files:**
- Modify: `.gitlab-ci.yml` (replace the monolithic `build-release` job with
  `.build-release-tmpl`, `.build-release-build-steps`, two concrete jobs, and a
  commented-out linux/arm64 job)

**Interfaces:**
- Consumes: `$VERSION` from the `version` job (Task 2); the
  `$CI_REGISTRY_IMAGE/ci-builder:latest` image (Task 1);
  `ci/smoke-check-darwin-arm64.py` (Task 3).
- Produces: two artifacts (`opencode-msb-linux-amd64`,
  `opencode-msb-darwin-arm64`) and two dotenv reports exposing
  `LINUX_AMD64_JOB_ID` and `DARWIN_ARM64_JOB_ID` (the job IDs that build the
  asset URLs). Consumed by `release` (Task 5).

- [ ] **Step 1: Replace `build-release` with the template + per-target jobs**

Remove the entire existing `build-release:` job (the one that `extends: build`
and inlines VERSION/BUILD_JOB_ID/linux-amd64 build). In its place, insert:

```yaml
.build-release-tmpl:
  stage: build
  image: $CI_REGISTRY_IMAGE/ci-builder:latest
  needs:
    - job: version
      artifacts: true
  variables:
    GOPATH: $CI_PROJECT_DIR/.go
  before_script:
    - mkdir -p .go
  cache:
    paths:
      - .go/pkg/mod/
  rules:
    - if: $CI_COMMIT_TAG
      when: never
    - if: $CI_COMMIT_BRANCH == "release"
  artifacts:
    paths:
      - $ARTIFACT_NAME
    reports:
      dotenv: build.env

build-release:linux-amd64:
  extends: .build-release-tmpl
  variables:
    GOOS: "linux"
    GOARCH: "amd64"
    ZIG_TARGET: "x86_64-linux-gnu"
    ARTIFACT_NAME: "opencode-msb-linux-amd64"
  script:
    - export CC="zig cc -target $ZIG_TARGET"
    - export CXX="zig c++ -target $ZIG_TARGET"
    - CGO_ENABLED=1 GOOS=$GOOS GOARCH=$GOARCH go build -ldflags "-X main.version=$VERSION" -o $ARTIFACT_NAME ./cmd/opencode-msb
    - ./opencode-msb-linux-amd64 --version
    - echo "LINUX_AMD64_JOB_ID=${CI_JOB_ID}" >> build.env

build-release:darwin-arm64:
  extends: .build-release-tmpl
  variables:
    GOOS: "darwin"
    GOARCH: "arm64"
    ZIG_TARGET: "aarch64-macos-gnu"
    ARTIFACT_NAME: "opencode-msb-darwin-arm64"
  script:
    - export CC="zig cc -target $ZIG_TARGET"
    - export CXX="zig c++ -target $ZIG_TARGET"
    - CGO_ENABLED=1 GOOS=$GOOS GOARCH=$GOARCH go build -ldflags "-X main.version=$VERSION" -o $ARTIFACT_NAME ./cmd/opencode-msb
    - file opencode-msb-darwin-arm64
    - file opencode-msb-darwin-arm64 | grep -q "Mach-O.*arm64"
    - python3 ci/smoke-check-darwin-arm64.py opencode-msb-darwin-arm64
    - go version -m opencode-msb-darwin-arm64
    - echo "DARWIN_ARM64_JOB_ID=${CI_JOB_ID}" >> build.env

# linux/arm64 is SDK-supported (libmicrosandbox_go_ffi-linux-arm64.so is
# embedded) and ready to enable by uncommenting:
#
# build-release:linux-arm64:
#   extends: .build-release-tmpl
#   variables:
#     GOOS: "linux"
#     GOARCH: "arm64"
#     ZIG_TARGET: "aarch64-linux-gnu"
#     ARTIFACT_NAME: "opencode-msb-linux-arm64"
#   script:
#     - export CC="zig cc -target $ZIG_TARGET"
#     - export CXX="zig c++ -target $ZIG_TARGET"
#     - CGO_ENABLED=1 GOOS=$GOOS GOARCH=$GOARCH go build -ldflags "-X main.version=$VERSION" -o $ARTIFACT_NAME ./cmd/opencode-msb
#     - ./opencode-msb-linux-arm64 --version
#     - echo "LINUX_ARM64_JOB_ID=${CI_JOB_ID}" >> build.env
```

Notes on correctness:
- The three-line build invocation (CC/CXX/`go build`) is inlined in each job
  rather than shared via `!reference`, so the YAML stays plain (PyYAML-valid)
  and the local structural checks below work without a custom loader. The
  template still abstracts image/needs/cache/rules/artifacts.
- The darwin job cannot execute a Mach-O on the linux runner, so it substitutes
  `file` (informational + a `grep` guard for `Mach-O.*arm64`), the Python
  signature assertion (Task 3), and `go version -m` to confirm the `-ldflags`
  version landed.
- `build.env` is written by each job with its own distinct variable name; the
  `release` job `needs` both and receives both dotenv reports.
- Go's cgo accepts `CC="zig cc -target …"` (splits the value into program + args)
  — the standard Zig-as-cross-C-compiler pattern.

- [ ] **Step 2: Verify YAML is still valid**

Run:
```bash
python3 -c "import yaml; yaml.safe_load(open('.gitlab-ci.yml'))"
```
Expected: no output, exit 0.

- [ ] **Step 3: Structural assertions**

Run:
```bash
python3 - <<'PY'
import yaml
d = yaml.safe_load(open('.gitlab-ci.yml'))
jobs = d.keys()
assert 'build-release' not in jobs, 'old monolithic build-release must be removed'
assert 'build-release:linux-amd64' in jobs
assert 'build-release:darwin-arm64' in jobs
assert 'version' in jobs
assert 'build-builder-image' in jobs
assert '.build-release-tmpl' in jobs
for j in ('build-release:linux-amd64','build-release:darwin-arm64'):
    assert d[j]['extends'] == '.build-release-tmpl'
    script = d[j]['script']
    assert any('go build' in s and 'main.version=$VERSION' in s for s in script), f'{j} missing go build'
    assert any('zig cc -target $ZIG_TARGET' in s for s in script), f'{j} missing CC export'
    assert any('JOB_ID=${CI_JOB_ID}' in s for s in script), f'{j} missing JOB_ID echo'
# per-target variables wired through
assert d['build-release:linux-amd64']['variables']['ZIG_TARGET'] == 'x86_64-linux-gnu'
assert d['build-release:darwin-arm64']['variables']['ZIG_TARGET'] == 'aarch64-macos-gnu'
print('structural OK')
PY
```
Expected: `structural OK`, exit 0.

- [ ] **Step 4: Commit**

```bash
git add .gitlab-ci.yml
git commit -m "ci: split release build into template and per-target zig jobs"
```

---

## Task 5: Wire the `release` job to both targets

**Files:**
- Modify: `.gitlab-ci.yml` (the `release:` job)

**Interfaces:**
- Consumes: `$VERSION` (from `version`), `$LINUX_AMD64_JOB_ID` and
  `$DARWIN_ARM64_JOB_ID` (from the two build jobs), and the two build artifacts.
- Produces: a GitLab Release named `opencode-msb $VERSION` with two asset links
  pointing at each job's artifact file.

- [ ] **Step 1: Rewrite the `release` job**

Replace the entire existing `release:` job with:

```yaml
release:
  stage: release
  image: registry.gitlab.com/gitlab-org/cli:latest
  needs:
    - job: version
      artifacts: true
    - job: build-release:linux-amd64
      artifacts: true
    - job: build-release:darwin-arm64
      artifacts: true
  rules:
    - if: $CI_COMMIT_TAG
      when: never
    - if: $CI_COMMIT_BRANCH == "release"
  script:
    - echo "running release_job for $VERSION"
  release:
    name: "opencode-msb $VERSION"
    description: "Release $VERSION"
    tag_name: "$VERSION"
    ref: '$CI_COMMIT_SHA'
    assets:
      links:
        - name: "opencode-msb-linux-amd64"
          url: "${CI_PROJECT_URL}/-/jobs/${LINUX_AMD64_JOB_ID}/artifacts/file/opencode-msb-linux-amd64"
          filepath: "/opencode-msb-linux-amd64"
        - name: "opencode-msb-darwin-arm64"
          url: "${CI_PROJECT_URL}/-/jobs/${DARWIN_ARM64_JOB_ID}/artifacts/file/opencode-msb-darwin-arm64"
          filepath: "/opencode-msb-darwin-arm64"
```

- [ ] **Step 2: Verify YAML is still valid**

Run:
```bash
python3 -c "import yaml; yaml.safe_load(open('.gitlab-ci.yml'))"
```
Expected: no output, exit 0.

- [ ] **Step 3: Structural assertion — two asset links, correct needs**

Run:
```bash
python3 - <<'PY'
import yaml
d = yaml.safe_load(open('.gitlab-ci.yml'))
r = d['release']
need_jobs = {n['job'] for n in r['needs']}
assert need_jobs == {'version','build-release:linux-amd64','build-release:darwin-arm64'}, need_jobs
links = r['release']['assets']['links']
names = [l['name'] for l in links]
assert names == ['opencode-msb-linux-amd64','opencode-msb-darwin-arm64'], names
assert '${LINUX_AMD64_JOB_ID}' in links[0]['url']
assert '${DARWIN_ARM64_JOB_ID}' in links[1]['url']
print('release wiring OK')
PY
```
Expected: `release wiring OK`, exit 0.

- [ ] **Step 4: Commit**

```bash
git add .gitlab-ci.yml
git commit -m "ci: release both linux-amd64 and darwin-arm64 assets"
```

---

## Acceptance gates (post-implementation)

These are not code tasks; they are the spec's verification gates, listed here
so the implementer/operator does not skip them.

1. **Bootstrap the builder image (one-time).** Before the first release-branch
   pipeline that consumes it, ensure `$CI_REGISTRY_IMAGE/ci-builder:latest`
   exists. Either merge a change to `ci/builder/Dockerfile` to a non-release
   branch (auto-triggers `build-builder-image`) or run the job manually. Verify
   the image pushed and that `zig version` / `go version` printed in its build
   log. If the image is missing, release-branch build jobs fail at image pull.

2. **linux/amd64 build smoke (automated).** `build-release:linux-amd64` runs
   `./opencode-msb-linux-amd64 --version` on the linux runner. Job success is
   the signal.

3. **darwin/arm64 build smoke (automated).** `build-release:darwin-arm64`
   succeeds (`go build` exits 0), `file` reports `Mach-O ... arm64`, the Python
   smoke script reports `OK`, and `go version -m` shows the version ldflag. CI
   build success is the primary automated signal (the Mach-O cannot execute on
   the linux runner).

4. **Release asset links (automated).** The `release` job creates a Release
   with exactly two assets: `opencode-msb-linux-amd64` and
   `opencode-msb-darwin-arm64`, each resolving to a downloadable binary via its
   job's artifact URL.

5. **Manual macOS runtime check (final acceptance gate).** Download the
   released `opencode-msb-darwin-arm64` on an Apple-Silicon Mac and run
   `./opencode-msb-darwin-arm64 --version`. Expect a Gatekeeper warning (ad-hoc
   signature, not notarized); bypass via right-click > Open or
   `xattr -d com.apple.quarantine <binary>`. This is the only check that
   actually executes the darwin binary; automated CI cannot perform it.

## Error handling notes

- Builder-image job failing blocks image updates; release-branch builds fail at
  `pull` time if `:latest` is missing (see bootstrap gate above).
- Any `go build` failure or smoke-assertion failure fails its job; `release`
  only runs when all builds pass (`needs` enforces ordering).
- Zig version is pinned (0.16.0) for reproducibility; 0.15.2 is the fallback
  if a cgo regression appears — change `ARG ZIG_VERSION` in
  `ci/builder/Dockerfile` and re-run `build-builder-image`.
