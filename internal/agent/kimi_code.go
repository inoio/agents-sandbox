package agent

import (
	"context"
	"path/filepath"
	"strings"
)

// kimiCodeName is the canonical registry name of the Kimi Code CLI. Its binary
// is kimi.
const kimiCodeName = "kimi-code"

// kimiCodeDataDir is kimi's data root, home-relative: config, sessions, logins,
// and update state all live under it.
const kimiCodeDataDir = ".kimi-code"

// kimiCodeConfigFileName is kimi's runtime config file under its data root.
const kimiCodeConfigFileName = "config.toml"

// kimiCodeNoAutoUpdate turns off kimi's whole update preflight (check,
// background install, and prompt): the sandbox pins the baked version and
// offers upgrades itself.
const kimiCodeNoAutoUpdate = "KIMI_CODE_NO_AUTO_UPDATE"

// kimiCodeResolveAsset sets kimi_asset to the release tarball for the build
// machine's architecture and fails the build on any other architecture, so a
// wrong-arch binary is never installed.
const kimiCodeResolveAsset = `case "$(uname -m)" in ` +
	`x86_64|amd64) kimi_asset=kimi-code-linux-x64.tar.gz ;; ` +
	`aarch64|arm64) kimi_asset=kimi-code-linux-arm64.tar.gz ;; ` +
	`*) echo "kimi-code: unsupported architecture $(uname -m)" >&2; exit 1 ;; ` +
	`esac`

// kimiCodeVerifyAsset checks the downloaded $kimi_asset in $kimi_dir against
// its sibling .sha256 file. A checksum file that names another asset or holds
// a malformed hash fails the build before sha256sum runs, and so does a
// mismatch.
const kimiCodeVerifyAsset = `{ grep -qE "^[0-9a-f]{64}  $kimi_asset\$" "$kimi_dir/$kimi_asset.sha256" || ` +
	`{ echo "kimi-code: $kimi_asset.sha256 holds no valid entry for $kimi_asset" >&2; exit 1; }; } && ` +
	`{ (cd "$kimi_dir" && sha256sum --check --strict --quiet "$kimi_asset.sha256") || ` +
	`{ echo "kimi-code: checksum mismatch for $kimi_asset" >&2; exit 1; }; }`

type kimiCodeProfile struct{}

func (kimiCodeProfile) Name() string          { return kimiCodeName }
func (kimiCodeProfile) ConfigDirName() string { return kimiCodeName }
func (kimiCodeProfile) Experimental() bool    { return true }

// ImageSpec installs the kimi release binary, a Node single-executable, so the
// image needs no Node. The release tag is the npm package's scoped name, URL-
// encoded in the download path. The checksum is verified before the tarball is
// extracted, and the build runs the binary once so a broken download never
// ships.
func (kimiCodeProfile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg: versionArgFor(kimiCodeName),
		AgentEnv:   map[string]string{kimiCodeNoAutoUpdate: "1"},
		InstallCommand: kimiCodeResolveAsset + ` && ` +
			`kimi_dir=/tmp/kimi-code && mkdir -p "$kimi_dir" && ` +
			`kimi_release="https://github.com/MoonshotAI/kimi-code/releases/download/%40moonshot-ai%2Fkimi-code%40${KIMI_CODE_VERSION}" && ` +
			`curl -fsSL "$kimi_release/$kimi_asset" -o "$kimi_dir/$kimi_asset" && ` +
			`curl -fsSL "$kimi_release/$kimi_asset.sha256" -o "$kimi_dir/$kimi_asset.sha256" && ` +
			kimiCodeVerifyAsset + ` && ` +
			`tar -xzf "$kimi_dir/$kimi_asset" -C "$kimi_dir" kimi && ` +
			`install -m 0755 "$kimi_dir/kimi" /opt/agents-sandbox/bin/kimi && rm -rf "$kimi_dir" && ` +
			kimiCodeNoAutoUpdate + `=1 /opt/agents-sandbox/bin/kimi --version`,
		RequiresNode: false,
		// kimi downloads its managed rg/fd and keeps its update state under
		// ~/.kimi-code on the home volume, so the tool tree stays root-owned.
		OwnsToolTree: false,
		// The embedded Node runtime links the C++ runtime beside glibc.
		RuntimeLibs:   []string{"libstdc++.so.6", "libgcc_s.so.1"},
		RunnerCommand: "",
	}
}

func (kimiCodeProfile) LatestVersion(ctx context.Context) (string, error) {
	return latestKimiCodeVersion(ctx)
}
func (kimiCodeProfile) NewerThan(a, b string) (bool, error) { return newerVersionThan(a, b) }

// SnippetPattern matches TOML snippets, which merge into config.toml as TOML.
func (kimiCodeProfile) SnippetPattern() string { return configTOMLSnippetPattern }

func (kimiCodeProfile) VMConfigPath(home string) string {
	return filepath.Join(home, kimiCodeDataDir, kimiCodeConfigFileName)
}
func (kimiCodeProfile) ConfigFileNames() []string { return []string{kimiCodeConfigFileName} }

// ProvisionRules copies only the files that carry no credential: the terminal
// UI preferences and the global agent instructions. config.toml keeps literal
// provider keys beside its settings, mcp.json can hold MCP tokens, and
// credentials/ holds rotating OAuth logins; keys reach the VM only as msb
// secrets, and the rest of the data root is runtime state.
func (kimiCodeProfile) ProvisionRules() []ProvisionRule {
	return []ProvisionRule{
		{Dir: kimiCodeDataDir, Patterns: []string{"tui.toml", "AGENTS.md"}},
	}
}

// ReadBackPaths declares kimi's config, its MCP declarations, and its login
// store: the first two can carry static keys, the credential files are logins.
func (kimiCodeProfile) ReadBackPaths() []string {
	return []string{
		kimiCodeDataDir + "/" + kimiCodeConfigFileName,
		kimiCodeDataDir + "/mcp.json",
		kimiCodeDataDir + "/credentials/*.json",
	}
}

func (kimiCodeProfile) CredentialProbes() []string { return nil }

// EnvIndirectionKey is kimi's native env indirection: it reads no key from the
// process environment and only accepts an environment-variable name in
// "api_key_env", so "api_key = \"${VAR}\"" would send the literal. The
// write-back therefore rewrites kimi's "api_key" assignment to
// "api_key_env = \"<ENVVAR>\"" (#749).
func (kimiCodeProfile) EnvIndirectionKey() string { return "api_key_env" }

// AttachCommand forwards every argument shell-quoted, because the command runs
// through `bash -c`.
func (kimiCodeProfile) AttachCommand(_ string, args []string) string {
	parts := append([]string{"kimi"}, shellQuoteArgs(args)...)
	return strings.Join(parts, " ")
}

func (kimiCodeProfile) VersionCmd() string { return "kimi --version" }
func (kimiCodeProfile) ParseVersion(stdout string) (string, error) {
	return extractSemverFromOutput(stdout)
}
