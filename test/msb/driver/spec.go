// Package driver holds the shared, untagged driving support for the msb-tagged
// harness suites: the harness table, the output helpers, the pty session, the
// child-environment and home-seeding helpers, and the launcher build/prebuild/
// cleanup steps. It is untagged so its unit tests run in the default `make test`;
// only a KVM host runs the suites that import it.
package driver

import "time"

// Terminal keys that leave a harness's full-screen TUI: Ctrl-D is `opencode`'s,
// `opencode2`'s, `pi`'s, and `oh-my-pi`'s documented `app_exit`/`app.exit`, and Ctrl-C is the
// interrupt `claude-code` announces as its exit ("Press Ctrl-C again to exit")
// and `codex`, `qwen-code`, `grok-build`, and `mistral-vibe` quit on.
const (
	CtrlC = 0x03
	CtrlD = 0x04
)

// PublicNetwork is the `--network` profile a harness that needs egress to
// render runs under.
const PublicNetwork = "public"

// NetworkNone is the secure default network policy a smoke run boots under when
// its spec names no network.
const NetworkNone = "none"

// opencodeAgentName is the agent the docker-storage smoke and the runner-image
// prebuild both build.
const opencodeAgentName = "opencode"

// Spec describes how to drive one harness to its TUI and out of it.
//
//exhaustruct:ignore
type Spec struct {
	// QuitKey leaves the harness's full-screen TUI. The smoke test taps it
	// until the process goes, because a harness can paint its TUI seconds
	// before it accepts input and can need the key more than once.
	QuitKey []byte
	// Network overrides the default `--network none` when a harness needs
	// egress to render.
	Network string
	// KeepsNormalBuffer declares a harness whose TUI renders into the
	// terminal's normal buffer instead of the alternate screen, so the
	// alternate-screen marker cannot announce that its UI is up.
	KeepsNormalBuffer bool
	// ReadyMarker, when set, is an output substring that announces a
	// normal-buffer harness's UI is up — the readiness signal WaitSettled
	// replaces for a harness whose CLI prints a stable startup line (hermes'
	// banner). It takes precedence over KeepsNormalBuffer's settle.
	ReadyMarker string
	// ReadyTimeoutOverride overrides the readiness wait for a KeepsNormalBuffer
	// harness; zero uses the shared default. It bounds either the marker wait
	// or the settle. It is ignored for an alternate-screen harness, whose
	// readiness is the alternate-screen marker rather than a wall clock.
	ReadyTimeoutOverride time.Duration
	// SeedHomeFiles maps a home-relative path to its content, written under the
	// isolated HOME (<root>/home) before the run so the launcher provisions it
	// into the VM. It is how a harness that needs host-side configuration (a
	// provider block, a settled opt-in) starts headful without credentials.
	SeedHomeFiles map[string]string
	// SeedHomeManifest maps a VM-home-relative target path to its content,
	// provisioned through the launcher config's `home:` key rather than the
	// agent's ProvisionRules. Use it for a harness whose first-run state lives
	// outside the paths its ProvisionRules copy (claude-code's ~/.claude.json,
	// beside the ~/.claude/settings.json the rules carry); a file the rules
	// already copy belongs in SeedHomeFiles.
	SeedHomeManifest map[string]string
	// SeedAgentSnippets maps a snippet file name to its content, written into
	// the agent's launcher config dir (XDG_CONFIG_HOME/agents-sandbox/<config
	// dir>) so the launcher merges it into the agent's config. Use it for a
	// harness whose settings live in its merged-config file, which neither the
	// ProvisionRules nor the home: manifest may write.
	SeedAgentSnippets map[string]string
}

