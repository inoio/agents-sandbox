package agent

import (
	"context"
	"fmt"
	"strings"
)

//nolint:gochecknoinits // built-in agent self-registration
func init() { Register(opencode2Profile{opencodeConfig: opencodeConfig{}}) }

// opencode2Name is the canonical registry name of the opencode 2 agent.
const opencode2Name = "opencode2"

// opencode2Port is the forwarded guest port the opencode2 serve daemon binds,
// matching the sandbox's fixed host/guest port mapping.
const opencode2Port = "4096"

// opencode2Profile is the opencode 2 (beta) coding agent. It embeds
// opencodeConfig for the config handling it shares with v1 (v2 reads the same
// ~/.config/opencode files and auth.json) and overrides the daemon, worktree,
// attach, and version behavior for the v2 server API.
type opencode2Profile struct {
	opencodeConfig
}

func (opencode2Profile) Name() string { return opencode2Name }

func (opencode2Profile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg:   versionArgFor(opencode2Name),
		VersionLabel: versionLabelFor(opencode2Name),
		// The sandbox pins the baked version and resolves upgrades itself; the
		// env var disables opencode's own auto-update checks at startup.
		AgentEnv:       map[string]string{"OPENCODE_DISABLE_AUTOUPDATE": "true"},
		InstallCommand: "npm install -g @opencode-ai/cli@$OPENCODE2_VERSION",
	}
}

func (opencode2Profile) DaemonStartCmd(serveOnly bool) string {
	hostname := "127.0.0.1"
	if serveOnly {
		hostname = "0.0.0.0"
	}
	return fmt.Sprintf(
		"nohup opencode2 serve --hostname %s --port %s > /tmp/opencode2-serve.log 2>&1 &",
		hostname,
		opencode2Port,
	)
}

func (opencode2Profile) DaemonKillCmd() string { return "pkill -f 'opencode2 serve' || true" }

func (opencode2Profile) DaemonHealthCmd() string {
	return "curl -sfm2 http://127.0.0.1:" + opencode2Port + "/api/health"
}

func (opencode2Profile) DaemonHealthParse(stdout string) (bool, error) {
	return parseDaemonHealth(stdout)
}

// opencode2ProjectIDCmd resolves the current project's id from the running v2
// server; the worktree routes are scoped by project id.
const opencode2ProjectIDCmd = "$(curl -sf http://127.0.0.1:4096/api/project/current | jq -r .id)"

func (opencode2Profile) WorktreeListCmd() string {
	return "PID=" + opencode2ProjectIDCmd + "; curl -sf http://127.0.0.1:4096/api/worktree/$PID"
}

func (opencode2Profile) WorktreeCreateCmd(spec WorktreeSpec) string {
	directory := fmt.Sprintf("$HOME/.local/share/opencode/worktree/$PID/%s", spec.Name)
	body := fmt.Sprintf(`{\"strategy\":\"git_worktree\",\"name\":\"%s\",\"directory\":\"%s\"}`, spec.Name, directory)
	if spec.Base != "" {
		body = fmt.Sprintf(
			`{\"strategy\":\"git_worktree\",\"name\":\"%s\",\"directory\":\"%s\",\"from\":\"%s\"}`,
			spec.Name,
			directory,
			spec.Base,
		)
	}
	return fmt.Sprintf(
		"PID="+opencode2ProjectIDCmd+"; curl -sf -X POST http://127.0.0.1:4096/api/worktree/$PID -H 'Content-Type: application/json' -d \"%s\"",
		body,
	)
}

func (opencode2Profile) WorktreeParseDir(stdout string) (string, bool) {
	return parseWorktreeDirectory(stdout)
}

func (opencode2Profile) LatestVersion(ctx context.Context) (string, error) {
	return latestOpenCode2Version(ctx)
}

func (opencode2Profile) NewerThan(a, b string) (bool, error) {
	return newerOpenCode2Than(a, b)
}

func (opencode2Profile) AttachCommand(target string, args []string) string {
	parts := []string{"opencode2", "--server", "http://127.0.0.1:4096", "--dir", target}
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}
