package driver

import (
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
)

func TestHarnessSpecsCoverEveryRegisteredAgent(t *testing.T) {
	names := agent.Names()
	if len(names) == 0 {
		t.Fatal("no agents registered")
	}
	for _, name := range names {
		if _, ok := Specs[name]; !ok {
			t.Errorf("registered harness %q has no smoke-test spec; add its quit key", name)
		}
	}
	for name := range Specs {
		if _, ok := agent.Lookup(name); !ok {
			t.Errorf("smoke-test spec %q matches no registered harness", name)
		}
	}
}

// TestHarnessSpecsQuitKeyNonEmpty pins that every harness names the key the
// smoke test taps until its process goes.
func TestHarnessSpecsQuitKeyNonEmpty(t *testing.T) {
	for name, spec := range Specs {
		if len(spec.QuitKey) == 0 {
			t.Errorf("harness %q has no key for the smoke test to press", name)
		}
	}
}

// TestSpecNetworkOrDefault pins the `--network` derivation: a spec's own profile
// passes through, and one with no profile runs under the secure default.
func TestSpecNetworkOrDefault(t *testing.T) {
	if got := (Spec{Network: PublicNetwork}).NetworkOrDefault(); got != PublicNetwork {
		t.Errorf("NetworkOrDefault() = %q, want the spec's own %q", got, PublicNetwork)
	}
	if got := (Spec{}).NetworkOrDefault(); got != NetworkNone {
		t.Errorf("NetworkOrDefault() = %q, want the default %q", got, NetworkNone)
	}
}

// TestOhMyPiSeedsAHomeConfigWithoutSetupWizard pins how the smoke test reaches
// oh-my-pi's editor: the seeded home config turns off the first-run setup
// wizard, which would otherwise take over the screen, and no egress is needed.
func TestOhMyPiSeedsAHomeConfigWithoutSetupWizard(t *testing.T) {
	spec, ok := Specs["oh-my-pi"]
	if !ok {
		t.Fatal("oh-my-pi has no smoke-test spec")
	}
	if !spec.KeepsNormalBuffer {
		t.Error("oh-my-pi KeepsNormalBuffer = false; its TUI renders in the normal buffer")
	}
	if spec.ReadyMarker != ohMyPiReadyMarker {
		t.Errorf("oh-my-pi ReadyMarker = %q, want %q; without it the subtest waits out the settle",
			spec.ReadyMarker, ohMyPiReadyMarker)
	}
	if spec.Network != "" {
		t.Errorf("oh-my-pi Network = %q, want the default `none`", spec.Network)
	}
	cfg, ok := spec.Seeds.HomeFiles[ohMyPiConfigPath]
	if !ok {
		t.Fatalf("oh-my-pi Seeds.HomeFiles missing %s", ohMyPiConfigPath)
	}
	if !strings.Contains(cfg, "setupWizard: false") {
		t.Errorf("oh-my-pi seeded config must turn off the setup wizard:\n%s", cfg)
	}
}

// TestKimiCodeSeedsATrustedWorkspaceThroughASnippet pins how the smoke test
// reaches kimi-code's prompt: config.toml is the merged-config target, so the
// trust opt-out is seeded as a launcher config snippet, the welcome banner
// marks the normal-buffer UI being up, and no egress is needed.
func TestKimiCodeSeedsATrustedWorkspaceThroughASnippet(t *testing.T) {
	spec, ok := Specs["kimi-code"]
	if !ok {
		t.Fatal("kimi-code has no smoke-test spec")
	}
	if !spec.KeepsNormalBuffer || spec.ReadyMarker != kimiCodeReadyMarker {
		t.Errorf("kimi-code KeepsNormalBuffer = %v, ReadyMarker = %q; want true and %q",
			spec.KeepsNormalBuffer, spec.ReadyMarker, kimiCodeReadyMarker)
	}
	if spec.Network != "" {
		t.Errorf("kimi-code Network = %q, want the default `none`", spec.Network)
	}
	cfg, ok := spec.Seeds.AgentSnippets[kimiCodeSmokeSnippet]
	if !ok {
		t.Fatalf("kimi-code Seeds.AgentSnippets missing %s", kimiCodeSmokeSnippet)
	}
	if !strings.Contains(cfg, "require_trust = false") {
		t.Errorf("kimi-code seeded config must skip the workspace-trust prompt:\n%s", cfg)
	}
}

// TestGooseSeedsAProviderThroughASnippet pins how the smoke test reaches goose's
// REPL: config.yaml is the merged-config target, so the provider and the
// telemetry choice are seeded as a launcher config snippet, the greeting marks
// the normal-buffer REPL being up, and no egress is needed.
func TestGooseSeedsAProviderThroughASnippet(t *testing.T) {
	spec, ok := Specs["goose"]
	if !ok {
		t.Fatal("goose has no smoke-test spec")
	}
	if !spec.KeepsNormalBuffer || spec.ReadyMarker != gooseReadyMarker {
		t.Errorf("goose KeepsNormalBuffer = %v, ReadyMarker = %q; want true and %q",
			spec.KeepsNormalBuffer, spec.ReadyMarker, gooseReadyMarker)
	}
	if spec.Network != "" {
		t.Errorf("goose Network = %q, want the default `none`", spec.Network)
	}
	cfg, ok := spec.Seeds.AgentSnippets[gooseSmokeSnippet]
	if !ok {
		t.Fatalf("goose Seeds.AgentSnippets missing %s", gooseSmokeSnippet)
	}
	for _, want := range []string{"GOOSE_PROVIDER: ollama", "GOOSE_TELEMETRY_ENABLED: false"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("goose seeded config must contain %q:\n%s", want, cfg)
		}
	}
}

