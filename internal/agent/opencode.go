package agent

import (
	"context"
	"fmt"
	"strings"
)

//nolint:gochecknoinits // built-in agent self-registration
func init() { Register(opencodeProfile{opencodeConfig: opencodeConfig{}}) }

// opencodeProfile is the opencode (v1) coding agent. It embeds opencodeConfig
// for the config handling it shares with opencode2.
type opencodeProfile struct {
	opencodeConfig
}

func (opencodeProfile) Name() string { return opencodeName }

func (opencodeProfile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg:     versionArgFor(opencodeName),
		VersionLabel:   versionLabelFor(opencodeName),
		AgentEnv:       map[string]string{"OPENCODE_DISABLE_AUTOUPDATE": "true"},
		InstallCommand: `curl -fsSL https://opencode.ai/install | bash -s -- --version "$OPENCODE_VERSION" && cp /root/.opencode/bin/opencode /usr/local/bin`,
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
	return parseDaemonHealth(stdout)
}

// SessionStatusCmd returns the v1 server endpoint listing session states, used
// by the launcher to wait for active sessions to finish before stopping a VM.
func (opencodeProfile) SessionStatusCmd() string {
	return "curl -sf http://127.0.0.1:4096/session/status"
}

// QuestionListCmd returns the v1 server endpoint listing pending questions,
// used by the launcher to keep sessions awaiting user input from being cut off.
func (opencodeProfile) QuestionListCmd() string {
	return "curl -sf http://127.0.0.1:4096/question"
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
	return parseWorktreeDirectory(stdout)
}

func (opencodeProfile) LatestVersion(ctx context.Context) (string, error) {
	return latestOpenCodeVersion(ctx)
}

func (opencodeProfile) NewerThan(a, b string) (bool, error) { return newerOpenCodeThan(a, b) }

func (opencodeProfile) AttachCommand(target string, args []string) string {
	parts := []string{"opencode", "attach", "http://127.0.0.1:4096", "--dir", target}
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}
