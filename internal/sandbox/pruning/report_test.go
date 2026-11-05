package pruning

import (
	"testing"
)

func TestPruneReportPrefix(t *testing.T) {
	if got := pruneReportPrefix(true); got != "dry-run: Would prune" {
		t.Errorf("pruneReportPrefix(true) = %q, want %q", got, "dry-run: Would prune")
	}
	if got := pruneReportPrefix(false); got != "Pruned" {
		t.Errorf("pruneReportPrefix(false) = %q, want %q", got, "Pruned")
	}
}
