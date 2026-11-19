package scripts

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBrainCheck ports every case of the bash harness scripts/test-brain-check.sh,
// asserting the same exit codes and output substrings the checker reports for
// each structural rule. The harness never runs git: it builds Operator brain
// fixtures in a temp dir and drives scripts/brain-check.sh against them.
func TestBrainCheck(t *testing.T) {
	t.Parallel()
	t.Run("valid brain exits 0", func(t *testing.T) {
		t.Parallel()
		root := newBrainCheckBrain(t, "good")

		result := runBrainCheck(t, root)

		assertStatus(t, result, 0)
		assertContains(t, "output", brainCheckOutput(result), "brain-check: ok")
	})

	t.Run("missing read_if fails", func(t *testing.T) {
		t.Parallel()
		root := newBrainCheckBrain(t, "no-frontmatter")
		writeDoc(t, filepath.Join(root, ".operator-shared", "specs", "demo", "contract.md"), `---
description: Demo contract.
---

# Demo
`)

		result := runBrainCheck(t, root)

		assertStatus(t, result, 1)
		assertContains(t, "output", brainCheckOutput(result), "specs/demo/contract.md: missing read_if")
	})

	t.Run("index without frontmatter fails", func(t *testing.T) {
		t.Parallel()
		root := newBrainCheckBrain(t, "no-index-frontmatter")
		writeDoc(t, filepath.Join(root, ".operator-shared", "index", "index.md"), "# Index\n")

		result := runBrainCheck(t, root)

		assertStatus(t, result, 1)
		assertContains(t, "output", brainCheckOutput(result), "index/index.md: missing description")
	})

	t.Run("broken link fails", func(t *testing.T) {
		t.Parallel()
		root := newBrainCheckBrain(t, "broken-link")
		writeDoc(
			t,
			filepath.Join(root, ".operator-shared", "specs", "demo", "contract.md"),
			"---\ndescription: Demo contract.\nread_if: Working on the demo.\n---\n\n# Demo\n\n- See [`../../guides/gone.md`](../../guides/gone.md).\n",
		)

		result := runBrainCheck(t, root)

		assertStatus(t, result, 1)
		assertContains(t, "output", brainCheckOutput(result), "broken link")
	})

	t.Run("external and fragment links pass", func(t *testing.T) {
		t.Parallel()
		root := newBrainCheckBrain(t, "external-link")
		writeDoc(t, filepath.Join(root, ".operator-shared", "guides", "demo.md"), `# Demo guide

- [docs](https://example.com/x)
- [section](#demo)
`)

		result := runBrainCheck(t, root)

		assertStatus(t, result, 0)
		assertContains(t, "output", brainCheckOutput(result), "brain-check: ok")
	})

	t.Run("uncatalogued guide fails", func(t *testing.T) {
		t.Parallel()
		root := newBrainCheckBrain(t, "uncatalogued")
		writeDoc(t, filepath.Join(root, ".operator-shared", "guides", "orphan.md"), "# Orphan\n")

		result := runBrainCheck(t, root)

		assertStatus(t, result, 1)
		assertContains(t, "output", brainCheckOutput(result), "guides/orphan.md")
	})

	t.Run("catalog of a missing path fails", func(t *testing.T) {
		t.Parallel()
		root := newBrainCheckBrain(t, "catalog-missing")
		writeDoc(t, filepath.Join(root, ".operator-shared", "catalog.md"), "# Stale\n")
		brainCheckAppendFile(
			t,
			filepath.Join(root, ".operator-shared", "catalog.md"),
			"- `guides/removed.md`\n  - Description: Removed guide.\n  - Read If: Nothing.\n",
		)

		result := runBrainCheck(t, root)

		assertStatus(t, result, 1)
		assertContains(t, "output", brainCheckOutput(result), "guides/removed.md: no such file")
	})

	t.Run("spec citing a plan fails", func(t *testing.T) {
		t.Parallel()
		root := newBrainCheckBrain(t, "cites-plan")
		brainCheckAppendFile(
			t,
			filepath.Join(root, ".operator-shared", "specs", "demo", "contract.md"),
			"\n- See `.operator-shared/plans/demo-plan.md` for the detail.\n",
		)

		result := runBrainCheck(t, root)

		assertStatus(t, result, 1)
		assertContains(t, "output", brainCheckOutput(result), "specs/demo/contract.md")
	})

	t.Run("Read If prefix fails", func(t *testing.T) {
		t.Parallel()
		root := newBrainCheckBrain(t, "read-if-prefix")
		writeDoc(t, filepath.Join(root, ".operator-shared", "catalog.md"), "# Stale\n")
		brainCheckAppendFile(
			t,
			filepath.Join(root, ".operator-shared", "catalog.md"),
			"- `specs/demo/`\n  - Description: Demo contracts.\n  - Read If: Implementing or reviewing the demo contract.\n",
		)

		result := runBrainCheck(t, root)

		assertStatus(t, result, 1)
		assertContains(t, "output", brainCheckOutput(result), "Implementing")
	})

	t.Run("private partition reference fails", func(t *testing.T) {
		t.Parallel()
		root := newBrainCheckBrain(t, "private-ref")
		brainCheckAppendFile(
			t,
			filepath.Join(root, ".operator-shared", "guides", "demo.md"),
			"\nSee also `.operator/notes.md`.\n",
		)

		result := runBrainCheck(t, root)

		assertStatus(t, result, 1)
		assertContains(t, "output", brainCheckOutput(result), "private partition")
	})

	t.Run("illustrative catalog path passes", func(t *testing.T) {
		t.Parallel()
		root := newBrainCheckBrain(t, "illustrative-path")
		brainCheckAppendFile(
			t,
			filepath.Join(root, ".operator-shared", "catalog.md"),
			"\nEach `specs/<subsystem>/` has one entry.\n",
		)

		result := runBrainCheck(t, root)

		assertStatus(t, result, 0)
		assertContains(t, "output", brainCheckOutput(result), "brain-check: ok")
	})

	t.Run("operator.md private reference passes", func(t *testing.T) {
		t.Parallel()
		root := newBrainCheckBrain(t, "operator-private")
		brainCheckAppendFile(
			t,
			filepath.Join(root, ".operator-shared", "operator.md"),
			"\n- **Private** (`.operator/`, local only).\n",
		)

		result := runBrainCheck(t, root)

		assertStatus(t, result, 0)
		assertContains(t, "output", brainCheckOutput(result), "brain-check: ok")
	})
}

