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

func writeUserConfig(t *testing.T, data string) {
	t.Helper()
	dir := configpaths.Get().UserConfigDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func acceptDefaults(ui *termio.Mock) {
	ui.SelectFn = func(_ string, _ []termio.Choice, def string) (string, error) { return def, nil }
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

func TestRunExistingUserKeepsValuesWhenAcceptingDefaults(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	existing := "agent: pi\n" +
		"network:\n" +
		"  profile: public\n" +
		"cpus: 16\n" +
		"memory: 32G\n" +
		"disk-size: 64G\n" +
		"workspace-quota: 128G\n" +
		"dind: true\n" +
		"tmp-size: 4G\n" +
		"log-level: verbose\n"
	writeUserConfig(t, existing)
	mock := &termio.Mock{IsInteractiveResult: true}
	acceptDefaults(mock)
	if err := Run(noScope{}, mock, Selection{FirstRun: true}); err != nil {
		t.Fatal(err)
	}
	if got := readUserConfig(t); got != existing {
		t.Errorf("existing user config was rewritten:\n got: %s\nwant: %s", got, existing)
	}
	for _, call := range mock.InfoCalls {
		if strings.Contains(call, "Wrote ") {
			t.Errorf("wizard reported a config write: %q", call)
		}
	}
}

func TestRunExplicitEssentialsTierDoesNotMarkFirstRunDone(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	mock := &termio.Mock{IsInteractiveResult: true}
	acceptDefaults(mock)
	if err := Run(noScope{}, mock, Selection{Tier: TierEssentials}); err != nil {
		t.Fatal(err)
	}
	if LoadState().FirstRunDone {
		t.Error("explicit essentials tier must not suppress the later first-run prompts")
	}
}

func TestRunWriteSummaryCountsOnlyChangedValues(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	mock := &termio.Mock{IsInteractiveResult: true}
	acceptDefaults(mock)
	if err := Run(noScope{}, mock, Selection{FirstRun: true}); err != nil {
		t.Fatal(err)
	}
	if len(mock.InfoCalls) == 0 ||
		mock.InfoCalls[len(mock.InfoCalls)-1] != "Wrote 9 setting(s) to "+filepath.Join(
			configpaths.Get().UserConfigDir(),
			"config.yaml",
		) {
		t.Errorf("info calls = %v, want a summary of exactly the 9 written values", mock.InfoCalls)
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
