package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const sampleReport = `[
  {"Name":"image","Path":"github.com/inoio/agents-sandbox/internal/sandbox/image",
   "Funcs":[{"Name":"WithMockAgentVersion","Position":{"File":"internal/sandbox/image/fetch.go","Line":26,"Col":6},"Generated":false,"Marker":false}]}
]`

func TestParseReport(t *testing.T) {
	got, err := ParseReport([]byte(sampleReport))
	if err != nil {
		t.Fatalf("ParseReport: %v", err)
	}
	want := []Finding{{
		Package:  "github.com/inoio/agents-sandbox/internal/sandbox/image",
		Function: "WithMockAgentVersion",
		File:     "internal/sandbox/image/fetch.go",
		Line:     26,
		Column:   6,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseReport = %#v; want %#v", got, want)
	}
}

func TestParseReportOrdersFindings(t *testing.T) {
	data := []byte(`[
	  {"Name":"z","Path":"example.com/z","Funcs":[
	    {"Name":"Later","Position":{"File":"z.go","Line":5,"Col":1}},
	    {"Name":"Earlier","Position":{"File":"a.go","Line":9,"Col":1}}]},
	  {"Name":"a","Path":"example.com/a","Funcs":[
	    {"Name":"Deeper","Position":{"File":"a.go","Line":40,"Col":1}}]}]`)

	got, err := ParseReport(data)
	if err != nil {
		t.Fatalf("ParseReport: %v", err)
	}
	var order []string
	for _, f := range got {
		order = append(order, f.File+":"+strconv.Itoa(f.Line)+":"+f.Function)
	}
	want := []string{"a.go:9:Earlier", "a.go:40:Deeper", "z.go:5:Later"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("ParseReport order = %v; want %v", order, want)
	}
}

func TestParseReportAcceptsEmptyFuncList(t *testing.T) {
	got, err := ParseReport([]byte(`[{"Name":"p","Path":"example.com/p","Funcs":[]}]`))
	if err != nil {
		t.Fatalf("ParseReport: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ParseReport = %#v; want no findings", got)
	}
}

func TestParseReportRejectsFailedAnalysis(t *testing.T) {
	cases := map[string]string{
		"empty body":                     "",
		"json null":                      "null",
		"object instead of array":        "{}",
		"package without a Path":         `[{"Name":"p","Funcs":[]}]`,
		"package without a Funcs key":    `[{"Name":"p","Path":"example.com/p"}]`,
		"finding without a Position":     `[{"Name":"p","Path":"example.com/p","Funcs":[{"Name":"Dead"}]}]`,
		"finding with an empty Position": `[{"Name":"p","Path":"example.com/p","Funcs":[{"Name":"Dead","Position":{}}]}]`,
		"truncated array":                `[{"Name":"p","Path":"example.com/p","Funcs":[{"Name":"Dead","Position":{"File":"a.go"}}]`,
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ParseReport([]byte(data))
			if err == nil {
				t.Errorf("ParseReport(%q) = %#v, nil; want an error", data, got)
			}
			if got != nil {
				t.Errorf("ParseReport(%q) returned %#v alongside an error; want nil", data, got)
			}
		})
	}
}

func TestScanMarkers(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []Marker
	}{
		{
			name: "on the package doc comment",
			src: `//deadcode:ignore reason="test support"
package main

func live() {}
`,
			want: []Marker{{File: "a.go", Line: 1, Reason: "test support", Scope: ScopeFile}},
		},
		{
			name: "on a function's doc comment",
			src: `package main

//deadcode:ignore reason="test seam"
func dead() {}
`,
			want: []Marker{{File: "a.go", Line: 3, Reason: "test seam", Scope: ScopeDeclaration, SiteLine: 4}},
		},
		{
			name: "trailing a one-line function",
			src: `package main

func dead() {} //deadcode:ignore reason="test seam"
`,
			want: []Marker{{File: "a.go", Line: 3, Reason: "test seam", Scope: ScopeDeclaration, SiteLine: 3}},
		},
		{
			name: "inside a multi-line doc block",
			src: `package main

// dead is a helper.
//
//deadcode:ignore reason="test seam"
func dead() {
}
`,
			want: []Marker{{File: "a.go", Line: 5, Reason: "test seam", Scope: ScopeDeclaration, SiteLine: 6}},
		},
		{
			name: "trailing a multi-line body",
			src: `package main

func dead() {
	_ = 1
} //deadcode:ignore reason="test seam"
`,
			want: []Marker{{File: "a.go", Line: 5, Reason: "test seam", Scope: ScopeDeclaration, SiteLine: 3}},
		},
		{
			name: "several markers in source order",
			src: `package main

//deadcode:ignore reason="first"
func a() {}

func b() {} //deadcode:ignore reason="second"
`,
			want: []Marker{
				{File: "a.go", Line: 3, Reason: "first", Scope: ScopeDeclaration, SiteLine: 4},
				{File: "a.go", Line: 6, Reason: "second", Scope: ScopeDeclaration, SiteLine: 6},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ScanMarkers("a.go", []byte(tc.src))
			if err != nil {
				t.Fatalf("ScanMarkers: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ScanMarkers = %#v; want %#v", got, tc.want)
			}
		})
	}
}