// brainCheckOutput merges the script's streams, matching the bash harness's
// `2>&1` capture.
func brainCheckOutput(result ScriptResult) string {
	return result.Stdout + result.Stderr
}

// runBrainCheck runs scripts/brain-check.sh against a fixture root.
func runBrainCheck(t *testing.T, root string) ScriptResult {
	t.Helper()
	return RunScript(t, root, nil, repositoryScript(t, "brain-check.sh"), root)
}

// newBrainCheckBrain writes the well-formed Operator brain the bash harness's
// new_brain builds under <name>/.operator-shared, and returns the fixture root.
func newBrainCheckBrain(tb testing.TB, name string) string {
	root := filepath.Join(tb.TempDir(), name)
	for _, dir := range []string{
		filepath.Join(root, ".operator-shared", "index"),
		filepath.Join(root, ".operator-shared", "specs", "demo"),
		filepath.Join(root, ".operator-shared", "guides"),
		filepath.Join(root, ".operator-shared", "plans"),
	} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			tb.Fatalf("create %s: %v", dir, err)
		}
	}
	writeDoc(tb, filepath.Join(root, ".operator-shared", "operator.md"), "# Operator\n")
	writeDoc(tb, filepath.Join(root, ".operator-shared", "catalog.md"), brainCheckCatalog())
	writeDoc(
		tb,
		filepath.Join(root, ".operator-shared", "index", "index.md"),
		"---\ndescription: Demo index.\nread_if: Navigating the demo.\n---\n\n# Shared Project Index\n\n- `README.md` — Ditto.\n",
	)
	writeDoc(
		tb,
		filepath.Join(root, ".operator-shared", "specs", "demo", "contract.md"),
		"---\ndescription: Demo contract.\nread_if: Working on the demo.\n---\n\n# Demo\n\n- See the guide at [`../../guides/demo.md`](../../guides/demo.md).\n",
	)
	writeDoc(tb, filepath.Join(root, ".operator-shared", "guides", "demo.md"), "# Demo guide\n")
	writeDoc(tb, filepath.Join(root, ".operator-shared", "plans", "demo-plan.md"), "# Demo plan\n")
	writeDoc(tb, filepath.Join(root, "README.md"), "# Repo\n")
	return root
}

// brainCheckCatalog is the valid catalog the bash harness's new_brain writes: it
// names the spec subsystem, the guide, and the plan, with the two auto-injected
// entries the checker expects.
func brainCheckCatalog() string {
	return "# Catalog\n" +
		"\n" +
		"## Tree\n" +
		"\n" +
		"- `operator.md`\n" +
		"  - Description: Instructions.\n" +
		"  - Read If: Auto-injected.\n" +
		"- `catalog.md`\n" +
		"  - Description: This catalog.\n" +
		"  - Read If: Auto-injected.\n" +
		"\n" +
		"### `specs/` - Contracts\n" +
		"\n" +
		"- `specs/demo/`\n" +
		"  - Description: Demo contracts.\n" +
		"  - Read If: Working on the demo.\n" +
		"- `guides/demo.md`\n" +
		"  - Description: Demo guide.\n" +
		"  - Read If: Following the demo.\n" +
		"- `plans/demo-plan.md`\n" +
		"  - Description: Demo plan.\n" +
		"  - Read If: Executing the demo plan.\n"
}

// brainCheckAppendFile appends content to path, creating it when needed.
func brainCheckAppendFile(tb testing.TB, path, content string) {
	tb.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		tb.Fatalf("open %s: %v", path, err)
	}
	_, writeErr := file.WriteString(content)
	closeErr := file.Close()
	if writeErr != nil {
		tb.Fatalf("append %s: %v", path, writeErr)
	}
	if closeErr != nil {
		tb.Fatalf("close %s: %v", path, closeErr)
	}
}
