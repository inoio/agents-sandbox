package agent

import (
	"context"
	"path/filepath"
	"strings"
)

// ohMyPiName is the canonical registry name of the Oh My Pi agent. Its binary
// is omp.
const ohMyPiName = "oh-my-pi"

// ohMyPiLauncherOverlay is the launcher-owned config overlay every attach loads
// with --config. omp treats an overlay as strict (a missing file is a hard
// error), so the install writes it and its build-time smoke loads it.
const ohMyPiLauncherOverlay = "/opt/agents-sandbox/oh-my-pi/launcher.yml"

// ohMyPiConfigDir is omp's user config base, home-relative.
const ohMyPiConfigDir = ".omp/agent"

// ohMyPiConfigFileName is omp's global settings file; it falls back to
// config.yaml, so both names are reserved for the merged config.
const (
	ohMyPiConfigFileName    = "config.yml"
	ohMyPiConfigFileNameAlt = "config.yaml"
)

// ohMyPiResolveAsset sets omp_asset to the glibc release asset for the build
// machine's architecture and fails the build on any other architecture, so a
// wrong-arch binary is never installed.
const ohMyPiResolveAsset = `case "$(uname -m)" in ` +
	`x86_64|amd64) omp_asset=omp-linux-x64 ;; ` +
	`aarch64|arm64) omp_asset=omp-linux-arm64 ;; ` +
	`*) echo "oh-my-pi: unsupported architecture $(uname -m)" >&2; exit 1 ;; ` +
	`esac`

// ohMyPiVerifyAsset checks the downloaded $omp_asset in $omp_dir against its
// SHA256SUMS.txt entry. A missing or malformed entry fails the build before
// sha256sum runs, and so does a mismatch.
const ohMyPiVerifyAsset = `{ grep -E "^[0-9a-f]{64}  $omp_asset\$" "$omp_dir/SHA256SUMS.txt" > "$omp_dir/$omp_asset.sha256" || ` +
	`{ echo "oh-my-pi: SHA256SUMS.txt has no valid entry for $omp_asset" >&2; exit 1; }; } && ` +
	`{ (cd "$omp_dir" && sha256sum --check --strict --quiet "$omp_asset.sha256") || ` +
	`{ echo "oh-my-pi: checksum mismatch for $omp_asset" >&2; exit 1; }; }`

type ohMyPiProfile struct{}

func (ohMyPiProfile) Name() string          { return ohMyPiName }
func (ohMyPiProfile) ConfigDirName() string { return ohMyPiName }
func (ohMyPiProfile) Experimental() bool    { return true }

// ImageSpec installs the standalone omp release binary, which embeds Bun and
// the native addon, so the image needs neither. The checksum is verified
// before the binary is installed, and the build runs it once with the launcher
// overlay so neither a broken download nor a missing overlay ships.
func (ohMyPiProfile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg: versionArgFor(ohMyPiName),
		AgentEnv:   nil,
		InstallCommand: ohMyPiResolveAsset + ` && ` +
			`omp_dir=/tmp/oh-my-pi && mkdir -p "$omp_dir" && ` +
			`omp_release="https://github.com/can1357/oh-my-pi/releases/download/v${OH_MY_PI_VERSION}" && ` +
			`curl -fsSL "$omp_release/$omp_asset" -o "$omp_dir/$omp_asset" && ` +
			`curl -fsSL "$omp_release/SHA256SUMS.txt" -o "$omp_dir/SHA256SUMS.txt" && ` +
			ohMyPiVerifyAsset + ` && ` +
			`install -m 0755 "$omp_dir/$omp_asset" /opt/agents-sandbox/bin/omp && rm -rf "$omp_dir" && ` +
			`mkdir -p ` + filepath.Dir(ohMyPiLauncherOverlay) + ` && ` +
			`printf 'startup:\n  checkUpdate: false\n' > ` + ohMyPiLauncherOverlay + ` && ` +
			`/opt/agents-sandbox/bin/omp --config ` + ohMyPiLauncherOverlay + ` --version`,
		RequiresNode: false,
		// The extracted native addon lands in ~/.omp/natives on the home
		// volume, so nothing writes into the install tree.
		OwnedToolPaths: nil,
		// The glibc binary and its embedded addon link only glibc itself
		// (libc, libm, libpthread, libdl, libutil), which every glibc base has.
		RuntimeLibs: nil,
	}
}

func (ohMyPiProfile) LatestVersion(ctx context.Context) (string, error) {
	return latestOhMyPiVersion(ctx)
}
func (ohMyPiProfile) NewerThan(a, b string) (bool, error) { return newerVersionThan(a, b) }

// SnippetPattern matches YAML snippets under either extension; they merge into
// config.yml as YAML.
func (ohMyPiProfile) SnippetPattern() string { return "config*.{yml,yaml}" }
func (ohMyPiProfile) VMConfigPath(home string) string {
	return filepath.Join(home, ohMyPiConfigDir, ohMyPiConfigFileName)
}
func (ohMyPiProfile) ConfigFileNames() []string {
	return []string{ohMyPiConfigFileName, ohMyPiConfigFileNameAlt}
}

// ProvisionRules copies only the settings file. The rest of ~/.omp is runtime
// state (sessions, agent.db, extracted natives), and models.yml and .env can
// hold literal provider keys, which reach the VM only as msb secrets.
func (ohMyPiProfile) ProvisionRules() []ProvisionRule {
	return []ProvisionRule{
		{Dir: ohMyPiConfigDir, Patterns: []string{ohMyPiConfigFileName, ohMyPiConfigFileNameAlt}},
	}
}

// AttachCommand loads the launcher overlay, which turns off omp's startup
// update check: the sandbox pins the baked version and offers upgrades itself.
// Every forwarded argument is shell-quoted, because the command runs through
// `bash -c`.
func (ohMyPiProfile) AttachCommand(_ string, args []string) string {
	parts := []string{"omp", "--config", ohMyPiLauncherOverlay}
	parts = append(parts, shellQuoteArgs(args)...)
	return strings.Join(parts, " ")
}

func (ohMyPiProfile) VersionCmd() string { return "omp --version" }
func (ohMyPiProfile) ParseVersion(stdout string) (string, error) {
	return extractSemverFromOutput(stdout)
}