func TestScanMarkersRejectsUnusable(t *testing.T) {
	cases := map[string]string{
		"bare directive": `package main

//deadcode:ignore
func dead() {}
`,
		"empty reason": `package main

//deadcode:ignore reason=""
func dead() {}
`,
		"reason followed by prose": `package main

//deadcode:ignore reason="x" because the linter is wrong
func dead() {}
`,
		"above a type": `package main

//deadcode:ignore reason="x"
type T struct{}
`,
		"above a variable": `package main

//deadcode:ignore reason="x"
var v = 1
`,
		"separated from its function by a blank line": `package main

//deadcode:ignore reason="x"

func dead() {}
`,
		"two markers on one declaration": `package main

//deadcode:ignore reason="a"
//deadcode:ignore reason="b"
func dead() {}
`,
		"a doc and a trailing marker on one declaration": `package main

//deadcode:ignore reason="a"
func dead() {} //deadcode:ignore reason="b"
`,
		"unparsable source": `package main

func (
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ScanMarkers("a.go", []byte(src))
			if err == nil {
				t.Errorf("ScanMarkers = %#v, nil; want an error", got)
			}
			if got != nil {
				t.Errorf("ScanMarkers returned %#v alongside an error; want nil", got)
			}
		})
	}
}

func TestScanMarkersInTestFileIsNotSpecial(t *testing.T) {
	src := `package main

//deadcode:ignore reason="test seam"
func dead() {}
`
	got, err := ScanMarkers("a_test.go", []byte(src))
	if err != nil {
		t.Fatalf("ScanMarkers: %v", err)
	}
	if len(got) != 1 || got[0].File != "a_test.go" {
		t.Errorf("ScanMarkers = %#v; want one marker recorded against a_test.go", got)
	}
}

func TestCheck(t *testing.T) {
	dead := Finding{File: "a.go", Line: 10, Function: "Dead", Column: 6, Source: "default"}
	covering := Marker{File: "a.go", Line: 9, Reason: "test seam", Scope: ScopeDeclaration, SiteLine: 10}
	elsewhere := Marker{File: "a.go", Line: 11, Reason: "test seam", Scope: ScopeDeclaration, SiteLine: 12}
	fileWide := Marker{File: "a.go", Line: 1, Reason: "test support", Scope: ScopeFile}

	cases := []struct {
		name         string
		markers      []Marker
		wantUnmarked int
		wantStale    int
	}{
		{name: "no marker", wantUnmarked: 1},
		{name: "declaration marker covering the finding", markers: []Marker{covering}},
		{name: "file marker covering the finding", markers: []Marker{fileWide}},
		{
			name:         "declaration marker covering something else",
			markers:      []Marker{elsewhere},
			wantUnmarked: 1,
			wantStale:    1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Check([]Finding{dead}, tc.markers, true)
			if len(got.Unmarked) != tc.wantUnmarked {
				t.Errorf("Unmarked = %#v; want %d entries", got.Unmarked, tc.wantUnmarked)
			}
			if len(got.Stale) != tc.wantStale {
				t.Errorf("Stale = %#v; want %d entries", got.Stale, tc.wantStale)
			}
			if want := tc.wantUnmarked == 0 && tc.wantStale == 0; got.Empty() != want {
				t.Errorf("Empty() = %v; want %v", got.Empty(), want)
			}
		})
	}
}

func TestCheckMergesSourcesAcrossConfigurations(t *testing.T) {
	first := Finding{File: "a.go", Line: 10, Function: "Dead", Source: "integration"}
	second := Finding{File: "a.go", Line: 10, Function: "Dead", Source: "default"}

	got := Check([]Finding{first, second}, nil, true)
	if len(got.Unmarked) != 1 {
		t.Fatalf("Unmarked = %#v; want one merged entry", got.Unmarked)
	}
	if want := "default,integration"; got.Unmarked[0].Source != want {
		t.Errorf("Source = %q; want %q", got.Unmarked[0].Source, want)
	}
}

func TestCheckScopedSkipsStale(t *testing.T) {
	dead := Finding{File: "a.go", Line: 10, Function: "Dead"}
	covering := Marker{File: "a.go", Line: 9, Reason: "test seam", Scope: ScopeDeclaration, SiteLine: 10}
	stale := Marker{File: "b.go", Line: 2, Reason: "test seam", Scope: ScopeDeclaration, SiteLine: 3}

	scoped := Check([]Finding{dead}, []Marker{covering, stale}, false)
	if len(scoped.Stale) != 0 || !scoped.Empty() {
		t.Errorf("Check(checkStale=false) = %#v; want a clean diagnosis", scoped)
	}
	if full := Check([]Finding{dead}, []Marker{covering, stale}, true); len(full.Stale) != 1 {
		t.Errorf("Check(checkStale=true).Stale = %#v; want the stale marker reported", full.Stale)
	}
}

func TestDiagnosisReportNamesTheTools(t *testing.T) {
	diag := Diagnosis{
		Unmarked: []Finding{{File: "a.go", Line: 10, Column: 6, Function: "Dead", Source: "integration+msb"}},
		Stale:    []Marker{{File: "b.go", Line: 12, Reason: "test seam", Scope: ScopeDeclaration, SiteLine: 13}},
		Unusable: []string{"c.go:3: malformed //deadcode:ignore"},
	}
	report := diag.Report()
	for _, want := range []string{"a.go:10:6", "unreachable func: Dead", "integration+msb", "b.go:12", "c.go:3"} {
		if !strings.Contains(report, want) {
			t.Errorf("Report() = %q; want it to mention %q", report, want)
		}
	}
}

// rig assembles a report directory and a source tree for the command's tests,
// mirroring how ci/check-deadcode.sh invokes it: report files named after their
// build-tag configuration, and module-relative source paths on stdin.
type rig struct {
	reportDir string
	sourceDir string
	listed    []string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	return &rig{reportDir: t.TempDir(), sourceDir: t.TempDir()}
}

func (r *rig) addSource(t *testing.T, name, src string) string {
	t.Helper()
	path := filepath.Join(r.sourceDir, name)
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	r.listed = append(r.listed, path)
	return path
}

func (r *rig) addReport(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.reportDir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write report %s: %v", name, err)
	}
}

func (r *rig) run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return invoke(append([]string{"-reports", r.reportDir}, args...), strings.Join(r.listed, "\n"))
}

func invoke(args []string, stdin string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(stdin), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func reportJSON(file string, line int, function string) string {
	return fmt.Sprintf(
		`[{"Name":"p","Path":"example.com/p","Funcs":[{"Name":%q,"Position":{"File":%q,"Line":%d,"Col":6}}]}]`,
		function, file, line)
}

func TestRunClean(t *testing.T) {
	r := newRig(t)
	src := r.addSource(t, "a.go", "package main\n\n//deadcode:ignore reason=\"test seam\"\nfunc Dead() {}\n")
	r.addReport(t, "default.json", reportJSON(src, 4, "Dead"))

	code, stdout, stderr := r.run(t)
	if code != 0 || stdout != "" || stderr != "" {
		t.Errorf("run = (%d, %q, %q); want a silent success", code, stdout, stderr)
	}
}

func TestRunFailsOnUnmarkedFinding(t *testing.T) {
	r := newRig(t)
	src := r.addSource(t, "a.go", "package main\n\nfunc Dead() {}\n")
	r.addReport(t, "integration.json", reportJSON(src, 3, "Dead"))

	code, stdout, _ := r.run(t)
	if code != 1 {
		t.Fatalf("run exit = %d; want 1", code)
	}
	for _, want := range []string{src + ":3", "unreachable func: Dead", "integration"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q; want it to mention %q", stdout, want)
		}
	}
}

func TestRunFailsOnStaleMarker(t *testing.T) {
	r := newRig(t)
	src := r.addSource(t, "a.go", "package main\n\n//deadcode:ignore reason=\"test seam\"\nfunc Dead() {}\n")
	r.addReport(t, "default.json", reportJSON(src, 99, "Elsewhere"))

	code, stdout, _ := r.run(t)
	if code != 1 {
		t.Fatalf("run exit = %d; want 1", code)
	}
	if !strings.Contains(stdout, "stale") || !strings.Contains(stdout, src+":3") {
		t.Errorf("stdout = %q; want a stale marker reported at %s:3", stdout, src)
	}
}

func TestRunReportsEveryUnusableMarker(t *testing.T) {
	r := newRig(t)
	first := r.addSource(t, "a.go", "package main\n\n//deadcode:ignore\nfunc Dead() {}\n")
	second := r.addSource(t, "b.go", "package main\n\n//deadcode:ignore reason=\"\"\nfunc Dead() {}\n")
	r.addReport(t, "default.json", "[]")

	code, stdout, _ := r.run(t)
	if code != 1 {
		t.Fatalf("run exit = %d; want 1", code)
	}
	for _, path := range []string{first, second} {
		if !strings.Contains(stdout, path+":3") {
			t.Errorf("stdout = %q; want the malformed marker at %s:3 reported too", stdout, path)
		}
	}
}

func TestRunScopedSkipsStaleMarkers(t *testing.T) {
	r := newRig(t)
	r.addSource(t, "a.go", "package main\n\n//deadcode:ignore reason=\"test seam\"\nfunc Dead() {}\n")
	r.addReport(t, "default.json", "[]")

	code, stdout, _ := r.run(t, "-scope", "single")
	if code != 0 {
		t.Errorf("run -scope single = (%d, %q); want a silent success", code, stdout)
	}
}

func TestRunExitsTwoOnBrokenInput(t *testing.T) {
	t.Run("no report directory", func(t *testing.T) {
		if code, _, stderr := invoke(nil, ""); code != 2 || !strings.Contains(stderr, "-reports") {
			t.Errorf("run = (%d, %q); want 2 mentioning -reports", code, stderr)
		}
	})
	t.Run("unknown scope", func(t *testing.T) {
		if code, _, _ := invoke([]string{"-reports", t.TempDir(), "-scope", "sometimes"}, ""); code != 2 {
			t.Errorf("run exit = %d; want 2", code)
		}
	})
	t.Run("malformed report", func(t *testing.T) {
		r := newRig(t)
		r.addReport(t, "default.json", "not a report")
		if code, _, stderr := r.run(t); code != 2 || stderr == "" {
			t.Errorf("run = (%d, %q); want 2 with a message on stderr", code, stderr)
		}
	})
	t.Run("missing source file", func(t *testing.T) {
		r := newRig(t)
		r.addReport(t, "default.json", "[]")
		r.listed = []string{filepath.Join(r.sourceDir, "absent.go")}
		if code, _, stderr := r.run(t); code != 2 || !strings.Contains(stderr, "absent.go") {
			t.Errorf("run = (%d, %q); want 2 naming the unreadable source file", code, stderr)
		}
	})
	t.Run("unknown flag", func(t *testing.T) {
		if code, _, _ := invoke([]string{"-nope"}, ""); code != 2 {
			t.Errorf("run exit = %d; want 2", code)
		}
	})
	t.Run("no reports", func(t *testing.T) {
		code, _, stderr := invoke([]string{"-reports", t.TempDir()}, "")
		if code != 2 || !strings.Contains(stderr, "no deadcode reports") {
			t.Errorf("run = (%d, %q); want 2 mentioning no reports", code, stderr)
		}
	})
	t.Run("unreadable source list", func(t *testing.T) {
		r := newRig(t)
		r.addReport(t, "default.json", reportJSON("a.go", 1, "Dead"))
		var stdout, stderr bytes.Buffer
		code := run([]string{"-reports", r.reportDir}, failingReader{}, &stdout, &stderr)
		if code != 2 || !strings.Contains(stderr.String(), "source paths") {
			t.Errorf("run = (%d, %q); want 2 mentioning source paths", code, stderr.String())
		}
	})
}

// failingReader stands in for a source list that disappears mid-read.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read boom") }

func TestScopeNameLabelsBothScopes(t *testing.T) {
	for _, tc := range []struct {
		scope Scope
		want  string
	}{
		{ScopeFile, "file"},
		{ScopeDeclaration, "declaration"},
	} {
		if got := scopeName(tc.scope); got != tc.want {
			t.Errorf("scopeName(%v) = %q; want %q", tc.scope, got, tc.want)
		}
	}
}

// TestMergeSourcesOrdersAcrossFilesAndLines drives the comparator's file and
// line branches; the end-to-end tests never merge findings across both.
func TestMergeSourcesOrdersAcrossFilesAndLines(t *testing.T) {
	got := mergeSources([]Finding{
		{File: "b.go", Line: 1, Source: "default"},
		{File: "a.go", Line: 9, Source: "default"},
		{File: "a.go", Line: 2, Source: "msb"},
	})
	want := []Finding{
		{File: "a.go", Line: 2, Source: "msb"},
		{File: "a.go", Line: 9, Source: "default"},
		{File: "b.go", Line: 1, Source: "default"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeSources = %#v; want %#v", got, want)
	}
}

func TestMergeSourceNames(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing string
		added    string
		want     string
	}{
		{"first source wins an absent existing", "", "default", "default"},
		{"an absent addition keeps the existing", "default", "", "default"},
		{"distinct names are unioned in sorted order", "default", "msb", "default,msb"},
		{"a repeated name is not duplicated", "default,msb", "msb", "default,msb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mergeSourceNames(tc.existing, tc.added); got != tc.want {
				t.Errorf("mergeSourceNames(%q, %q) = %q; want %q", tc.existing, tc.added, got, tc.want)
			}
		})
	}
}

func TestLoadFindingsRejectsUnreadableInput(t *testing.T) {
	t.Run("missing directory", func(t *testing.T) {
		if _, err := loadFindings(filepath.Join(t.TempDir(), "absent")); err == nil {
			t.Error("expected an error for a missing report directory")
		}
	})
	t.Run("a directory with no reports", func(t *testing.T) {
		if _, err := loadFindings(t.TempDir()); err == nil {
			t.Error("expected an error for a report directory holding no reports")
		}
	})
	t.Run("a nested directory is skipped", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "nested"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(dir, "default.json"),
			[]byte(reportJSON("a.go", 1, "Dead")),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		findings, err := loadFindings(dir)
		if err != nil {
			t.Fatalf("loadFindings: %v", err)
		}
		if len(findings) != 1 {
			t.Errorf("loadFindings = %v; want only the file entry", findings)
		}
	})
	t.Run("a dangling report symlink is an error", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Symlink(filepath.Join(dir, "absent.json"), filepath.Join(dir, "default.json")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := loadFindings(dir); err == nil {
			t.Error("expected an error for a report entry that cannot be read")
		}
	})
}

// TestMainProcess runs main() in a child process, where its os.Exit cannot end
// the test run. The child sees one unmarked finding, so main exits 1.
func TestMainProcess(t *testing.T) {
	r := newRig(t)
	src := r.addSource(t, "a.go", "package main\n\nfunc Dead() {}\n")
	r.addReport(t, "default.json", reportJSON(src, 3, "Dead"))

	out, code := runMainHelper(t, []string{"-reports", r.reportDir})
	if code != 1 {
		t.Fatalf("main exit = %d; want 1 (output: %s)", code, out)
	}
	if !strings.Contains(out, "unreachable func: Dead") {
		t.Errorf("main output = %q; want it to name Dead", out)
	}
}

// runMainHelper re-executes the test binary with GO_WANT_MAIN_HELPER=1 so the
// helper below calls main() in a clean process.
func runMainHelper(t *testing.T, args []string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestMainHelperProcess")
	cmd.Env = append(os.Environ(),
		"GO_WANT_MAIN_HELPER=1",
		"GO_MAIN_ARGS="+strings.Join(args, "\n"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return string(out), exitErr.ExitCode()
		}
		t.Fatalf("running helper: %v", err)
	}
	return string(out), 0
}

func TestMainHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MAIN_HELPER") != "1" {
		t.Skip("helper process only")
	}
	os.Args = append([]string{"deadcodecheck"}, strings.Split(os.Getenv("GO_MAIN_ARGS"), "\n")...)
	main()
	os.Exit(0)
}
