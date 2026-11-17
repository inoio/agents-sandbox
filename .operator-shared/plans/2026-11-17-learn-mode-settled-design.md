# S0 — Settled design and subspec restructure

> **For agentic workers:** This is a docs subplan, not an SDD run. There is no code, so there are no implementer/reviewer subagents: it is drafting and reorganizing prose, verified by `make brain-check` and the doc-link gate. Reviewer: a single `code-review` pass over the diff.

**Goal:** Put the settled learn-mode contract on `main` and split it into a thin hub plus four per-contract subspecs, so each later subplan carries the exact contract it implements rather than re-scoping a monolith.

**Architecture:** `learn-mode.md` keeps the cross-cutting truth (intent, vocabulary, journeys, invariants, phasing, deferred). Its per-area sections move into `learn-mode/observation-channel.md`, `learn-mode/home-read-back.md`, and `learn-mode/findings-and-apply.md`; the launcher network-surface truth becomes a new `network-and-secrets/interception-and-proxy.md`.

**Tech Stack:** none (Markdown only).

**Spec:** the source is `origin/spike/learn-mode`'s `.operator-shared/specs/onboarding/learn-mode.md` (the post-spike, resolved version) and `spike/learn-mode/results.md`. `main`'s copy is the pre-spike "unresolved" version and is replaced.

## Global Constraints

See `2026-11-17-learn-mode-program.md` → Global Constraints. For this subplan specifically:
- The hub stays thin: intent, vocabulary/voice, journeys (UC1–UC4) + UC2 detection bar, invariants, phasing, deferred. No approaches, no finding shape.
- **Specs hold behavior and invariants; plans hold signatures.** The exact Go shape of `InterceptMode`, `Proxy`, `Manifest`, `Finding` lives in the plans' Interfaces blocks, never in a spec.
- Every subspec needs YAML frontmatter (`description`, `read_if`); `read_if` is anchor-first and must not restate the `description` (the `brain-check` gate fails on that).
- Cross-partition links only within `.operator-shared/`; link subspecs from the hub with relative Markdown links.

---

### Task 1: Land the settled contract and the spike record

**Files:**
- From `origin/spike/learn-mode`: `.operator-shared/specs/onboarding/learn-mode.md` (source), `.operator-shared/specs/onboarding/onboarding.md`, `.operator-shared/plans/2026-11-15-learn-mode-spike.md`, `spike/learn-mode/**`, `.operator-shared/catalog.md`

- [ ] **Step 1:** Open the PR for `origin/spike/learn-mode` (tip `a5081241`). It has no PR; PR #698 landed only the pre-spike design.
- [ ] **Step 2:** Confirm `make check` and `make brain-check` pass on the branch; land it. If it is too stale to land whole, cherry-pick only `.operator-shared/` and `spike/` — the code paths are docs-only.

- [ ] **Step 3: Commit** (only if cherry-picked)

```bash
git add .operator-shared/ spike/
git commit -m "docs(operator): record the learn-mode spike outcome"
```

---

### Task 2: Split `learn-mode.md` into a hub and three subspecs

**Files:**
- Modify: `.operator-shared/specs/onboarding/learn-mode.md` (becomes the hub)
- Create: `.operator-shared/specs/onboarding/learn-mode/observation-channel.md`, `home-read-back.md`, `findings-and-apply.md`

**Interfaces:** none (prose). The content mapping below is the contract of this task — move each source section whole, do not paraphrase it into a weaker statement.

- [ ] **Step 1: Create `learn-mode/observation-channel.md`.** Frontmatter `description`/`read_if`: the approaches (A passive read-back, B opaque egress, C TLS termination), which ships and why, the T1/T2 topology, the capture artefact format and its scrubbing, CA generation and trust placement, the `#149`/`#151`/#2080 prerequisites, "observation requires the policy at creation" (recreate + guards), and the channel's open-validation items (QUIC, pinning, `msb-trust`). Source: the settled `learn-mode.md` sections "Observation approaches", "Prerequisites and phasing" (the `#149`/`#151` half), "Session boundary" (the creation-only half), and "Open validation" (the channel items); plus `spike/learn-mode/results.md`.