// Specs maps every registered harness to how the smoke test drives it.
// It must stay in sync with agent.Names(); the smoke test asserts the two key
// sets are equal.
var Specs = map[string]Spec{
	// claude-code checks in with its API before it draws anything, so it needs
	// egress, and its renderer keeps the terminal's normal buffer, so the
	// alternate screen never announces that its UI is up. Its startup splash
	// prints the product name, which readies the subtest. Its first run stops at
	// onboarding (a theme picker and a workspace-trust dialog) whose only quit
	// key aborts with exit 1, so the subtest seeds its first-run state through
	// the launcher's home: manifest (SeedHomeManifest) and lands on the prompt.
	"claude-code": {
		QuitKey:           []byte{CtrlC},
		Network:           PublicNetwork,
		KeepsNormalBuffer: true,
		ReadyMarker:       claudeCodeReadyMarker,
		SeedHomeManifest:  map[string]string{ClaudeCodeConfigPath: ClaudeCodeSmokeConfig},
	},
	// codex quits on Ctrl-C and may reach its API at startup, so it gets
	// egress like claude-code; its TUI uses the alternate screen.
	"codex": {QuitKey: []byte{CtrlC}, Network: PublicNetwork, KeepsNormalBuffer: false},
	// hermes' classic CLI keeps the terminal's normal buffer, so the alternate
	// screen never announces that its UI is up. The subtest seeds a home config
	// that makes a dummy, loopback provider count as configured, so hermes starts
	// the classic CLI REPL instead of stopping at its first-run setup guard (which
	// always exits 1 without a real provider); Ctrl-D then leaves the REPL with
	// exit 0. The seeded endpoint is loopback, so it runs under the default
	// `--network none`; the startup banner is the readiness marker. Its runner
	// image carries a whole Python/Node tool store, so its startup is the slowest
	// of the harnesses.
	"hermes": {
		QuitKey:              []byte{CtrlD},
		KeepsNormalBuffer:    true,
		ReadyMarker:          hermesReadyMarker,
		ReadyTimeoutOverride: heavyweightReadyTimeout,
		SeedHomeFiles:        map[string]string{HermesConfigPath: hermesSmokeConfig},
	},
	// oh-my-pi reaches its TUI without credentials, like pi, which it forks.
	// Its renderer keeps the terminal's normal buffer (it borrows the alternate
	// screen only while resizing or for fullscreen overlays), so readiness is a
	// marker rather than the alternate screen: with no model configured it prints
	// its "No default model selected" warning above the editor's prompt bar. The
	// subtest seeds a home config that turns off the first-run setup wizard, a
	// fullscreen overlay that would otherwise come up before the editor. It needs
	// no egress, so it runs under the default `--network none`.
	"oh-my-pi": {
		QuitKey:           []byte{CtrlD},
		KeepsNormalBuffer: true,
		ReadyMarker:       ohMyPiReadyMarker,
		SeedHomeFiles:     map[string]string{ohMyPiConfigPath: ohMyPiSmokeConfig},
	},
	// kimi-code's regular TUI mode renders into the terminal's normal buffer, so
	// its welcome banner is the readiness marker. With no provider it starts
	// logged out at the prompt and needs no egress. The first open of /workspace
	// would show a workspace-trust dialog, so the subtest seeds a launcher config
	// snippet that turns the trust requirement off; it merges into config.toml,
	// which is the merged-config target and so cannot come through the home:
	// manifest. Ctrl-D exits on a second press, which the repeated tap covers.
	"kimi-code": {
		QuitKey:           []byte{CtrlD},
		KeepsNormalBuffer: true,
		ReadyMarker:       kimiCodeReadyMarker,
		SeedAgentSnippets: map[string]string{kimiCodeSmokeSnippet: kimiCodeSmokeConfig},
	},
	// qwen-code draws its TUI on the alternate screen and, with no provider
	// configured, starts at its auth dialog, so it needs no seed and no egress.
	// It ignores the first Ctrl-C and exits on the second, which the repeated tap
	// covers.
	"qwen-code": {QuitKey: []byte{CtrlC}, Network: "", KeepsNormalBuffer: false},
	// goose's session REPL keeps the terminal's normal buffer, so the goose in its
	// greeting is the readiness marker. Without a provider it would start the
	// interactive configure flow or exit 1, so the subtest seeds a launcher config
	// snippet naming a local Ollama provider (no key, no request until a prompt)
	// and declining telemetry, which would otherwise ask first. Ctrl-D leaves the
	// REPL.
	"goose": {
		QuitKey:           []byte{CtrlD},
		KeepsNormalBuffer: true,
		ReadyMarker:       gooseReadyMarker,
		SeedAgentSnippets: map[string]string{gooseSmokeSnippet: gooseSmokeConfig},
	},
	// grok-build's fullscreen TUI (its default screen mode) uses the alternate
	// screen. With no credential it comes up on its device-code sign-in screen
	// ("Waiting for approval…"), which still satisfies the TUI assertion, so it
	// needs no seed and no egress; Ctrl-C leaves it with exit 0.
	"grok-build": {QuitKey: []byte{CtrlC}, Network: "", KeepsNormalBuffer: false},
	// mistral-vibe's Rust TUI draws on the alternate screen. Ctrl-C leaves it,
	// including from the folder-trust dialog it may show before the session.
	"mistral-vibe": {QuitKey: []byte{CtrlC}, Network: "", KeepsNormalBuffer: false},
	// opencode's editor owns Ctrl-C, so its TUI leaves on Ctrl-D.
	opencodeAgentName: {QuitKey: []byte{CtrlD}, Network: "", KeepsNormalBuffer: false},
	"opencode2":       {QuitKey: []byte{CtrlD}, Network: "", KeepsNormalBuffer: false},
	"pi":              {QuitKey: []byte{CtrlD}, Network: "", KeepsNormalBuffer: false},
}

