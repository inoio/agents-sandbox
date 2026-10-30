package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// opencode1Name is the canonical registry name of the opencode (v1) agent.
const opencode1Name = "opencode"

// opencodeProfile is the opencode (v1) coding agent. It embeds opencodeConfig
// for the guest config path, auth, and host drop-in it shares with opencode2.
type opencodeProfile struct {
	opencodeConfig
}

func (opencodeProfile) Name() string { return opencode1Name }

// GuestTools declares the daemon's in-VM runtime tools for the image-build check.
func (opencodeProfile) GuestTools() []string { return daemonGuestTools }

func (opencodeProfile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg: versionArgFor(opencode1Name),
		AgentEnv: map[string]string{
			"OPENCODE_DISABLE_AUTOUPDATE":      opencodeAutoupdateDisabled,
			"OPENCODE_EXPERIMENTAL_WORKSPACES": "true",
		},
		InstallCommand: `curl -fsSL https://opencode.ai/install | bash -s -- --version "$OPENCODE_VERSION" && cp /root/.opencode/bin/opencode /opt/agents-sandbox/bin`,
		RequiresNode:   false,
		OwnedToolPaths: nil,
		RuntimeLibs:    nil,
	}
}

// DaemonCommand launches the v1 serve daemon as a runtime-owned detached job,
// binding loopback for a co-located client or the external interface for a
// forwarded port.
func (opencodeProfile) DaemonCommand(serveOnly bool) DaemonCommand {
	hostname := daemonLoopbackHostname
	if serveOnly {
		hostname = daemonExternalHostname
	}
	return DaemonCommand{
		Binary: opencodeCmdName,
		Args:   []string{"serve", "--hostname", hostname, "--port", "4096"},
	}
}

func (opencodeProfile) DaemonHealthCmd() string {
	// No -f: on an HTTP error the body is kept for the health probe's
	// diagnostic detail instead of being swallowed.
	return "curl -sm2 http://127.0.0.1:4096/global/health"
}

// DaemonAuthHint: v1's basic auth is opt-in (its /global/health is
// unauthenticated), so the credentials are the user's to set.
func (opencodeProfile) DaemonAuthHint() string {
	return "Optional: set OPENCODE_SERVER_PASSWORD (and OPENCODE_SERVER_USERNAME) to protect the server with basic auth."
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

// WorktreeResolveCmd: v1's routes are global, so there is no resolution step.
func (opencodeProfile) WorktreeResolveCmd(string) string { return "" }

// WorktreeParseProject is never called: v1 has no resolve step to produce a
// project.
func (opencodeProfile) WorktreeParseProject(string) (WorktreeProject, bool) {
	return WorktreeProject{}, false
}

// WorktreeListCmd lists worktrees via v1's global route.
func (opencodeProfile) WorktreeListCmd(WorktreeProject) string {
	return "curl -sf http://127.0.0.1:4096/experimental/worktree"
}

// WorktreeParseList decodes v1's list — path strings or {directory} objects —
// into directory paths. A non-array payload, or an entry without a non-empty
// string directory, is not ok, so a route or field rename never reads as "no
// worktrees".
func (opencodeProfile) WorktreeParseList(stdout string, _ WorktreeProject) ([]string, bool) {
	var entries []json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil || entries == nil {
		return nil, false
	}
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		var directory string
		if len(entry) > 0 && entry[0] == '"' {
			// The outer unmarshal already validated entry as JSON, so a value
			// starting with a quote is a string and decoding it cannot fail; a
			// rejected value still fails the empty check below.
			_ = json.Unmarshal(entry, &directory)
		} else {
			var info struct {
				Directory string `json:"directory"`
			}
			if json.Unmarshal(entry, &info) != nil {
				return nil, false
			}
			directory = info.Directory
		}
		if directory == "" {
			return nil, false
		}
		dirs = append(dirs, directory)
	}
	return dirs, true
}

func (opencodeProfile) WorktreeCreateCmd(spec WorktreeSpec, _ WorktreeProject) string {
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
	parts := []string{opencodeCmdName, "attach", "http://127.0.0.1:4096", "--dir", shellQuote(target)}
	parts = append(parts, shellQuoteArgs(args)...)
	return strings.Join(parts, " ")
}

func (opencodeProfile) VersionCmd() string { return opencodeCmdName + " --version" }
func (opencodeProfile) ParseVersion(stdout string) (string, error) {
	return extractSemverFromOutput(stdout)
}

// sessionErrorEvent is the session-level error event shared by the opencode
// profiles: v1 latches on it, v2 treats it as turn-scoped like its other
// error event.
const sessionErrorEvent = "session.error"

func (opencodeProfile) EventStream() EventStreamSpec {
	return EventStreamSpec{
		StreamCommand:     "curl -N -s http://127.0.0.1:4096/global/event",
		BusyEvents:        []string{"message.part.updated", "session.updated"},
		AwaitingInput:     []string{"permission.asked", "question.asked"},
		IdleEvents:        []string{"session.idle"},
		ErrorEvents:       []string{sessionErrorEvent},
		StickyErrorEvents: []string{sessionErrorEvent},
		SessionEvents:     []string{"session.created", "session.updated"},
		SessionList:       NewSessionList("curl -sf -m 30 http://127.0.0.1:4096/session", parseOpencodeV1SessionList),
		Name:              opencode1Name,
		Parse:             parseOpencodeV1Event,
	}
}

// parseOpencodeV1Event decodes v1's /global/event SSE payload: the event type
// and session id live under payload.{type,properties.sessionID}, and a
// session.created event's parent (non-empty for a subagent session) lives
// under payload.properties.info.parentID.
func parseOpencodeV1Event(payload []byte) (StreamEvent, bool) {
	var raw struct {
		Payload struct {
			ID         string `json:"id"`
			Type       string `json:"type"`
			Properties struct {
				SessionID string `json:"sessionID"`
				Info      struct {
					ParentID string `json:"parentID"`
				} `json:"info"`
			} `json:"properties"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return StreamEvent{}, false
	}
	if raw.Payload.Type == "" {
		return StreamEvent{}, false
	}
	return StreamEvent{
		Type:      raw.Payload.Type,
		SessionID: raw.Payload.Properties.SessionID,
		ParentID:  raw.Payload.Properties.Info.ParentID,
		EventID:   raw.Payload.ID,
	}, true
}

// sessionParent is the (id, parentID) pair both agents' session lists carry.
type sessionParent struct {
	ID       string `json:"id"`
	ParentID string `json:"parentID"`
}

// childSessionIDs returns the ids of sessions carrying a non-empty parentID,
// the one rule both agents' session lists share.
func childSessionIDs(sessions []sessionParent) []string {
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		if session.ParentID != "" {
			ids = append(ids, session.ID)
		}
	}
	return ids
}

// parseOpencodeV1SessionList extracts child session ids from v1's /session
// response, a JSON array of sessions each carrying an optional parentID.
func parseOpencodeV1SessionList(payload []byte) []string {
	var sessions []sessionParent
	if err := json.Unmarshal(payload, &sessions); err != nil {
		return nil
	}
	return childSessionIDs(sessions)
}
