package scripts

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestDeadcode ports every case of the bash harness ci/test-check-deadcode.sh,
// asserting the same exit codes and output substrings for the dead-code gate
// (ci/check-deadcode.sh) driven by throwaway fixture reports.
func TestDeadcode(t *testing.T) {
	// Build ci/deadcodecheck once and hand it to every case through
	// DEADCODE_CHECKER: the script's default rebuild per case would dominate
	// this harness. The first case below leaves DEADCODE_CHECKER empty so the
	// script's own `go build` path stays exercised too.
	checker := deadcodeChecker(t, repositoryRoot(t))

	t.Run("marked finding in every configuration", func(t *testing.T) {
		h := newDeadcodeHarness(t, checker, `//deadcode:ignore reason="test seam"`)
		h.writeAllConfigurations(h.deadLine, "Dead")

		h.expect("marked finding in every configuration", 0, "", map[string]string{"DEADCODE_CHECKER": ""})
	})

	t.Run("unmarked finding reported by one configuration", func(t *testing.T) {
		h := newDeadcodeHarness(t, checker, "")
		h.writeEmptyReport("no-tags")
		h.writeEmptyReport("msb")
		h.writeEmptyReport("integration+msb")
		h.writeReport("integration", h.deadLine, "Dead")

		h.expect("unmarked finding reported by one configuration", 1, "unreachable func: Dead", nil)
		h.expect("the report names the configuration", 1, "integration", nil)
	})

	t.Run("stale marker", func(t *testing.T) {
		h := newDeadcodeHarness(t, checker, `//deadcode:ignore reason="test seam"`)
		h.writeAllConfigurations(99, "Elsewhere")

		h.expect("stale marker", 1, "stale", nil)
		h.expect("the stale marker names its line", 1, h.source+":3", nil)
	})

	t.Run("malformed report payload", func(t *testing.T) {
		h := newDeadcodeHarness(t, checker, "")
		h.writeAllConfigurations(h.deadLine, "Dead")
		writeDoc(t, filepath.Join(h.reports, "no-tags.json"), "not a report\n")

		h.expect("malformed report payload", 2, "", nil)
	})

	t.Run("missing source file", func(t *testing.T) {
		h := newDeadcodeHarness(t, checker, "")
		h.writeAllConfigurations(h.deadLine, "Dead")
		writeDoc(t, h.files, filepath.Join(h.work, "src", "absent.go")+"\n")

		h.expect("missing source file", 2, "", nil)
	})

	t.Run("single configuration run", func(t *testing.T) {
		h := newDeadcodeHarness(t, checker, `//deadcode:ignore reason="test seam"`)
		h.writeReport("no-tags", h.deadLine, "Dead")

		h.expect("single configuration run", 0, "", map[string]string{"TAGS": ""})
	})

	t.Run("missing configuration payload", func(t *testing.T) {
		h := newDeadcodeHarness(t, checker, `//deadcode:ignore reason="test seam"`)
		h.writeReport("no-tags", h.deadLine, "Dead")

		h.expect("missing configuration payload", 2, "missing fixture", nil)
	})

	t.Run("package-scoped run skips staleness", func(t *testing.T) {
		h := newDeadcodeHarness(t, checker, `//deadcode:ignore reason="test seam"`)
		h.writeEmptyReport("no-tags")
		h.writeEmptyReport("integration")
		h.writeEmptyReport("msb")
		h.writeEmptyReport("integration+msb")

		h.expect(
			"package-scoped run skips staleness",
			0,
			"skipping the stale-marker pass",
			map[string]string{"PKG": "./cmd/..."},
		)
	})

	t.Run("supplied checker is executed", func(t *testing.T) {
		h := newDeadcodeHarness(t, checker, "")
		h.writeAllConfigurations(h.deadLine, "Dead")
		stub := filepath.Join(t.TempDir(), "stub-checker")
		if err := os.WriteFile(stub, []byte("#!/usr/bin/env bash\nexit 3\n"), 0o755); err != nil {
			t.Fatalf("write stub checker: %v", err)
		}

		h.expect("supplied checker is executed", 3, "", map[string]string{"DEADCODE_CHECKER": stub})
	})
}

// deadcodeChecker builds ci/deadcodecheck once for the whole test binary,
// returning the binary path so every case can pass it through DEADCODE_CHECKER.
// The build runs inside the first caller's temp dir (the top-level TestDeadcode
// t), which lives until that test returns.
var (
	deadcodeCheckerOnce sync.Once
	deadcodeCheckerBin  string
	errDeadcodeChecker  error
)

