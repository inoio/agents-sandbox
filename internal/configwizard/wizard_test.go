package configwizard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func readUserConfig(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(configpaths.Get().UserConfigDir(), "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRunFirstRunAsksShallowTiersOnly(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	mock := &termio.Mock{IsInteractiveResult: true}
	mock.SelectFn = func(_ string, _ []termio.Choice, def string) (string, error) { return def, nil }
	if err := Run(noScope{}, mock, Selection{FirstRun: true}); err != nil {
		t.Fatal(err)
	}
	data := readUserConfig(t)
	if strings.Contains(data, "upgrade:") || strings.Contains(data, "notify:") {
		t.Errorf("first run wrote an integration key:\n%s", data)
	}
	if !LoadState().FirstRunDone {
		t.Error("FirstRunDone not recorded")
	}
}

func TestRunIsQuietAfterFirstRun(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	SaveState(State{Version: 1, FirstRunDone: true})
	mock := &termio.Mock{IsInteractiveResult: true}
	if err := Run(noScope{}, mock, Selection{FirstRun: true}); err != nil {
		t.Fatal(err)
	}
	if len(mock.SelectCalls) != 0 {
		t.Fatalf("quiet first run prompted %d times", len(mock.SelectCalls))
	}
}

func TestRunNonInteractiveDoesNothing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	mock := &termio.Mock{}
	if err := Run(noScope{}, mock, Selection{FirstRun: true}); err != nil {
		t.Fatal(err)
	}
	if len(mock.SelectCalls) != 0 {
		t.Fatal("non-interactive run prompted")
	}
}

func TestRunExplicitTierAsksThatTier(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	mock := &termio.Mock{IsInteractiveResult: true}
	mock.SelectFn = func(_ string, _ []termio.Choice, def string) (string, error) { return def, nil }
	if err := Run(noScope{}, mock, Selection{Tier: TierRuntime}); err != nil {
		t.Fatal(err)
	}
	if data := readUserConfig(t); !strings.Contains(data, "dind") {
		t.Errorf("runtime tier not written:\n%s", data)
	}
}