// settledReadyTimeout bounds the readiness wait for a harness whose TUI keeps
// the terminal's normal buffer, where no marker announces that its UI is up. It
// covers the launcher's image load and VM start — about 14s on a cold host when
// the image prebuild has already run — with margin. A harness whose runner image
// loads far slower overrides it (see Spec.ReadyTimeoutOverride).
const settledReadyTimeout = 25 * time.Second

// heavyweightReadyTimeout bounds the readiness wait for a KeepsNormalBuffer
// harness whose startup is much slower than the lightweight agents the shared
// budget was measured on: hermes carries a whole Python/Node tool store. It is a
// ceiling, not a sleep — hermes' banner marker ends the wait as soon as the CLI
// is up — so a slow cold start fails the subtest only when it genuinely hangs.
const heavyweightReadyTimeout = 120 * time.Second

// hermesReadyMarker is the startup line hermes' classic CLI prints before it
// accepts input (show_banner: "Hermes Agent v<version> (<date>)"). It is stable
// across releases, unlike the version itself.
const hermesReadyMarker = "Hermes Agent v"

// claudeCodeReadyMarker is the product name claude-code's startup splash prints
// before its prompt is live. Its renderer keeps the normal buffer and emits no
// alternate-screen sequence, so without a marker its subtest would wait out the
// whole settle.
const claudeCodeReadyMarker = "Claude Code"

// ClaudeCodeConfigPath is the file claude-code keeps its first-run state in,
// home-relative. It sits beside .claude/settings.json, which the launcher's
// claude-code ProvisionRules copy, so the smoke test seeds it through the
// launcher config's home: manifest (SeedHomeManifest) instead.
const ClaudeCodeConfigPath = ".claude.json"

// ClaudeCodeSmokeConfig skips claude-code's first-run flow so the subtest lands
// on the prompt, where its repeated Ctrl-C quits 0. hasCompletedOnboarding drops
// the theme picker; the /workspace project entry accepts the workspace-trust
// dialog for the guest working directory the subtest runs in (the throwaway
// project dir is not a repository, so it binds at /workspace). Without it the
// onboarding's only quit key aborts the launch with exit 1, which the subtest's
// exit-0 assertion rejects. Both keys are the guest's, not the host's.
const ClaudeCodeSmokeConfig = `{"hasCompletedOnboarding": true, "projects": {"/workspace": {"hasTrustDialogAccepted": true}}}`

// kimiCodeReadyMarker is the title of kimi-code's welcome banner, printed when
// its prompt comes up.
const kimiCodeReadyMarker = "Welcome to Kimi Code!"

