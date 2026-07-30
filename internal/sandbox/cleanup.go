package sandbox

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/output"
)

var autoPruneOnce sync.Once

// AutoPrune runs the prune logic once per process with the given threshold.
// Threshold of 0 defaults to 30 days.
func AutoPrune(ctx context.Context, threshold time.Duration, logger *output.Printer) {
	if threshold == 0 {
		threshold = 30 * 24 * time.Hour
	}
	autoPruneOnce.Do(func() {
		report, err := Prune(ctx, threshold, true, logger)
		if report != nil {
			printPruneSummary(report, err)
		}
	})
}

func printPruneSummary(report *StaleReport, error error) {
	action := "Pruned"

	parts := []string{
		action,
		fmt.Sprintf("%d VMs", report.PrunedVMs),
		fmt.Sprintf("%d home volumes", report.PrunedVolumes),
		fmt.Sprintf("%d docker images", report.PrunedDockerImages),
		fmt.Sprintf("%d msb images", report.PrunedMSBImages),
		fmt.Sprintf("%d task sandboxes", report.PrunedTaskSandboxes),
		fmt.Sprintf("%d clone volumes", report.PrunedCloneVolumes),
	}

	fmt.Println(strings.Join(parts, ", "))
	if error != nil {
		fmt.Printf("error occured: %s", error)
	}
}
