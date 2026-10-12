# Node-free runner images Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Emit the `agents-sandbox-node` stage only for agents that need node (pi), and install opencode v1/opencode2/claude-code from their native installers, so those runner images contain no node/npm.

**Architecture:** `ImageSpec` gains a `RequiresNode` flag; `RenderDockerfile` emits the node stage only when it is set and points `agents-sandbox-agent` at the node stage only then, otherwise at the base stage. The agent stage creates `/opt/agents-sandbox/bin` itself (the node stage used to). opencode2 and claude-code swap their npm install command for the upstream bash installer; pi keeps npm.

**Tech Stack:** Go 1.26, `internal/agent`, `internal/sandbox/image`; Docker (integration tests only).

**Spec:** [.operator-shared/specs/image-composition.md](../specs/image-composition.md) (stage diagram + "Node dependency" section).

Tracking issue: [inoio/agents-sandbox#133](https://github.com/inoio/agents-sandbox/issues/133).

## Global Constraints

- Tool artifact contract: every agent binary lands in `/opt/agents-sandbox/bin`; node/npm live under `/opt/agents-sandbox`; the runner appends `toolBin` to `PATH`.
- Stage aliases stay deterministic and reserved (`agents-sandbox-base|node|agent|docker|runner`); the `agents-sandbox-node` alias stays reserved even when not emitted.
- `dockerfile-id` stays a content hash of the rendered Dockerfile + profile.d merge script + resolved agent version; do not add host-dependent or nondeterministic content.
- Version selection stays pinned: the resolved version travels as a build `ARG` referenced by the install `RUN` (npm dep specifier, opencode `--version "$V"`, claude positional `"$V"`). Version discovery stays a host-side HTTP query; never run node for it.
- Target platforms: Linux (KVM) and macOS (Apple Silicon).
- Do not add any inline comments beyond those already idiomatic in these files.

## Review Focus

- **claude-code launcher is a symlink** (`~/.local/bin/claude` → `~/.local/share/claude/versions/…`): copying it without `cp -L` would copy a dangling symlink into the image, so the binary must be dereferenced. Covered by the install-command assertion in Task 3.
- **opencode2 binary name**: the v2 installer's real binary is `opencode`; the image must expose it as `opencode2` (what the launcher invokes). Covered by the install-command assertion in Task 2.
- **`/opt/agents-sandbox/bin` must exist without the node stage**: for a node-free agent the agent stage no longer inherits the node stage's `mkdir`. Covered by the agent-stage `mkdir` assertion in Task 1.
- **pi still builds on the managed base**: dropping node from shared stages must not touch pi's npm install. Covered by the pi node-stage assertion in Task 1 and the pi integration test in Task 4.
- **custom base that lacks node**: a node-free agent must still render/build (no node stage at all). Covered by the custom-base render assertion in Task 1.

---

### Task 1: Emit the node stage only when the agent requires it

**Files:**
- Modify: `internal/agent/image.go`
- Modify: `internal/agent/pi.go:17-27`
- Modify: `internal/sandbox/image/dockerfile.go` (`RenderDockerfile`, `agentStage`)
- Test: `internal/sandbox/image/render_test.go`
- Test: `internal/agent/pi_test.go`

**Interfaces:**
- Produces: `agent.ImageSpec.RequiresNode bool`; `image.agentStage(a agent.Agent, parentAlias string) string`.
- Consumes: existing `nodeStage(baseAlias string) string`, `baseStageAlias`, `nodeStageAlias`.

- [ ] **Step 1: Write the failing tests**

In `internal/sandbox/image/render_test.go`, add:

```go
func TestRenderDockerfileOmitsNodeStageForNodeFreeAgent(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	s := mustRender(t, a, nil, false)
	if strings.Contains(s, "AS "+nodeStageAlias) || strings.Contains(s, "nodejs.org/dist") {
		t.Errorf("a node-free agent must not emit the node stage; got:\n%s", s)
	}
	if !strings.Contains(s, "FROM agents-sandbox-base AS "+agentStageAlias) {
		t.Errorf("agent stage must start from the base stage without node; got:\n%s", s)
	}
	if !strings.Contains(s, "mkdir -p "+toolBin) {
		t.Errorf("agent stage must create %s so the tool COPY source exists; got:\n%s", toolBin, s)
	}
}

func TestRenderDockerfileEmitsNodeStageForPi(t *testing.T) {
	a, ok := agent.Lookup("pi")
	if !ok {
		t.Fatal("pi agent not registered")
	}
	s := mustRender(t, a, nil, false)
	for _, want := range []string{
		"FROM agents-sandbox-base AS " + nodeStageAlias,
		"FROM " + nodeStageAlias + " AS " + agentStageAlias,
		"nodejs.org/dist/" + nodeVersion,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("pi render missing %q; got:\n%s", want, s)
		}
	}
}
```

In `internal/agent/pi_test.go` (same package), assert the flag:

```go
func TestPiRequiresNode(t *testing.T) {
	a, _ := agent.Lookup("pi")
	if !a.ImageSpec().RequiresNode {
		t.Error("pi.ImageSpec().RequiresNode = false, want true (npm node bundle)")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/image/ ./internal/agent/ -run 'NodeStage|RequiresNode' -v`
Expected: FAIL — node stage still emitted for opencode; `RequiresNode` field does not exist (compile error).

- [ ] **Step 3: Implement**

In `internal/agent/image.go`, add `RequiresNode bool` to `ImageSpec` (after `VersionArg`, with a doc line: true when the agent needs node at install/runtime).

In `internal/agent/pi.go`, set `RequiresNode: true` in the `ImageSpec` literal.

In `internal/sandbox/image/dockerfile.go`:

- In `RenderDockerfile`, build the agent stage from the node alias only when `a.ImageSpec().RequiresNode`:

```go
blocks := make([]string, 0, 6)
blocks = append(blocks, earlier, baseStage)
agentParent := baseAlias
if a.ImageSpec().RequiresNode {
	blocks = append(blocks, nodeStage(baseAlias))
	agentParent = nodeStageAlias
}
blocks = append(blocks, agentStage(a, agentParent))
```

- Change `agentStage` to accept `parentAlias` and use it in the `FROM`, and `mkdir -p` `toolBin` before the install check: the `RUN` becomes `RUN mkdir -p %s && if command -v %s >/dev/null 2>&1; then ...`. The stage keeps `USER root` and `ARG <spec.VersionArg>`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/image/ ./internal/agent/ -run 'NodeStage|RequiresNode|Composition|StageOrder|CustomBaseReusesAlias' -v`
Expected: PASS. Also update the now-wrong existing expectations in `render_test.go`:
- `TestRenderDockerfileManagedComposition`: drop the two node-stage lines and `nodejs.org/dist/…`.
- `TestRenderDockerfileStageOrder`: drop the node-stage line; agent stage now follows the base stage.
- `TestRenderDockerfileCustomBaseReusesAlias`: replace `FROM final AS agents-sandbox-node` with `FROM final AS agents-sandbox-agent`.

Then run the full package: `go test ./internal/sandbox/image/ ./internal/agent/`.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/image.go internal/agent/pi.go internal/sandbox/image/dockerfile.go internal/sandbox/image/render_test.go internal/agent/pi_test.go
git commit -m "feat(image): emit node stage only for agents that need node (#133)"
```

---

### Task 2: Install opencode2 from its native installer

**Files:**
- Modify: `internal/agent/opencode2.go:26-34`
- Test: `internal/agent/opencode2_test.go:48`

**Interfaces:**
- Consumes: `RequiresNode` defaults to `false` for opencode2 (no change needed).
- Produces: opencode2 `ImageSpec.InstallCommand` that lands `opencode2` in `/opt/agents-sandbox/bin`.

- [ ] **Step 1: Update the failing test**

In `internal/agent/opencode2_test.go`, replace `wantInstall` with:

```go
wantInstall := `curl -fsSL https://opencode.ai/v2/install | bash -s -- --version "$OPENCODE2_VERSION" && cp /root/.opencode/bin/opencode /opt/agents-sandbox/bin/opencode2`
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run OpenCode2 -v`
Expected: FAIL (install command still the npm one).

- [ ] **Step 3: Implement**

In `internal/agent/opencode2.go`, set `InstallCommand` to the string above. Keep the `AgentEnv` (`OPENCODE_DISABLE_AUTOUPDATE`) and `VersionArg` unchanged.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent/ -run OpenCode2 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/opencode2.go internal/agent/opencode2_test.go
git commit -m "feat(agent): install opencode2 from its native installer (#133)"
```

---

### Task 3: Install claude-code from its native installer

**Files:**
- Modify: `internal/agent/claudecode.go:17-26`
- Test: `internal/agent/claudecode_test.go:33-43`

**Interfaces:**
- Produces: claude-code `ImageSpec.InstallCommand` that dereferences the installer's launcher symlink into `/opt/agents-sandbox/bin/claude`.

- [ ] **Step 1: Update the failing test**

In `internal/agent/claudecode_test.go`, replace the `@anthropic-ai/claude-code` assertion with:

```go
wantInstall := `curl -fsSL https://claude.ai/install.sh | bash -s -- "$CLAUDE_CODE_VERSION" && cp -L /root/.local/bin/claude /opt/agents-sandbox/bin/claude`
if spec.InstallCommand != wantInstall {
	t.Errorf("InstallCommand = %q, want %q", spec.InstallCommand, wantInstall)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run ClaudeCode -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

In `internal/agent/claudecode.go`, set `InstallCommand` to the string above. Keep `AgentEnv` `DISABLE_AUTOUPDATER=1` and `VersionArg`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent/ -run ClaudeCode -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/claudecode.go internal/agent/claudecode_test.go
git commit -m "feat(agent): install claude-code from its native installer (#133)"
```

---

### Task 4: Extend render + integration coverage

**Files:**
- Test: `internal/sandbox/image/render_test.go` (`TestRenderDockerfileOpencode2`, add node-free assertions for opencode2 + claude-code)
- Test: `internal/sandbox/image/render_integration_test.go` (`TestRenderDockerfileNpmAgentNodeRuns`, `TestRenderDockerfileToolLayersStayCached`)

**Interfaces:**
- Consumes: Task 1–3 behaviour.

- [ ] **Step 1: Write the failing assertions**

In `TestRenderDockerfileOpencode2`, replace the npm expectation with the native install string and add:

```go
"https://opencode.ai/v2/install",
```

Add a claude-code render test asserting the native install string and that neither `agentStageAlias` node stage nor `nodejs.org/dist` appears.

- [ ] **Step 2: Run to verify they fail / then pass**

Run: `go test ./internal/sandbox/image/ -run 'Opencode2|ClaudeCode' -v`
Expected: PASS after Tasks 1–3 (assertions encode the new behaviour).

- [ ] **Step 3: Retarget the integration tests**

- `TestRenderDockerfileNpmAgentNodeRuns`: switch the agent from `claude-code` to `pi` (the only npm agent) and rename to `TestRenderDockerfileNodeAgentNodeRuns`; it still builds on the managed base and runs `node --version` / `npm --version`.
- `TestRenderDockerfileToolLayersStayCached`: for opencode the node stage no longer exists; change the loop to `[]string{agentStageAlias, dockerStageAlias}`. Keep `stageInstallMarkers[agentStageAlias]` = `"opencode.ai/install"` and `[dockerStageAlias]`; the `nodeStageAlias` entry may stay for the pi case if a pi variant is added, otherwise leave it (used by no test only after this change — acceptable, or drop it).

- [ ] **Step 4: Verify compilation of tagged tests**

Run: `go test -tags integration -run xxx ./internal/sandbox/image/`
Expected: compiles (SKIP/no match without a docker daemon). Integration tests themselves run via `make test-integration` on a host with Docker.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/image/render_test.go internal/sandbox/image/render_integration_test.go
git commit -m "test(image): cover node-free agents and retarget npm/node IT to pi (#133)"
```

---

### Task 5: Docs, changelog, catalog

**Files:**
- Modify: `docs/runner-image.md` (stage bullets ~line 38-42; custom-base `curl`/`tar` bullet ~76; "Node and the agent" ~154-171)
- Modify: `README.md:89-97`
- Modify: `CHANGELOG.md` (`[Unreleased]` `Changed`)
- Modify: `.operator-shared/catalog.md` (image-composition entry description)

- [ ] **Step 1: Update the docs**

- `docs/runner-image.md`: mark `agents-sandbox-node` as emitted **only for agents that require node (pi)**; describe the agent install methods (opencode/opencode2 native installer, claude-code native installer, pi npm); note opencode/opencode2/claude images contain no node/npm and that JS LSP servers run on the agent's embedded runtime or on node a project installs.
- `README.md`: update the `opencode2` and `claude-code` bullets to say they use their native installers (version still resolved via npm/pi.dev).
- `CHANGELOG.md`: add a `Changed` bullet under `[Unreleased]`: opencode/opencode2/claude-code runner images are node-free (node stage emitted only for pi), shrinking the image and cold-build time; installs switched to the upstream native installers.
- `.operator-shared/catalog.md`: extend the `image-composition.md` entry's description to mention the node-dependency contract.

- [ ] **Step 2: Verify docs link checks**

Run: `make check` (or `ci/check-docs.sh`)
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add docs/runner-image.md README.md CHANGELOG.md .operator-shared/catalog.md
git commit -m "docs(image): document node-free agents (#133)"
```

---

## Verification

- [ ] `make check` passes (fmt, lint, tests).
- [ ] `git status` clean.
- [ ] Open a PR against `main` (session finalization per `AGENTS.md`).
