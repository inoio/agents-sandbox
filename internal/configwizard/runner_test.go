package configwizard

import (
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configfile"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func mustConfig(t *testing.T, data string) *configfile.Config {
	t.Helper()
	cfg, err := configfile.Parse("config.yaml", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

type noScope struct{}

func (noScope) KeySetAboveUserConfig(string) bool { return false }

type higherScope map[string]bool

func (h higherScope) KeySetAboveUserConfig(key string) bool { return h[key] }

func TestRunnerAcceptsGroupDefaultsWithOneKeystroke(t *testing.T) {
	cfg := mustConfig(t, "{}\n")
	mock := &termio.Mock{IsInteractiveResult: true}
	mock.SelectFn = func(prompt string, _ []termio.Choice, def string) (string, error) {
		if strings.Contains(prompt, "recommended resource") {
			return "yes", nil
		}
		return def, nil
	}
	r := &runner{
		questions: DefaultCatalog(), tiers: map[int]bool{TierResources: true},
		scope: noScope{}, state: State{}, ui: mock, config: cfg,
	}
	if _, err := r.run(); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"cpus": "4", "memory": "8G", "disk-size": "16G", "workspace-quota": "16G"} {
		if got, _ := cfg.GetString(strings.Split(key, ".")); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestRunnerEarlyExitAppliesDefaultsToRest(t *testing.T) {
	cfg := mustConfig(t, "{}\n")
	mock := &termio.Mock{IsInteractiveResult: true}
	mock.SelectFn = func(prompt string, _ []termio.Choice, def string) (string, error) {
		switch {
		case strings.Contains(prompt, "recommended resource"):
			return "no", nil
		case strings.Contains(prompt, "disk"):
			return stopChoice, nil
		default:
			return def, nil
		}
	}
	r := &runner{
		questions: DefaultCatalog(), tiers: map[int]bool{TierResources: true},
		scope: noScope{}, state: State{}, ui: mock, config: cfg,
	}
	stop, err := r.run()
	if err != nil {
		t.Fatal(err)
	}
	if stop >= len(DefaultCatalog()) {
		t.Fatalf("expected an early stop, got %d", stop)
	}
	if got, _ := cfg.GetString([]string{"workspace-quota"}); got != "16G" {
		t.Errorf("workspace-quota default not applied after early exit: %q", got)
	}
}

func TestRunnerSkipsHigherScopeAndUnsetCollapses(t *testing.T) {
	cfg := mustConfig(t, "cpus: 12\n")
	mock := &termio.Mock{IsInteractiveResult: true}
	r := &runner{
		questions: DefaultCatalog(), tiers: map[int]bool{TierResources: true},
		scope: higherScope{"cpus": true}, state: State{}, ui: mock, config: cfg,
	}
	if _, err := r.run(); err != nil {
		t.Fatal(err)
	}
	if len(mock.WarnCalls)+len(mock.InfoCalls) == 0 {
		t.Error("expected a skip to be reported")
	}
	if got, _ := cfg.GetInt([]string{"cpus"}); got != 12 {
		t.Errorf("cpus = %d, want the higher-scope value 12 retained", got)
	}
}

func TestRunnerAskOnlyIfUnsetSkipsSetKey(t *testing.T) {
	cfg := mustConfig(t, "agent: pi\n")
	mock := &termio.Mock{IsInteractiveResult: true}
	r := &runner{
		questions: DefaultCatalog(), tiers: map[int]bool{TierEssentials: true},
		scope: noScope{}, state: State{}, ui: mock, config: cfg,
	}
	if _, err := r.run(); err != nil {
		t.Fatal(err)
	}
	if got, _ := cfg.GetString([]string{"agent"}); got != "pi" {
		t.Errorf("agent = %q, want the existing value pi", got)
	}
	for _, prompt := range mock.SelectCalls {
		if strings.Contains(prompt, "coding agent") {
			t.Errorf("agent was asked despite being set: %q", prompt)
		}
	}
}

func TestRunnerWrittenCountsOnlyChangedValues(t *testing.T) {
	cfg := mustConfig(t, "cpus: 12\n")
	mock := &termio.Mock{IsInteractiveResult: true}
	mock.SelectFn = func(_ string, _ []termio.Choice, def string) (string, error) { return def, nil }
	r := &runner{
		questions: DefaultCatalog(), tiers: map[int]bool{TierResources: true},
		scope: noScope{}, state: State{}, ui: mock, config: cfg,
	}
	if _, err := r.run(); err != nil {
		t.Fatal(err)
	}
	if r.Written() != 3 {
		t.Errorf("Written() = %d, want 3 (memory, disk-size, workspace-quota)", r.Written())
	}
	if !r.Changed() {
		t.Error("Changed() = false, want true")
	}
}
