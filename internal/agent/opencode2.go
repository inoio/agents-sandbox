package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// opencode2Name is the canonical registry name of the opencode 2 agent.
const opencode2Name = "opencode2"

// opencode2Port is the forwarded guest port the opencode2 serve daemon binds,
// matching the sandbox's fixed host/guest port mapping.
const opencode2Port = "4096"

// opencode2ServerUsername and opencode2ServerPassword are the fixed basic-auth
// credential the opencode2 daemon, the health probe, and the in-VM client share.
// v2's serve enforces HTTP basic auth on every /api/* route and, when
// OPENCODE_SERVER_PASSWORD is unset, generates a random password the launcher
// cannot know — which left the daemon permanently unhealthy. v2 hardcodes the
// basic-auth username to "opencode" (OPENCODE_SERVER_USERNAME no longer exists),
// so the values are equal; only the password is baked via
// OPENCODE_SERVER_PASSWORD, the username being implied.
const (
	opencode2ServerUsername = "opencode"
	opencode2ServerPassword = "opencode"
)

// opencode2CurlFlags apply to every v2 worktree API call: -m bounds the request
// so a hung daemon cannot block the launcher indefinitely, and -sS keeps curl's
// error message on stderr (plain -s would silence it, leaving an empty error
// message) while -f makes an HTTP error status a failure.
const opencode2CurlFlags = "-fsS -m 30"

// opencode2CurlAuth is the curl basic-auth flag for the fixed opencode2 server
// credential, used by the health probe and the worktree API calls.
const opencode2CurlAuth = "-u " + opencode2ServerUsername + ":" + opencode2ServerPassword

// opencode2Profile is the opencode 2 (beta) coding agent. It embeds
// opencodeConfig for the guest config path, auth, and host drop-in it shares
// with v1 (v2 reads the same ~/.config/opencode files and auth.json), overrides
// the launcher config dir to its own namespace, and overrides the daemon,
// worktree, attach, and version behavior for the v2 server API.
type opencode2Profile struct {
	opencodeConfig
}

func (opencode2Profile) Name() string { return opencode2Name }

// GuestTools declares the daemon's in-VM runtime tools for the image-build check.
func (opencode2Profile) GuestTools() []string { return daemonGuestTools }

// ConfigDirName scopes opencode2's own launcher config dir; the guest config
// path and host drop-in still come from the embedded opencodeConfig.
func (opencode2Profile) ConfigDirName() string { return opencode2Name }

// ConfigDirAliases names the legacy shared config dir opencode2 used before it
// gained its own namespace.
func (opencode2Profile) ConfigDirAliases() []string { return []string{opencodeConfigDirName} }

// WorktreeCreatesDetachedHead reports that v2's daemon creates worktrees on a
// detached HEAD (v1 keeps its own branch), so the launcher can tell the user how
// to keep their commits.
func (opencode2Profile) WorktreeCreatesDetachedHead() bool { return true }

func (opencode2Profile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg: versionArgFor(opencode2Name),
		// The sandbox pins the baked version and resolves upgrades itself; the
		// env var disables opencode's own auto-update checks at startup. The
		// fixed server password gives the launcher and the in-VM client a
		// credential v2's mandatory basic auth accepts.
		AgentEnv: map[string]string{
			"OPENCODE_DISABLE_AUTOUPDATE": opencodeAutoupdateDisabled,
			"OPENCODE_SERVER_PASSWORD":    opencode2ServerPassword,
		},
		InstallCommand: `curl -fsSL https://opencode.ai/v2/install | bash -s -- --version "$OPENCODE2_VERSION" && cp /root/.opencode/bin/opencode /opt/agents-sandbox/bin/opencode2`,
		RequiresNode:   false,
		OwnedToolPaths: nil,
	}
}

// DaemonCommand launches the v2 serve daemon as a runtime-owned detached job,
// binding loopback for a co-located client or the external interface for a
// forwarded port.
func (opencode2Profile) DaemonCommand(serveOnly bool) DaemonCommand {
	hostname := daemonLoopbackHostname
	if serveOnly {
		hostname = daemonExternalHostname
	}
	return DaemonCommand{
		Binary: "opencode2",
		Args:   []string{"serve", "--hostname", hostname, "--port", opencode2Port},
	}
}

