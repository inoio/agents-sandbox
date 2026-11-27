package journeys

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
)

var updateCatalogue = flag.Bool("update-catalogue", false, "regenerate "+catalogueFileName)

// TestJourneyCatalogueIsCurrent keeps the generated scenario catalogue in step
// with the journey table: it fails when the file is stale and rewrites it with
// -update-catalogue.
func TestJourneyCatalogueIsCurrent(t *testing.T) {
	want := renderCatalogue(Journeys, Exemptions)
	if *updateCatalogue {
		//testenv:allow regenerating the checked-in catalogue on request
		if err := os.WriteFile(
			catalogueFileName,
			[]byte(want),
			0o644,
		); err != nil {
			t.Fatalf("write %s: %v", catalogueFileName, err)
		}
	}
	//testenv:allow the checked-in catalogue beside the test
	got, err := os.ReadFile(catalogueFileName)
	if err != nil {
		t.Fatalf("read %s: %v (regenerate it with -update-catalogue)", catalogueFileName, err)
	}
	if string(got) != want {
		t.Errorf(
			"%s is stale (%s); regenerate it: go test -tags microsandbox_ffi_path ./test/msb/journeys/"+
				" -run TestJourneyCatalogueIsCurrent -args -update-catalogue",
			catalogueFileName, firstDifference(string(got), want),
		)
	}
}

func TestRenderCatalogueShowsTheScenarioAndExemptions(t *testing.T) {
	journeys := []Journey{{Name: "core", Harnesses: []string{"opencode2"}, Network: "host", Steps: []Step{
		Given(Step{Name: "a fresh home"}),
		Then(AsCheck(Step{Name: "the reply shows"})),
	}}}
	text := renderCatalogue(journeys, map[string]map[string]string{"core": {"codex": "later", "pi": "later"}})

	for _, want := range []string{
		"## core", "Runs on: opencode2", "Network profile: `host`", "- codex, pi: later",
		"Scenario: core\n  Given a fresh home\n  # check\n  Then the reply shows\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("catalogue lacks %q:\n%s", want, text)
		}
	}
}

// firstDifference names the first line where got and want differ.
func firstDifference(got, want string) string {
	gotLines, wantLines := strings.Split(got, "\n"), strings.Split(want, "\n")
	for index := range max(len(gotLines), len(wantLines)) {
		var gotLine, wantLine string
		if index < len(gotLines) {
			gotLine = gotLines[index]
		}
		if index < len(wantLines) {
			wantLine = wantLines[index]
		}
		if gotLine != wantLine {
			return fmt.Sprintf("line %d is %q, want %q", index+1, gotLine, wantLine)
		}
	}
	return "no line differs"
}
