package agent

import (
	"context"
	"path/filepath"
	"strings"
)

// qwenCodeName is the canonical registry name of the Qwen Code CLI. Its binary
// is qwen.
const qwenCodeName = "qwen-code"

// qwenCodeDataDir is qwen's data root, home-relative: settings, logins,
// sessions, and user-authored commands, skills, and subagents live under it.
const qwenCodeDataDir = ".qwen"

// qwenCodeInstallDir holds the unpacked standalone release: its bundled Node,
// the CLI bundle, and the launcher, which finds the rest relative to itself.
const qwenCodeInstallDir = "/opt/agents-sandbox/qwen-code"

// qwenCodeResolveAsset sets qwen_asset to the release tarball for the build
// machine's architecture and fails the build on any other architecture, so a
// wrong-arch build is never installed.
const qwenCodeResolveAsset = `case "$(uname -m)" in ` +
	`x86_64|amd64) qwen_asset=qwen-code-linux-x64.tar.gz ;; ` +
	`aarch64|arm64) qwen_asset=qwen-code-linux-arm64.tar.gz ;; ` +
	`*) echo "qwen-code: unsupported architecture $(uname -m)" >&2; exit 1 ;; ` +
	`esac`

// qwenCodeVerifyAsset checks the downloaded $qwen_asset in $qwen_dir against
// its line in the release's SHA256SUMS. A list with no well-formed line for the
// asset fails the build before sha256sum runs, and so does a mismatch.
const qwenCodeVerifyAsset = `{ grep -E "^[0-9a-f]{64}  $qwen_asset\$" "$qwen_dir/SHA256SUMS" > "$qwen_dir/$qwen_asset.sha256" || ` +
	`{ echo "qwen-code: SHA256SUMS holds no valid entry for $qwen_asset" >&2; exit 1; }; } && ` +
	`{ (cd "$qwen_dir" && sha256sum --check --strict --quiet "$qwen_asset.sha256") || ` +
	`{ echo "qwen-code: checksum mismatch for $qwen_asset" >&2; exit 1; }; }`

// qwenCodeLauncherShim puts qwen on PATH. The release's bin/qwen resolves its
// install root from its own path, so it is exec'd from there, not symlinked.
const qwenCodeLauncherShim = `printf '%s\n' '#!/bin/sh' 'exec ` + qwenCodeInstallDir + `/bin/qwen "$@"' ` +
	`> /opt/agents-sandbox/bin/qwen && chmod 755 /opt/agents-sandbox/bin/qwen`

// qwenCodeSystemSettingsEnv relocates qwen's system settings file, the scope
// that outranks user and project settings. It is set in the image so the file
// can live under the install dir: only toolDir is copied from the agent stage
// into the runner image, so the default /etc/qwen-code/settings.json would be
// dropped.
const qwenCodeSystemSettingsEnv = "QWEN_CODE_SYSTEM_SETTINGS_PATH"

// qwenCodeSystemSettingsPath is the system settings file the image bakes,
// under the copied tool tree (see qwenCodeSystemSettingsEnv).
const qwenCodeSystemSettingsPath = qwenCodeInstallDir + "/system-settings.json"

// qwenCodeDisableUpdates turns off qwen's update check and auto-install in the
// system settings file: the sandbox pins the baked version and offers upgrades
// itself.
const qwenCodeDisableUpdates = `printf '%s\n' '{"general":{"enableAutoUpdate":false}}' > ` + qwenCodeSystemSettingsPath

type qwenCodeProfile struct{}

func (qwenCodeProfile) Name() string          { return qwenCodeName }
func (qwenCodeProfile) ConfigDirName() string { return qwenCodeName }
func (qwenCodeProfile) Experimental() bool    { return true }