func deadcodeChecker(t *testing.T, root string) string {
	t.Helper()
	deadcodeCheckerOnce.Do(func() {
		path := filepath.Join(t.TempDir(), "deadcodecheck")
		build := exec.Command("go", "build", "-o", path, "./ci/deadcodecheck")
		build.Dir = root
		if out, err := build.CombinedOutput(); err != nil {
			errDeadcodeChecker = fmt.Errorf("build deadcodecheck: %w\n%s", err, out)
			return
		}
		deadcodeCheckerBin = path
	})
	if errDeadcodeChecker != nil {
		t.Fatal(errDeadcodeChecker)
	}
	return deadcodeCheckerBin
}

// deadcodeHarness is one fixture: a work dir holding a source file, its report
// directory, and the file list ci/check-deadcode.sh reads, mirroring the bash
// harness's new_fixture.
type deadcodeHarness struct {
	t        *testing.T
	checker  string
	work     string
	source   string
	reports  string
	files    string
	deadLine int
}

// newDeadcodeHarness creates the source tree every case starts from: one
// function Dead, either bare or documented by the given //deadcode:ignore line.
func newDeadcodeHarness(t *testing.T, checker, marker string) *deadcodeHarness {
	t.Helper()
	work := t.TempDir()
	source := filepath.Join(work, "src", "a.go")
	reports := filepath.Join(work, "reports")
	files := filepath.Join(work, "files.txt")
	if err := os.MkdirAll(filepath.Dir(source), 0o750); err != nil {
		t.Fatalf("create source dir: %v", err)
	}
	if err := os.MkdirAll(reports, 0o750); err != nil {
		t.Fatalf("create reports dir: %v", err)
	}

	h := &deadcodeHarness{t: t, checker: checker, work: work, source: source, reports: reports, files: files}
	sourceContent := "package main\n\nfunc Dead() {}\n"
	h.deadLine = 3
	if marker != "" {
		sourceContent = fmt.Sprintf("package main\n\n%s\nfunc Dead() {}\n", marker)
		h.deadLine = 4
	}
	writeDoc(t, source, sourceContent)
	writeDoc(t, files, source+"\n")
	return h
}

// deadcodeReportPackage mirrors one element of deadcode's -json array.
type deadcodeReportPackage struct {
	Name  string               `json:"Name"`
	Path  string               `json:"Path"`
	Funcs []deadcodeReportFunc `json:"Funcs"`
}

type deadcodeReportFunc struct {
	Name     string                 `json:"Name"`
	Position deadcodeReportPosition `json:"Position"`
}

type deadcodeReportPosition struct {
	File string `json:"File"`
	Line int    `json:"Line"`
	Col  int    `json:"Col"`
}

// writeReport writes the report payload for one build-tag configuration.
func (h *deadcodeHarness) writeReport(configuration string, line int, function string) {
	h.t.Helper()
	writeJSON(h.t, filepath.Join(h.reports, configuration+".json"), []deadcodeReportPackage{{
		Name: "p",
		Path: "example.com/p",
		Funcs: []deadcodeReportFunc{{
			Name:     function,
			Position: deadcodeReportPosition{File: h.source, Line: line, Col: 6},
		}},
	}})
}

// writeEmptyReport writes a clean report for one build-tag configuration.
func (h *deadcodeHarness) writeEmptyReport(configuration string) {
	h.t.Helper()
	writeDoc(h.t, filepath.Join(h.reports, configuration+".json"), "[]\n")
}

// writeAllConfigurations writes a finding for every build-tag configuration.
func (h *deadcodeHarness) writeAllConfigurations(line int, function string) {
	h.t.Helper()
	h.writeReport("no-tags", line, function)
	h.writeReport("integration", line, function)
	h.writeReport("msb", line, function)
	h.writeReport("integration+msb", line, function)
}

// run drives ci/check-deadcode.sh with the fixture environment plus overrides.
func (h *deadcodeHarness) run(overrides map[string]string) ScriptResult {
	h.t.Helper()
	env := map[string]string{
		"DEADCODE_VERSION":     "fixture",
		"DEADCODE_FIXTURE_DIR": h.reports,
		"DEADCODE_FILES":       h.files,
		"DEADCODE_CHECKER":     h.checker,
	}
	maps.Copy(env, overrides)
	root := repositoryRoot(h.t)
	return RunScript(h.t, root, env, filepath.Join(root, "ci", "check-deadcode.sh"))
}

// expect runs the gate and asserts its exit status and a combined
// stdout+stderr substring, the bash harness's expect.
func (h *deadcodeHarness) expect(name string, wantStatus int, wantMsg string, overrides map[string]string) {
	h.t.Helper()
	result := h.run(overrides)
	combined := result.Stdout + result.Stderr
	if result.Status != wantStatus {
		h.t.Errorf("%s: exit %d, want %d\noutput:\n%s", name, result.Status, wantStatus, combined)
		return
	}
	if wantMsg != "" && !strings.Contains(combined, wantMsg) {
		h.t.Errorf("%s: output missing %q\noutput:\n%s", name, wantMsg, combined)
	}
}