func (opencode2Profile) DaemonHealthCmd() string {
	// v2's health endpoint is /api/info. There is no /api/health route: once
	// authenticated it answers 404 with an empty body, which fails to parse as
	// JSON and so reads as not-ready. No -f: on an HTTP error the body (e.g.
	// v2's UnauthorizedError) is kept for the probe's diagnostic detail instead
	// of being swallowed.
	return "curl -sm2 " + opencode2CurlAuth + " http://127.0.0.1:" + opencode2Port + "/api/info"
}

// parseOpencode2Health decodes v2's ServerInfo response. Unlike v1's
// {"healthy": bool}, /api/info has no health flag: a response carrying a
// version means the server is ready.
func parseOpencode2Health(stdout string) (bool, error) {
	var serverInfo struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(stdout), &serverInfo); err != nil {
		return false, fmt.Errorf("parse health response: %w", err)
	}
	return serverInfo.Version != "", nil
}

func (opencode2Profile) DaemonHealthParse(stdout string) (bool, error) {
	return parseOpencode2Health(stdout)
}

// DaemonAuthHint: v2's basic auth is mandatory with a baked credential, so an
// external client must use it rather than setting its own.
func (opencode2Profile) DaemonAuthHint() string {
	return "The server requires basic auth: connect as user \"" + opencode2ServerUsername +
		"\" with password \"" + opencode2ServerPassword + "\" (baked into the image)."
}

// opencode2ServerURL is the loopback base URL of the opencode2 serve daemon,
// used by the worktree API calls.
const opencode2ServerURL = "http://127.0.0.1:" + opencode2Port

// WorktreeResolveCmd resolves the launch directory to its project through v2's
// /api/location route, which registers the project as a side effect.
func (opencode2Profile) WorktreeResolveCmd(target string) string {
	return "curl " + opencode2CurlFlags + " " + opencode2CurlAuth + " --get --data-urlencode " +
		shellQuote("location[directory]="+target) + " " + opencode2ServerURL + "/api/location"
}

// WorktreeParseProject decodes /api/location. Both the id and the canonical
// directory are required: an empty id would send an invalid projectID, and an
// empty canonical would filter the list against "" and keep the main checkout.
func parseOpencode2WorktreeProject(stdout string) (WorktreeProject, bool) {
	var location struct {
		Project struct {
			ID        string `json:"id"`
			Canonical string `json:"canonical"`
		} `json:"project"`
	}
	if err := json.Unmarshal([]byte(stdout), &location); err != nil {
		return WorktreeProject{}, false
	}
	if location.Project.ID == "" || location.Project.Canonical == "" {
		return WorktreeProject{}, false
	}
	return WorktreeProject{ID: location.Project.ID, Canonical: location.Project.Canonical}, true
}

func (opencode2Profile) WorktreeParseProject(stdout string) (WorktreeProject, bool) {
	return parseOpencode2WorktreeProject(stdout)
}

// WorktreeListCmd lists the project's worktrees. The canonical root is dropped
// by WorktreeParseList, not here.
func (opencode2Profile) WorktreeListCmd(project WorktreeProject) string {
	return "curl " + opencode2CurlFlags + " " + opencode2CurlAuth + " " +
		opencode2ServerURL + "/api/worktree?projectID=" + shellQuote(project.ID)
}

// WorktreeParseList decodes /api/worktree into directories, dropping the
// canonical root. Every entry must carry a non-empty string directory: a
// renamed, blanked, or retyped field would otherwise be skipped by the
// base-name match and a second worktree created beside the existing one.
func parseOpencode2WorktreeList(stdout string, canonical string) ([]string, bool) {
	var entries []json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil || entries == nil {
		return nil, false
	}
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		var info struct {
			Directory string `json:"directory"`
		}
		if json.Unmarshal(entry, &info) != nil || info.Directory == "" {
			return nil, false
		}
		if info.Directory == canonical {
			continue
		}
		dirs = append(dirs, info.Directory)
	}
	return dirs, true
}

func (opencode2Profile) WorktreeParseList(stdout string, project WorktreeProject) ([]string, bool) {
	return parseOpencode2WorktreeList(stdout, project.Canonical)
}