- [ ] **Step 2: Create `learn-mode/home-read-back.md`.** The facts 1–3 contract (agent config files and which entries are credentials; static secrets; login credentials, recorded never copied), the per-harness layout truth (opencode2's credential is an inline SQLite row, so read-back parses the agent DB, not only files), the **baseline manifest** contract (path, size, mtime, content hash; not a scan; refreshed at run end), and the S1 open-validation item (other harnesses unverified). Source: the settled `learn-mode.md` "What the analyser must establish" items 1–3, the opencode2 note, and "Session boundary"'s manifest paragraph.

- [ ] **Step 3: Create `learn-mode/findings-and-apply.md`.** The finding shape (evidence, proposed write, scope), scopes (per-slug user scope keyed by repo slug; project scope as an explicit second choice with the trust gate), the credential-store invariant detail (`reprovision.Provision` writes merged/mirror files unconditionally, so the managed layer never owns the credential store), how a learned static secret is attributed before it is scoped, the network diff (normally `profile: none` + observed allow set; never widen), interactive per-finding apply + decline memory keyed on an evidence hash, non-interactive/dry-run behavior, and `config harden` as the config-only subset of the same engine. Source: the settled `learn-mode.md` "The proposal" and "`config harden`" sections.

- [ ] **Step 4: Thin the hub.** Keep in `learn-mode.md`: Intent; User-facing vocabulary; The four journeys + UC2 detection bar; a one-line pointer that the analyser establishes facts 1–7 with links to `home-read-back.md` (1–3) and `observation-channel.md` (4–7); Invariants; Prerequisites and phasing (the phasing line); Deferred. Remove the sections moved in Steps 1–3. Add relative links to each subspec at the point of reference.

- [ ] **Step 5: Verify and commit.**

Run: `make brain-check`
Expected: `brain-check: ok` (write `read_if` lines that do not restate `description` — the gate rejects a restatement).

```bash
git add .operator-shared/specs/onboarding/
git commit -m "docs(operator): split learn mode into hub and subspecs"
```

---

### Task 3: Add `network-and-secrets/interception-and-proxy.md`

**Files:**
- Create: `.operator-shared/specs/network-and-secrets/interception-and-proxy.md`
- Modify: `.operator-shared/specs/network-and-secrets/` catalog entry (Task 5)

**Interfaces:** none (prose). Content is derived from issues #151 and #149, not from either existing spec.

- [ ] **Step 1: Draft the subspec** covering: `network.intercept-tls` (`auto`/`on`/`off`) and its interaction with secret-implied interception; `network.tls-bypass` (`*.suffix` and exact hosts; the package/registry use case that supersedes the in-VM docker CA workaround); the `proxy.*`/`--proxy` surface for SOCKS4/5 and HTTP CONNECT; **fingerprint completeness** (every field that reaches the VM is in `network.Policy.Fingerprint()`, so a change triggers the recreate path); the **proxy-reachability composition rule** (a proxy the policy denies silently denies all egress — auto-allow it or fail with an actionable error; the exact rule is decided by S1); and **credential fail-fast** (the SDK rejects credentials for anything but SOCKS5 — validate at config time). State the T1/T2 channel dependency by linking `learn-mode/observation-channel.md`.

- [ ] **Step 2: Add a summary entry** to `.operator-shared/specs/network-and-secrets/` in the catalog naming `interception-and-proxy.md` (Task 5 does the catalog edit; keep the two in the same PR).

- [ ] **Step 3: Verify and commit.**

Run: `make brain-check`
Expected: `brain-check: ok`

```bash
git add .operator-shared/specs/network-and-secrets/
git commit -m "docs(operator): add the interception-and-proxy spec"
```

---

### Task 4: Verify spec links and the doc gates

- [ ] **Step 1:** Run `make check`. Expected: PASS (includes `ci/check-docs.sh`; the specs under `.operator-shared/` are exempt from the docs-site link rules, but the hub's relative links to subspecs must resolve).
- [ ] **Step 2:** Grep for now-dangling references: `grep -rn "learn-mode.md" .operator-shared/ docs/ | grep -v superpowers`. Every hit must resolve, and any sentence that pointed at a moved section must point at its subspec.

---

### Task 5: Update the catalog

**Files:**
- Modify: `.operator-shared/catalog.md`

- [ ] **Step 1:** Update the `specs/onboarding/` entry: its Description and Read If must now name `learn-mode.md` (hub) and the `learn-mode/` subspecs (`observation-channel.md`, `home-read-back.md`, `findings-and-apply.md`).
- [ ] **Step 2:** Update the `specs/network-and-secrets/` entry to name `interception-and-proxy.md`.
- [ ] **Step 3:** Add the five plan entries (`learn-mode-program.md`, `learn-mode-settled-design.md`, `learn-mode-sdk-semantics.md`, `learn-mode-tls-interception.md`, `learn-mode-outbound-proxy.md`; the `readback` entry is added when S5 is written) — one-liners grouped, not one paragraph each.
- [ ] **Step 4: Verify and commit.**

Run: `make brain-check`
Expected: `brain-check: ok`

```bash
git add .operator-shared/catalog.md
git commit -m "docs(operator): catalog the learn-mode subspecs and subplans"
```

---

## Self-review

**Coverage.** Every section of the settled `learn-mode.md` maps to exactly one destination: hub (Intent, vocabulary, journeys, invariants, phasing, deferred) or a subspec (approaches/channel, read-back, findings/apply/harden). `interception-and-proxy.md` is new, sourced from #151/#149. The catalog and the doc-link gate are the verification.

**Proportion.** Prose only; no code blocks beyond the commit commands.

**Type consistency.** No types are defined here (the frozen interfaces live in the program index; signatures in the executable subplans).
