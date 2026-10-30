// Command deadcodecheck decides the dead-code gate. It decodes one or more
// `deadcode -json` reports and fails on functions that no `main` package can
// reach and that carry no `//deadcode:ignore` marker, and on markers that
// excuse nothing.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Finding is one function that deadcode reported as unreachable from any main
// package. Source names the report it came from so a diagnosis can say which
// build-tag configuration found it; ParseReport leaves it empty.
type Finding struct {
	Package  string
	Function string
	File     string
	Line     int
	Column   int
	Source   string
}

// deadcodePackage mirrors one element of deadcode's -json array. Funcs is a
// pointer so a missing key is distinguishable from an empty list.
type deadcodePackage struct {
	Name  string          `json:"Name"`
	Path  string          `json:"Path"`
	Funcs *[]deadcodeFunc `json:"Funcs"`
}

type deadcodeFunc struct {
	Name     string            `json:"Name"`
	Position *deadcodePosition `json:"Position"`
}

type deadcodePosition struct {
	File string `json:"File"`
	Line int    `json:"Line"`
	Col  int    `json:"Col"`
}

// ParseReport decodes one deadcode -json payload. A payload that is not a JSON
// array of packages, or that omits a field the gate needs, is an error: an
// empty finding set must stay distinguishable from an analysis that failed.
func ParseReport(data []byte) ([]Finding, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("empty deadcode report")
	}

	var packages *[]deadcodePackage
	if err := json.Unmarshal(trimmed, &packages); err != nil {
		return nil, fmt.Errorf("decode deadcode report: %w", err)
	}
	if packages == nil {
		return nil, errors.New("deadcode report is null")
	}

	findings := []Finding{}
	for _, pkg := range *packages {
		if pkg.Path == "" {
			return nil, fmt.Errorf("deadcode package %q has no Path", pkg.Name)
		}
		if pkg.Funcs == nil {
			return nil, fmt.Errorf("deadcode package %q has no Funcs key", pkg.Path)
		}
		for _, fn := range *pkg.Funcs {
			if fn.Position == nil || fn.Position.File == "" {
				return nil, fmt.Errorf("deadcode finding %q in %s has no file position", fn.Name, pkg.Path)
			}
			findings = append(findings, Finding{
				Package:  pkg.Path,
				Function: fn.Name,
				File:     fn.Position.File,
				Line:     fn.Position.Line,
				Column:   fn.Position.Col,
				// Source is stamped by the caller from the report it came from.
				Source: "",
			})
		}
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
	return findings, nil
}

// Scope is the extent a //deadcode:ignore marker excuses.
type Scope int

const (
	// ScopeDeclaration excuses the function the marker documents.
	ScopeDeclaration Scope = iota
	// ScopeFile excuses every function in the file.
	ScopeFile
)

// Marker is one //deadcode:ignore directive found in source. SiteLine is the
// line of the function a ScopeDeclaration marker documents, so it can be
// matched against a Finding; it is 0 for ScopeFile.
type Marker struct {
	File     string
	Line     int
	Reason   string
	Scope    Scope
	SiteLine int
}

var (
	// markerDirective recognises the directive wherever it appears, so a
	// marker missing its reason is reported rather than passed over.
	markerDirective = regexp.MustCompile(`^//\s*deadcode:ignore(\s|$)`)
	// markerStrict captures the reason from a well-formed marker.
	markerStrict = regexp.MustCompile(`^//\s*deadcode:ignore\s+reason="(.+)"\s*$`)
)

// ScanMarkers extracts every //deadcode:ignore marker from one Go source file.
// A marker attached to neither the package clause nor a function, a marker
// whose reason is missing or empty, and two markers covering the same site are
// all errors: the gate must never pass because a marker could not be read.
func ScanMarkers(path string, src []byte) ([]Marker, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	ranges := functionRanges(fset, file)
	var markers []Marker
	covered := map[[2]int]bool{}
	for _, group := range file.Comments {
		found, err := groupMarkers(path, fset, file, ranges, group)
		if err != nil {
			return nil, err
		}
		for _, marker := range found {
			key := [2]int{int(marker.Scope), marker.SiteLine}
			if covered[key] {
				return nil, fmt.Errorf(
					"%s:%d: a second //deadcode:ignore already covers this %s",
					path,
					marker.Line,
					scopeName(marker.Scope),
				)
			}
			covered[key] = true
			markers = append(markers, marker)
		}
	}

	sort.Slice(markers, func(i, j int) bool { return markers[i].Line < markers[j].Line })
	return markers, nil
}

// functionRange locates one function declaration in a file.
type functionRange struct {
	startLine int
	endLine   int
	end       token.Pos
}

