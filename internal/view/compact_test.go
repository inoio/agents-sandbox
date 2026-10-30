package view

import (
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/pruning"
)

func TestNewCompactReportJSONMapsSandboxesAndDisks(t *testing.T) {
	report := pruning.CompactReport{DryRun: true, Entries: []pruning.CompactEntry{{
		Name: "proj-abc", Slug: "proj",
		Result: msbSdk.DiskCompactionResult{
			InputLayers: 5, SelectedLayers: 4, OutputLayers: 2, MaterializedBytes: 10, TotalUs: 20, PauseUs: 30,
			Disks: []msbSdk.DiskCompactionDiskResult{{
				GuestPath: "/", InputLayers: 5, SelectedLayers: 4, OutputLayers: 2, MaterializedBytes: 10, TotalUs: 20,
			}},
		},
	}}}

	got := marshal(t, NewCompactReportJSON(report))

	want := `{"dry_run":true,"sandboxes":[{"name":"proj-abc","slug":"proj","input_layers":5,"selected_layers":4,` +
		`"output_layers":2,"materialized_bytes":10,"total_us":20,"pause_us":30,"disks":[{"guest_path":"/",` +
		`"input_layers":5,"selected_layers":4,"output_layers":2,"materialized_bytes":10,"total_us":20}]}]}`
	if got != want {
		t.Fatalf("json =\n%s\nwant\n%s", got, want)
	}
}

func TestNewCompactReportJSONEmptyListsAreArrays(t *testing.T) {
	if got := marshal(t, NewCompactReportJSON(pruning.CompactReport{})); got != `{"dry_run":false,"sandboxes":[]}` {
		t.Fatalf("json = %s", got)
	}
}
