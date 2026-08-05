package sandbox

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
)

//nolint:gochecknoglobals // once-per-process singleton is the right pattern
var autoPruneOnce sync.Once

// AutoPrune runs the prune logic once per process with the given threshold.
// Threshold of 0 defaults to 30 days.
func AutoPrune(ctx context.Context, threshold time.Duration, ui stdio.UI) {
	if threshold == 0 {
		threshold = 30 * 24 * time.Hour
	}
	autoPruneOnce.Do(func() {
		report, err := Prune(ctx, threshold, true, ui)
		if report != nil {
			printPruneSummary(ui, report, err)
		}
	})
}

func printPruneSummary(ui stdio.UI, report *StaleReport, err error) {
	parts := []string{
		fmt.Sprintf("%d VMs", report.PrunedVMs),
		fmt.Sprintf("%d home volumes", report.PrunedVolumes),
		fmt.Sprintf("%d docker images", report.PrunedDockerImages),
		fmt.Sprintf("%d msb images", report.PrunedMSBImages),
		fmt.Sprintf("%d task sandboxes", report.PrunedTaskSandboxes),
		fmt.Sprintf("%d clone volumes", report.PrunedCloneVolumes),
	}
	ui.Infof("Pruned %s", strings.Join(parts, ", "))
	if err != nil {
		ui.Errorf("error occurred: %s", err)
	}
}