// functionRanges records where every function declaration in file starts and
// ends, so a comment group can be attributed to the function it documents.
func functionRanges(fset *token.FileSet, file *ast.File) []functionRange {
	var ranges []functionRange
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		ranges = append(ranges, functionRange{
			startLine: fset.Position(fn.Pos()).Line,
			endLine:   fset.Position(fn.End()).Line,
			end:       fn.End(),
		})
		return true
	})
	return ranges
}

// groupMarkers returns the markers one comment group carries.
func groupMarkers(
	path string,
	fset *token.FileSet,
	file *ast.File,
	ranges []functionRange,
	group *ast.CommentGroup,
) ([]Marker, error) {
	scope, siteLine, attached := groupSite(fset, file, ranges, group)
	var markers []Marker
	for _, comment := range group.List {
		reason, isMarker, err := parseMarker(comment.Text)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, fset.Position(comment.Pos()).Line, err)
		}
		if !isMarker {
			continue
		}
		line := fset.Position(comment.Pos()).Line
		if !attached {
			return nil, fmt.Errorf(
				"%s:%d: //deadcode:ignore is attached to neither the package clause nor a function",
				path,
				line,
			)
		}
		markers = append(markers, Marker{File: path, Line: line, Reason: reason, Scope: scope, SiteLine: siteLine})
	}
	return markers, nil
}

// groupSite is the scope a comment group documents, if any. A group trailing a
// declaration on its last line wins over one that precedes the next
// declaration, matching how Go itself attributes comments.
func groupSite(
	fset *token.FileSet,
	file *ast.File,
	ranges []functionRange,
	group *ast.CommentGroup,
) (Scope, int, bool) {
	if group == file.Doc {
		return ScopeFile, 0, true
	}
	for _, r := range ranges {
		if group.Pos() > r.end && fset.Position(group.Pos()).Line == r.endLine {
			return ScopeDeclaration, r.startLine, true
		}
	}
	for _, r := range ranges {
		if fset.Position(group.End()).Line == r.startLine-1 {
			return ScopeDeclaration, r.startLine, true
		}
	}
	return ScopeDeclaration, 0, false
}

// parseMarker reports whether a comment carries the //deadcode:ignore
// directive and, if it does, the reason it must supply.
func parseMarker(text string) (string, bool, error) {
	if !markerDirective.MatchString(text) {
		return "", false, nil
	}
	matches := markerStrict.FindStringSubmatch(text)
	if matches == nil {
		return "", true, errors.New(
			`malformed //deadcode:ignore: want //deadcode:ignore reason="…" with a non-empty reason`,
		)
	}
	return matches[1], true, nil
}

// scopeName labels a scope for a diagnostic.
func scopeName(scope Scope) string {
	if scope == ScopeFile {
		return "file"
	}
	return "declaration"
}

// Diagnosis is the gate's verdict: the findings no marker excused, the markers
// that excused nothing, and the markers that could not be read at all.
type Diagnosis struct {
	Unmarked []Finding
	Stale    []Marker
	Unusable []string
}

// Empty reports whether the gate passes.
func (d Diagnosis) Empty() bool {
	return len(d.Unmarked) == 0 && len(d.Stale) == 0 && len(d.Unusable) == 0
}

// Report renders the diagnosis one line per problem, in deadcode's own
// file:line:col: shape so a red build names what to fix without a rerun.
func (d Diagnosis) Report() string {
	var lines []string
	for _, finding := range d.Unmarked {
		line := fmt.Sprintf(
			"%s:%d:%d: unreachable func: %s",
			finding.File,
			finding.Line,
			finding.Column,
			finding.Function,
		)
		if finding.Source != "" {
			line += fmt.Sprintf(" (%s)", finding.Source)
		}
		lines = append(lines, line)
	}
	for _, marker := range d.Stale {
		lines = append(
			lines,
			fmt.Sprintf("%s:%d: stale //deadcode:ignore %q excuses nothing", marker.File, marker.Line, marker.Reason),
		)
	}
	lines = append(lines, d.Unusable...)
	return strings.Join(lines, "\n")
}