// TestReadyTimeoutPrefersThePerHarnessOverride pins the readiness-budget
// selection: a spec that names an override uses it, and one that leaves it
// unset falls back to the shared default.
func TestReadyTimeoutPrefersThePerHarnessOverride(t *testing.T) {
	if got := (Spec{ReadyTimeoutOverride: 90 * time.Second}).ReadyTimeout(); got != 90*time.Second {
		t.Errorf("ReadyTimeout() = %s, want the override 90s", got)
	}
	if got := (Spec{}).ReadyTimeout(); got != settledReadyTimeout {
		t.Errorf("ReadyTimeout() = %s, want the fallback %s", got, settledReadyTimeout)
	}
	// The smallest positive override must win over the fallback: a
	// truthiness test (`> 1`) would drop it.
	if got := (Spec{ReadyTimeoutOverride: time.Nanosecond}).ReadyTimeout(); got != time.Nanosecond {
		t.Errorf("ReadyTimeout() = %s, want the nanosecond override", got)
	}
}

// TestHeavyweightReadyTimeoutIsTheDocumentedCeiling pins the readiness ceiling
// hermes' slower startup is given: it is a wall-clock budget, so its value is
// the contract (a smaller one would fail a slow cold start).
func TestHeavyweightReadyTimeoutIsTheDocumentedCeiling(t *testing.T) {
	if heavyweightReadyTimeout != 120*time.Second {
		t.Errorf("heavyweightReadyTimeout = %s, want 120s", heavyweightReadyTimeout)
	}
}

// TestHermesSeedsAConfiguredHomeAndMarker pins how the smoke test reaches
// hermes' interactive REPL without a real credential: it seeds a home config
// that makes hermes consider a dummy, loopback provider configured and settles
// its shared-metrics offer, and it waits for the CLI's startup banner instead of
// a fixed settle. Without the seed hermes stops at its first-run setup guard and
// exits 1; the banner is a stronger readiness signal than the wall-clock settle.
func TestHermesSeedsAConfiguredHomeAndMarker(t *testing.T) {
	spec, ok := Specs["hermes"]
	if !ok {
		t.Fatal("hermes has no smoke-test spec")
	}
	if !spec.KeepsNormalBuffer {
		t.Error("hermes KeepsNormalBuffer = false; its classic CLI renders in the normal buffer")
	}
	if spec.ReadyMarker == "" {
		t.Error("hermes ReadyMarker is empty; the classic CLI emits no alternate-screen marker to wait for")
	}
	if spec.ReadyTimeoutOverride != heavyweightReadyTimeout {
		t.Errorf("hermes ReadyTimeoutOverride = %s, want %s", spec.ReadyTimeoutOverride, heavyweightReadyTimeout)
	}
	if spec.Network != "" {
		t.Errorf("hermes Network = %q, want the default `none` (the seeded provider is loopback)", spec.Network)
	}
	cfg, ok := spec.Seeds.HomeFiles[HermesConfigPath]
	if !ok {
		t.Fatalf("hermes Seeds.HomeFiles missing %s", HermesConfigPath)
	}
	for _, want := range []string{"model:", "base_url:", "telemetry:", "shared_metrics:", "offer_version:"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("hermes seeded config missing %q:\n%s", want, cfg)
		}
	}
}

// TestClaudeCodeReadiesOnAMarker pins that claude-code's normal-buffer renderer
// also readies on a startup marker, rather than its subtest waiting out the
// whole settle.
func TestClaudeCodeReadiesOnAMarker(t *testing.T) {
	spec, ok := Specs["claude-code"]
	if !ok {
		t.Fatal("claude-code has no smoke-test spec")
	}
	if !spec.KeepsNormalBuffer {
		t.Error("claude-code KeepsNormalBuffer = false; its renderer keeps the normal buffer")
	}
	if spec.ReadyMarker == "" {
		t.Error("claude-code ReadyMarker is empty; its subtest would wait out the settle")
	}
}

// TestClaudeCodeSeedsItsFirstRunState pins why and how claude-code is seeded:
// its first run stops at onboarding, whose only quit key aborts with exit 1, so
// its subtest seeds the first-run state claude-code keeps outside its
// ProvisionRules paths through the launcher's home: manifest.
func TestClaudeCodeSeedsItsFirstRunState(t *testing.T) {
	spec, ok := Specs["claude-code"]
	if !ok {
		t.Fatal("claude-code has no smoke-test spec")
	}
	cfg, ok := spec.Seeds.HomeManifest[ClaudeCodeConfigPath]
	if !ok {
		t.Fatalf("claude-code Seeds.HomeManifest missing %s", ClaudeCodeConfigPath)
	}
	for _, want := range []string{"hasCompletedOnboarding", "/workspace", "hasTrustDialogAccepted"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("claude-code seeded config missing %q:\n%s", want, cfg)
		}
	}
	if len(spec.Seeds.HomeFiles) != 0 {
		t.Errorf("claude-code Seeds.HomeFiles = %v; want the home: manifest", spec.Seeds.HomeFiles)
	}
}

// TestSawDaemonTimeout, TestStripANSI, and TestTUIStarted live in
// output_test.go, named after the output.go unit they cover.