// kimiCodeSmokeSnippet is the launcher config snippet the subtest seeds; it
// matches kimi-code's config*.toml snippet pattern.
const kimiCodeSmokeSnippet = "config-smoke.toml"

// kimiCodeSmokeConfig treats every workspace as trusted, so kimi-code skips the
// trust dialog it shows the first time a workspace is opened.
const kimiCodeSmokeConfig = `[workspace]
require_trust = false
`

// gooseReadyMarker is the goose drawn at the start of goose's session greeting,
// printed once the session is built and the REPL is about to read input.
const gooseReadyMarker = "__( O)>"

// gooseSmokeSnippet is the launcher config snippet the subtest seeds; it matches
// goose's config*.{yml,yaml} snippet pattern.
const gooseSmokeSnippet = "config-smoke.yaml"

// gooseSmokeConfig configures a local Ollama provider, which needs no key and
// sends nothing until a prompt, and records the telemetry choice so the
// first-run consent prompt does not come up.
const gooseSmokeConfig = `GOOSE_PROVIDER: ollama
GOOSE_MODEL: qwen3
GOOSE_TELEMETRY_ENABLED: false
`

// ohMyPiReadyMarker is the warning oh-my-pi prints when it starts its editor
// with no model configured, which is exactly the smoke run: the seeded config
// turns off the setup wizard and sets no model, and the child's restricted
// environment carries no API-key variables. It renders just above the editor's
// prompt bar, so it marks the editor being up; like claude-code, oh-my-pi keeps
// the normal buffer and emits no alternate-screen sequence.
const ohMyPiReadyMarker = "No default model selected"

// HermesConfigPath is hermes' config file, home-relative, provisioned into the
// guest from the host's ~/.hermes/config.yaml.
const HermesConfigPath = ".hermes/config.yaml"

// hermesSmokeConfig makes hermes start its interactive REPL without a real
// credential: the model block's custom, loopback base_url (and dummy api_key)
// satisfies hermes' "any provider configured" check, so the classic CLI starts
// instead of the first-run setup guard, which exits 1 with no provider. The
// endpoint is a closed loopback port, so no request leaves the VM; the deeper
// model keys only matter once a message is submitted. The telemetry block
// settles the shared-metrics opt-in so hermes does not show that prompt before
// the REPL (offer_version must match hermes' current OFFER_VERSION).
const hermesSmokeConfig = `model:
  provider: custom
  default: local-model
  base_url: http://127.0.0.1:9/v1
  api_key: dummy
telemetry:
  shared_metrics:
    enabled: false
    send: false
    offer_version: 2
`

// ohMyPiConfigPath is omp's global config file, home-relative, provisioned
// into the guest from the host's ~/.omp/agent/config.yml.
const ohMyPiConfigPath = ".omp/agent/config.yml"

// ohMyPiSmokeConfig turns off omp's first-run setup wizard (provider sign-in,
// model, and theme scenes), so the smoke test lands in the editor.
const ohMyPiSmokeConfig = `startup:
  setupWizard: false
`

// ReadyTimeout returns the readiness wait for this harness: its per-harness
// override when set, otherwise settledReadyTimeout.
func (s Spec) ReadyTimeout() time.Duration {
	if s.ReadyTimeoutOverride > 0 {
		return s.ReadyTimeoutOverride
	}
	return settledReadyTimeout
}

// NetworkOrDefault is the `--network` value a run under this spec uses: the
// harness's own profile when it names one, else the secure default.
func (s Spec) NetworkOrDefault() string {
	if s.Network != "" {
		return s.Network
	}
	return NetworkNone
}

// TB is the subset of testing.TB the driver helpers need. It is declared here so
// the driver's non-test files compile without importing testing; a suite passes
// the *testing.T (or *testing.B) it is driving.
type TB interface {
	Helper()
	Logf(string, ...any)
	Fatalf(string, ...any)
	Skipf(string, ...any)
	Cleanup(func())
}