// ImageSpec installs qwen's standalone release, which bundles its own Node, so
// the image needs none. The checksum is verified before the tarball is
// extracted, and the build runs the binary once so a broken download never
// ships.
func (qwenCodeProfile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg: versionArgFor(qwenCodeName),
		// Qwen has no env switch for its update check; qwenCodeDisableUpdates
		// writes the system settings file and this points qwen at it.
		AgentEnv: map[string]string{qwenCodeSystemSettingsEnv: qwenCodeSystemSettingsPath},
		InstallCommand: qwenCodeResolveAsset + ` && ` +
			`qwen_dir=/tmp/qwen-code && mkdir -p "$qwen_dir" && ` +
			`qwen_release="https://github.com/QwenLM/qwen-code/releases/download/v${QWEN_CODE_VERSION}" && ` +
			`curl -fsSL "$qwen_release/$qwen_asset" -o "$qwen_dir/$qwen_asset" && ` +
			`curl -fsSL "$qwen_release/SHA256SUMS" -o "$qwen_dir/SHA256SUMS" && ` +
			qwenCodeVerifyAsset + ` && ` +
			`mkdir -p ` + qwenCodeInstallDir + ` && ` +
			`tar -xzf "$qwen_dir/$qwen_asset" -C ` + qwenCodeInstallDir + ` --strip-components=1 && rm -rf "$qwen_dir" && ` +
			qwenCodeLauncherShim + ` && ` +
			qwenCodeDisableUpdates + ` && ` +
			`/opt/agents-sandbox/bin/qwen --version`,
		RequiresNode: false,
		// qwen keeps its sessions, managed tools, and caches under ~/.qwen on the
		// home volume, so the tool tree stays root-owned.
		OwnsToolTree: false,
		// The bundled Node and the prebuilt native modules link the C++ runtime.
		RuntimeLibs:   []string{"libstdc++.so.6", "libgcc_s.so.1"},
		RunnerCommand: "",
	}
}

func (qwenCodeProfile) LatestVersion(ctx context.Context) (string, error) {
	return latestQwenCodeVersion(ctx)
}
func (qwenCodeProfile) NewerThan(a, b string) (bool, error) { return newerVersionThan(a, b) }

func (qwenCodeProfile) SnippetPattern() string { return settingsSnippetPattern }

func (qwenCodeProfile) VMConfigPath(home string) string {
	return filepath.Join(home, qwenCodeDataDir, settingsFileName)
}
func (qwenCodeProfile) ConfigFileNames() []string { return []string{settingsFileName} }

// ProvisionRules copies the data root's user-authored files that carry no
// credential: the global instructions and the command, skill, subagent, and
// saved-workflow trees. settings.json holds the keys /auth saves in its env,
// .env holds environment secrets, oauth_creds.json and mcp-oauth-tokens.json
// hold logins, and every extension keeps its settings in a .env; keys reach the VM only as msb secrets, and the rest of the data
// root is runtime state.
func (qwenCodeProfile) ProvisionRules() []ProvisionRule {
	return []ProvisionRule{
		{Dir: qwenCodeDataDir, Patterns: []string{
			"/QWEN.md", agentsRulesPattern, "/commands/", "/skills/", "/agents/", "/workflows/",
		}},
	}
}

// ReadBackPaths declares qwen's two login stores. Its settings file is left out
// for now: /auth saves provider keys into its top-level env table, and MCP and
// hook entries keep tokens in their own env tables, shapes the read-back does
// not yet classify. Declaring it would let a learning session's agent-config
// write-back copy a raw value into the host's per-slug config; it is added once
// that classification lands (#760).
func (qwenCodeProfile) ReadBackPaths() []string {
	return []string{
		qwenCodeDataDir + "/oauth_creds.json",
		qwenCodeDataDir + "/mcp-oauth-tokens.json",
	}
}

func (qwenCodeProfile) CredentialProbes() []string { return nil }

// AttachCommand forwards every argument shell-quoted, because the command runs
// through `bash -c`.
func (qwenCodeProfile) AttachCommand(_ string, args []string) string {
	parts := append([]string{"qwen"}, shellQuoteArgs(args)...)
	return strings.Join(parts, " ")
}

func (qwenCodeProfile) VersionCmd() string { return "qwen --version" }
func (qwenCodeProfile) ParseVersion(stdout string) (string, error) {
	return extractSemverFromOutput(stdout)
}
