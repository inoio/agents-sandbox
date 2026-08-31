package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/inoio/opencode-sandbox/internal/opencode"
)

//nolint:gochecknoinits // built-in agent self-registration
func init() { Register(opencodeProfile{}) }

type opencodeProfile struct{}

func (opencodeProfile) Name() string          { return opencodeName }
func (opencodeProfile) ConfigDirName() string { return "opencode" }

func (opencodeProfile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg:       "OPENCODE_VERSION",
		VersionLabel:     "org.opencode-sandbox.opencode-version",
		DisableUpdateEnv: "OPENCODE_DISABLE_AUTOUPDATE",
		InstallCommand:   `curl -fsSL https://opencode.ai/install | bash -s -- --version "$OPENCODE_VERSION" && cp /root/.opencode/bin/opencode /usr/local/bin`,
	}
}

func (opencodeProfile) DaemonStartCmd(serveOnly bool) string {
	hostname := "127.0.0.1"
	if serveOnly {
		hostname = "0.0.0.0"
	}
	return fmt.Sprintf(
		"nohup opencode serve --hostname %s --port %s > /tmp/opencode-serve.log 2>&1 &",
		hostname,
		"4096",
	)
}

func (opencodeProfile) DaemonKillCmd() string { return "pkill -f 'opencode serve' || true" }
func (opencodeProfile) DaemonHealthCmd() string {
	return "curl -sfm2 http://127.0.0.1:4096/global/health"
}

func (opencodeProfile) DaemonHealthParse(stdout string) (bool, error) {
	var resp struct {
		Healthy bool   `json:"healthy"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		return false, fmt.Errorf("parse health response: %w", err)
	}
	return resp.Healthy, nil
}

func (opencodeProfile) WorktreeListCmd() string {
	return "curl -sf http://127.0.0.1:4096/experimental/worktree"
}
func (opencodeProfile) WorktreeCreateCmd(spec WorktreeSpec) string {
	body := fmt.Sprintf(`{"name":%q}`, spec.Name)
	if spec.Base != "" {
		body = fmt.Sprintf(`{"name":%q,"startCommand":"git reset --hard %s"}`, spec.Name, spec.Base)
	}
	return fmt.Sprintf(
		"curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '%s'",
		body,
	)
}
func (opencodeProfile) WorktreeParseDir(stdout string) (string, bool) {
	var resp struct {
		Directory string `json:"directory"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		return "", false
	}
	return resp.Directory, resp.Directory != ""
}

func (opencodeProfile) LatestVersion(ctx context.Context) (string, error) {
	return opencode.LatestVersion(ctx)
}
func (opencodeProfile) NewerThan(a, b string) (bool, error) { return opencode.NewerThan(a, b) }

func (opencodeProfile) SnippetPattern() string { return "opencode-*.json*" }
func (opencodeProfile) VMConfigPath(home string) string {
	return filepath.Join(home, ".config", "opencode", "opencode.json")
}

func (opencodeProfile) ProvisionRules() []ProvisionRule {
	return []ProvisionRule{
		{Dir: ".config/opencode", Patterns: []string{"**", "!node_modules/", "!package*.json", "!.gitignore"}},
		{Dir: ".local/share/opencode", Patterns: []string{"auth.json"}},
	}
}

func (opencodeProfile) AttachCommand(target string, args []string) string {
	parts := []string{"opencode", "attach", "http://127.0.0.1:4096", "--dir", target}
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}
