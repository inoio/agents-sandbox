package mocks

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestInstalledFailFastDefaultsPanic covers what InitFailFastMocks does, not
// just which installers it calls: TestMain installs the defaults through the
// wrapper, so reaching any seam family must panic with that family's fail-fast
// message. Each family's own package tests the panic its installer produces;
// this covers the composition, and it fails if the wrapper stops installing a
// family. The probes call the cheapest read-only operation of each seam, so a
// missing install touches as little real state as possible.
func TestInstalledFailFastDefaultsPanic(t *testing.T) {
	tests := []struct {
		family      string
		panicsWith  string
		reachesSeam func(t *testing.T)
	}{
		{
			family:      "configpaths",
			panicsWith:  "WithMockConfigPaths",
			reachesSeam: func(*testing.T) { _, _ = configpaths.Get().UserHomeDir() },
		},
		{
			family:      "docker",
			panicsWith:  "WithDockerMock",
			reachesSeam: func(*testing.T) { _, _ = docker.Get().ImageInspect(context.Background(), "ref") },
		},
		{
			family:     "doctor",
			panicsWith: "doctor.CheckAll not mocked",
			reachesSeam: func(t *testing.T) {
				testUI := termio.NewTestMock(t)
				_ = doctor.CheckAll(context.Background(), &testUI)
			},
		},
		{
			family:      "msb",
			panicsWith:  "WithMsbMock",
			reachesSeam: func(*testing.T) { _, _ = msb.Get().StorageUsage(context.Background()) },
		},
		{
			family:      "notify",
			panicsWith:  "reached real execCommand",
			reachesSeam: func(*testing.T) { (&notify.DesktopNotifier{}).Notify(notify.Notification{}) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.family, func(t *testing.T) {
			defer func() {
				panicValue := recover()
				if panicValue == nil {
					t.Errorf(
						"the %s seam did not panic: InitFailFastMocks left its fail-fast default uninstalled",
						tt.family,
					)
					return
				}
				if message := fmt.Sprint(panicValue); !strings.Contains(message, tt.panicsWith) {
					t.Errorf(
						"the %s seam panicked with %q, want a fail-fast default mentioning %q",
						tt.family,
						message,
						tt.panicsWith,
					)
				}
			}()
			tt.reachesSeam(t)
		})
	}
}