// worktreeCreateInput is v2's Worktree.CreateInput (additionalProperties:
// false): only projectID, name, and an optional branch.
type worktreeCreateInput struct {
	ProjectID string `json:"projectID"`
	Name      string `json:"name"`
	Branch    string `json:"branch,omitempty"`
}

// WorktreeCreateCmd creates a worktree. The body is built in Go and
// shell-quoted, so arbitrary names and refs need no guest tooling.
func (opencode2Profile) WorktreeCreateCmd(spec WorktreeSpec, project WorktreeProject) string {
	body, _ := json.Marshal(worktreeCreateInput{
		ProjectID: project.ID,
		Name:      spec.Name,
		Branch:    spec.Base,
	})
	return "curl " + opencode2CurlFlags + " " + opencode2CurlAuth + " -X POST " + opencode2ServerURL + "/api/worktree " +
		"-H 'Content-Type: application/json' -d " + shellQuote(
		string(body),
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

// VersionCmd and ParseVersion let the launcher read the version baked into the
// runner image, so opencode2's agent_version baseline is recorded on boot like
// the other profiles'. Without them the baseline stayed empty and a build could
// silently push the image back to the recorded release.
func (opencode2Profile) VersionCmd() string { return "opencode2 --version" }
func (opencode2Profile) ParseVersion(stdout string) (string, error) {
	return extractSemverFromOutput(stdout)
}

func (opencode2Profile) AttachCommand(target string, args []string) string {
	// v2 takes the working directory as a positional argument; it has no --dir
	// flag (which the launcher passed until it learned v2's CLI shape).
	parts := []string{"opencode2", "--server", "http://127.0.0.1:4096", shellQuote(target)}
	parts = append(parts, shellQuoteArgs(args)...)
	return strings.Join(parts, " ")
}

// EventStream exposes v2's authed /api/event SSE stream, which carries a flat
// envelope (type plus data.sessionID) unlike v1's nested payload shape. A user
// turn is one session.execution.started/succeeded pair and awaiting input is a
// permission.asked or form.created event.
func (opencode2Profile) EventStream() EventStreamSpec {
	return EventStreamSpec{
		StreamCommand:     "curl -N -s " + opencode2CurlAuth + " " + opencode2ServerURL + "/api/event",
		BusyEvents:        []string{"session.execution.started", "session.step.started"},
		AwaitingInput:     []string{"permission.asked", "form.created"},
		IdleEvents:        []string{"session.execution.succeeded"},
		ErrorEvents:       []string{sessionErrorEvent, "session.execution.failed"},
		StickyErrorEvents: nil,
		SessionEvents:     []string{"session.created"},
		SessionList: NewSessionList(
			"curl "+opencode2CurlFlags+" "+opencode2CurlAuth+" "+opencode2ServerURL+"/api/session",
			parseOpencodeV2SessionList,
		),
		Name:  opencode2Name,
		Parse: parseOpencodeV2Event,
	}
}

// parseOpencodeV2Event decodes v2's /api/event SSE payload: the event type and
// session id live at data.sessionID, with two fallbacks the flat envelope makes
// necessary — a form.created nests its session id under data.form.sessionID, and
// a session event carries the parent (non-empty for a subagent session) under
// data.info.parentID.
func parseOpencodeV2Event(payload []byte) (StreamEvent, bool) {
	var raw struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Data struct {
			SessionID string `json:"sessionID"`
			ParentID  string `json:"parentID"`
			Form      struct {
				SessionID string `json:"sessionID"`
			} `json:"form"`
			Info struct {
				ParentID string `json:"parentID"`
			} `json:"info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return StreamEvent{}, false
	}
	if raw.Type == "" {
		return StreamEvent{}, false
	}
	sessionID := raw.Data.SessionID
	if sessionID == "" {
		sessionID = raw.Data.Form.SessionID
	}
	parentID := raw.Data.ParentID
	if parentID == "" {
		parentID = raw.Data.Info.ParentID
	}
	return StreamEvent{Type: raw.Type, SessionID: sessionID, ParentID: parentID, EventID: raw.ID}, true
}

// parseOpencodeV2SessionList extracts child session ids from v2's /api/session
// response, which wraps the array under "data".
func parseOpencodeV2SessionList(payload []byte) []string {
	var body struct {
		Data []sessionParent `json:"data"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil
	}
	return childSessionIDs(body.Data)
}