// Check decides whether the reported findings are excused. A ScopeDeclaration
// marker covers the function on its own line, a ScopeFile marker covers every
// function in its file. Staleness — a marker covering nothing — is checked only
// when checkStale is set, so a package-scoped inspection does not report the
// rest of the module's markers as stale.
func Check(findings []Finding, markers []Marker, checkStale bool) Diagnosis {
	var diagnosis Diagnosis

	used := make([]bool, len(markers))
	for _, finding := range mergeSources(findings) {
		excused := false
		for i, marker := range markers {
			if marker.File != finding.File {
				continue
			}
			if marker.Scope == ScopeFile || marker.SiteLine == finding.Line {
				used[i] = true
				excused = true
			}
		}
		if !excused {
			diagnosis.Unmarked = append(diagnosis.Unmarked, finding)
		}
	}

	if checkStale {
		for i, marker := range markers {
			if !used[i] {
				diagnosis.Stale = append(diagnosis.Stale, marker)
			}
		}
	}
	return diagnosis
}

// mergeSources collapses findings reported by several build-tag configurations
// into one, naming every configuration that saw it, and orders the result for
// stable output.
func mergeSources(findings []Finding) []Finding {
	index := map[string]int{}
	var merged []Finding
	for _, finding := range findings {
		key := fmt.Sprintf("%s:%d", finding.File, finding.Line)
		at, seen := index[key]
		if !seen {
			index[key] = len(merged)
			merged = append(merged, finding)
			continue
		}
		merged[at].Source = mergeSourceNames(merged[at].Source, finding.Source)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].File != merged[j].File {
			return merged[i].File < merged[j].File
		}
		return merged[i].Line < merged[j].Line
	})
	return merged
}

// mergeSourceNames unions two comma-joined configuration names.
func mergeSourceNames(existing, added string) string {
	if added == "" {
		return existing
	}
	if existing == "" {
		return added
	}
	names := strings.Split(existing, ",")
	for name := range strings.SplitSeq(added, ",") {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// verdicts the command exits with.
const (
	exitClean     = 0
	exitDiagnosis = 1
	exitError     = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run decides the gate for one invocation. args comes from the command line,
// stdin carries the module-relative source paths to scan one per line, and the
// claims are reported on stdout.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("deadcodecheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	reportDir := flags.String("reports", "", "directory holding one deadcode -json payload per build-tag configuration")
	scope := flags.String(
		"scope",
		"all",
		"all to check that markers excuse something, single to skip that for a package-scoped inspection",
	)
	if err := flags.Parse(args); err != nil {
		return exitError
	}
	if *reportDir == "" {
		fmt.Fprintln(stderr, "deadcodecheck: -reports is required")
		return exitError
	}
	checkStale := false
	switch *scope {
	case "all":
		checkStale = true
	case "single":
	default:
		fmt.Fprintf(stderr, "deadcodecheck: unknown -scope %q, want all or single\n", *scope)
		return exitError
	}

	findings, err := loadFindings(*reportDir)
	if err != nil {
		fmt.Fprintf(stderr, "deadcodecheck: %v\n", err)
		return exitError
	}

	paths, err := sourcePaths(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "deadcodecheck: %v\n", err)
		return exitError
	}

	var markers []Marker
	var unusable []string
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "deadcodecheck: %v\n", err)
			return exitError
		}
		found, err := ScanMarkers(path, src)
		if err != nil {
			// A marker that cannot be read is itself a failure, but it must not
			// hide the markers in the files that follow.
			unusable = append(unusable, err.Error())
			continue
		}
		markers = append(markers, found...)
	}

	diagnosis := Check(findings, markers, checkStale)
	diagnosis.Unusable = unusable
	if diagnosis.Empty() {
		return exitClean
	}
	fmt.Fprintln(stdout, diagnosis.Report())
	return exitDiagnosis
}

// loadFindings decodes every report in dir, naming each finding after the
// configuration its report file is named for. A directory with no reports is an
// error: the gate must never treat a missing analysis as a clean one.
func loadFindings(dir string) ([]Finding, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read report directory %s: %w", dir, err)
	}
	var findings []Finding
	reports := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read report %s: %w", entry.Name(), err)
		}
		parsed, err := ParseReport(data)
		if err != nil {
			return nil, fmt.Errorf("report %s: %w", entry.Name(), err)
		}
		reports++
		source := strings.TrimSuffix(entry.Name(), ".json")
		for _, finding := range parsed {
			finding.Source = source
			findings = append(findings, finding)
		}
	}
	if reports == 0 {
		return nil, fmt.Errorf("no deadcode reports in %s", dir)
	}
	return findings, nil
}

// sourcePaths reads the source paths to scan, one per line.
func sourcePaths(stdin io.Reader) ([]string, error) {
	var paths []string
	lines := bufio.NewScanner(stdin)
	lines.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for lines.Scan() {
		if path := strings.TrimSpace(lines.Text()); path != "" {
			paths = append(paths, path)
		}
	}
	if err := lines.Err(); err != nil {
		return nil, fmt.Errorf("read source paths: %w", err)
	}
	return paths, nil
}
