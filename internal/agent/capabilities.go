package agent

import "context"

const (
	authFileName               = "auth.json"
	modelsFileName             = "models.json"
	authAccessField            = "access"
	authRefreshField           = "refresh"
	authAccessTokenField       = "accesstoken"
	authKeyField               = "key"
	authAccessKeyField         = "accesskey"
	authAPIKeyType             = "api_key"
	authAnthropicProvider      = "anthropic"
	authAPIKeyField            = "apikey"
	authAPITokenField          = "apitoken"
	authAuthorizationField     = "authorization"
	authBearerTokenField       = "bearertoken"
	authClientSecretField      = "clientsecret"
	authCookieField            = "cookie"
	authCredentialField        = "credential"
	authCredentialsField       = "credentials"
	authIDTokenField           = "idtoken"
	authTypeField              = "type"
	authPasswordField          = "password"
	authPrivateKeyField        = "privatekey"
	authPrivateTokenField      = "privatetoken"
	authOAuthAccessField       = "oauthaccess"
	authOAuthRefreshField      = "oauthrefresh"
	authRefreshTokenField      = "refreshtoken"
	authSecretField            = "secret"
	authSecretAccessKeyField   = "secretaccesskey"
	authSessionField           = "session"
	authTokenField             = "token"
	authXAPIKeyField           = "xapikey"
	authSessionTokenField      = "sessiontoken"
	authBaseURLField           = "baseURL"
	authBaseURLLowerField      = "baseUrl"
	authEndpointField          = "endpoint"
	authURLField               = "url"
	anthropicAPIHost           = "api.anthropic.com"
	githubCopilotProvider      = "github-copilot"
	githubCopilotAPIHost       = "api.githubcopilot.com"
	githubCopilotLoginHost     = "github.com"
	oauthAuthType              = "oauth"
	migrationPlaceholderPrefix = "$MSB_"
)

// settingsFileName is the settings filename shared by the pi and claude-code
// agents, whose merged snippet config is written to <config dir>/settings.json.
const settingsFileName = "settings.json"

// defaultSensitiveAuthFields and defaultConfigSensitiveFields are the credential
// field names every agent spec treats as sensitive, in the native auth document
// and the merged config respectively. They are read-only package constants in
// effect; Go has no slice constants.
//
//nolint:gochecknoglobals // shared immutable credential-field sets
var (
	defaultSensitiveAuthFields = []string{
		authAccessField,
		authAccessKeyField,
		authAccessTokenField,
		authAPIKeyType,
		authAPIKeyField,
		authAPITokenField,
		authAuthorizationField,
		authBearerTokenField,
		authClientSecretField,
		authCookieField,
		authCredentialField,
		authCredentialsField,
		authIDTokenField,
		authKeyField,
		authPasswordField,
		authPrivateKeyField,
		authPrivateTokenField,
		authRefreshField,
		authRefreshTokenField,
		authSecretField,
		authSessionField,
		authSessionTokenField,
		authTokenField,
		authXAPIKeyField,
	}
	defaultConfigSensitiveFields = []string{
		authAccessKeyField,
		authAccessTokenField,
		authAPIKeyField,
		authAPITokenField,
		authAuthorizationField,
		authBearerTokenField,
		authClientSecretField,
		authCredentialField,
		authCredentialsField,
		authIDTokenField,
		authOAuthAccessField,
		authOAuthRefreshField,
		authPasswordField,
		authPrivateKeyField,
		authPrivateTokenField,
		authRefreshTokenField,
		authSecretField,
		authSecretAccessKeyField,
		authSessionField,
		authSessionTokenField,
		authTokenField,
		authXAPIKeyField,
	}
)

// Daemon bind hostnames: loopback for a co-located client, the external
// interface when the host forwards a published port into the VM.
const (
	daemonLoopbackHostname = "127.0.0.1"
	daemonExternalHostname = "0.0.0.0"
)

// DaemonCommand is the executable and arguments that launch an agent's serve
// daemon as a runtime-owned detached job.
type DaemonCommand struct {
	Binary string
	Args   []string
}

// DaemonProvider runs a long-lived server that clients attach to (opencode).
type DaemonProvider interface {
	DaemonCommand(serveOnly bool) DaemonCommand
	DaemonHealthCmd() string
	DaemonHealthParse(stdout string) (bool, error)
	// DaemonAuthHint is the one-line basic-auth note shown in serve-only
	// guidance, telling an external client how to authenticate (or that auth is
	// optional). It is empty when the daemon needs no credentials.
	DaemonAuthHint() string
}

// GuestToolRequirer declares the shell tools the launcher invokes inside the
// running VM for this agent's runtime: curl for the daemon health probe,
// session and worktree calls, and the notify event stream. The daemon itself is
// launched and stopped as a runtime-owned job, so no guest process control tool
// is needed. The renderer verifies each with command -v in the
// final runner stage, so a custom base missing one fails the image build naming
// the tool instead of failing at runtime. Agents that run no daemon in the VM
// (pi, claude-code) do not implement it.
type GuestToolRequirer interface {
	GuestTools() []string
}

// WorktreeProject identifies the daemon project a worktree request is scoped to.
// Canonical is the repository root, which the worktree list must exclude so a
// worktree named like the repository never reuses the main checkout. Both fields
// are required: a request is never sent with an empty ID, and the list is never
// filtered against an empty canonical.
type WorktreeProject struct {
	ID        string
	Canonical string
}

// WorktreeProvider manages git worktrees through a running daemon. The daemon's
// JSON is decoded in Go by the owning agent, so no guest command needs jq (which
// a custom runner base need not have). The steps are explicit because v2's
// routes are project-scoped: the resolve command turns the launch directory into
// a project (empty for agents whose routes are global, which skip the step), the
// list and create commands take that project, and each response has its own
// parser.
type WorktreeProvider interface {
	// WorktreeResolveCmd returns the command that resolves the launch directory
	// to a project, or "" when the agent's routes are global (no step).
	WorktreeResolveCmd(target string) string
	// WorktreeParseProject decodes the resolve command's stdout. ok is false on
	// a bad payload or an empty id/canonical.
	WorktreeParseProject(stdout string) (WorktreeProject, bool)
	// WorktreeListCmd returns the command that lists project's worktrees.
	WorktreeListCmd(project WorktreeProject) string
	// WorktreeParseList decodes the list command's stdout into directory paths,
	// already filtered of the project's canonical root. ok is false on a bad
	// payload (a non-array, or an entry without a non-empty directory), so a
	// failed list never reads as "no worktrees".
	WorktreeParseList(stdout string, project WorktreeProject) ([]string, bool)
	// WorktreeCreateCmd returns the command that creates spec's worktree in
	// project.
	WorktreeCreateCmd(spec WorktreeSpec, project WorktreeProject) string
	// WorktreeParseDir decodes the create command's stdout into the created
	// worktree directory.
	WorktreeParseDir(stdout string) (string, bool)
}

// UpgradeChecker can resolve and compare releases.
type UpgradeChecker interface {
	LatestVersion(ctx context.Context) (string, error)
	NewerThan(a, b string) (bool, error)
}

// SessionStatusProvider exposes the daemon's session-status and pending-question
// endpoints so the launcher can wait for active sessions to finish before
// stopping a VM. Only the opencode (v1) daemon exposes these endpoints today;
// agents without them skip the quiescence wait.
type SessionStatusProvider interface {
	SessionStatusCmd() string
	QuestionListCmd() string
}

// ConfigMerger exposes data consumed by the shared merge logic in
// internal/configmerge.BuildMerged.
type ConfigMerger interface {
	SnippetPattern() string
	VMConfigPath(home string) string
	// ConfigFileNames returns the config filenames the agent reads from its VM
	// config directory that the merged config supersedes (and which a stale
	// host copy could otherwise shadow it with).
	ConfigFileNames() []string
}

// AttachRunner starts the client TUI/session.
type AttachRunner interface {
	AttachCommand(target string, args []string) string
}

// Provisioner declares host files for the optional host-config drop-in.
type Provisioner interface {
	ProvisionRules() []ProvisionRule
}

// AuthMigrationSpec describes the provider/auth fields that a migration can
// safely transform for an agent.
type AuthMigrationSpec struct {
	SecretPrefix            string
	AuthFields              map[string][]string
	LoginAuthTypes          []string
	SensitiveFields         []string
	SafeFields              []string
	ConfigSensitiveFields   []string
	EndpointFields          []string
	AuthPlaceholderPrefix   string
	ConfigPlaceholderPrefix string
	ConfigPlaceholderSuffix string
	RejectUnresolvedValues  bool
}

// ConfigMigrationSpec describes the native and managed paths used by a safe
// configuration migration. Supplemental files are migrated independently when
// an agent has multiple native configuration documents.
type ConfigMigrationSpec struct {
	NativeConfigDir           string
	NativeConfigEnv           string
	NativeConfigEnvIsPath     bool
	NativeConfigSubdir        string
	NativeCredential          string
	NativeDataEnv             string
	NativeDataEnvIsPath       bool
	NativeDataSubdir          string
	CredentialTarget          string
	ManagedCredential         string
	ManagedSnippet            string
	NativeConfigFiles         []string
	NativeConfigStrictJSON    bool
	RequiredNetworkHosts      []string
	NativeSupplementalFiles   []string
	ManagedSupplementalFiles  []string
	ProvisioningExcludedFiles []string
	KnownProviderHosts        map[string]string
	LoginHosts                map[string][]string
	Auth                      AuthMigrationSpec
}

// MigrationSpecProvider exposes agent-specific migration metadata without
// coupling agent profiles to the migration implementation.
type MigrationSpecProvider interface {
	MigrationSpec() ConfigMigrationSpec
}

// AsDaemonProvider returns the agent's DaemonProvider, if it implements one.
func AsDaemonProvider(a Agent) (DaemonProvider, bool) {
	p, ok := a.(DaemonProvider)
	return p, ok
}

// AsGuestToolRequirer returns the agent's GuestToolRequirer, if it implements one.
func AsGuestToolRequirer(a Agent) (GuestToolRequirer, bool) {
	p, ok := a.(GuestToolRequirer)
	return p, ok
}

// AsWorktreeProvider returns the agent's WorktreeProvider, if it implements one.
func AsWorktreeProvider(a Agent) (WorktreeProvider, bool) {
	p, ok := a.(WorktreeProvider)
	return p, ok
}

// AsUpgradeChecker returns the agent's UpgradeChecker, if it implements one.
func AsUpgradeChecker(a Agent) (UpgradeChecker, bool) { p, ok := a.(UpgradeChecker); return p, ok }

// LatestVersion resolves a's newest release through its UpgradeChecker. An agent
// without one has no release to look up and reports an empty version, not an
// error, so the image build and the upgrade check share this one resolution
// instead of each wrapping AsUpgradeChecker.
func LatestVersion(ctx context.Context, a Agent) (string, error) {
	checker, ok := AsUpgradeChecker(a)
	if !ok {
		return "", nil
	}
	return checker.LatestVersion(ctx)
}

// AsSessionStatusProvider returns the agent's SessionStatusProvider, if it
// implements one.
func AsSessionStatusProvider(a Agent) (SessionStatusProvider, bool) {
	p, ok := a.(SessionStatusProvider)
	return p, ok
}

// AsConfigMerger returns the agent's ConfigMerger, if it implements one.
func AsConfigMerger(a Agent) (ConfigMerger, bool) { p, ok := a.(ConfigMerger); return p, ok }

// AsAttachRunner returns the agent's AttachRunner, if it implements one.
func AsAttachRunner(a Agent) (AttachRunner, bool) { p, ok := a.(AttachRunner); return p, ok }

// AsProvisioner returns the agent's Provisioner, if it implements one.
func AsProvisioner(a Agent) (Provisioner, bool) { p, ok := a.(Provisioner); return p, ok }

// AsMigrationSpecProvider returns the agent's migration metadata, if it has a
// safe config migration implementation.
func AsMigrationSpecProvider(a Agent) (MigrationSpecProvider, bool) {
	p, ok := a.(MigrationSpecProvider)
	return p, ok
}

// ConfigDirAliaser declares alternate launcher config-dir names this agent used
// before its current ConfigDirName. They are fallback candidates only: the
// primary dir always wins when present.
type ConfigDirAliaser interface {
	ConfigDirAliases() []string
}

// AsConfigDirAliaser returns the agent's ConfigDirAliaser, if it implements one.
func AsConfigDirAliaser(a Agent) (ConfigDirAliaser, bool) {
	p, ok := a.(ConfigDirAliaser)
	return p, ok
}

// DetachedWorktreeReporter declares that the agent's daemon creates worktrees on
// a detached HEAD rather than a branch, so the launcher can warn the user to
// create a branch before they commit (and lose the work when the guest home
// volume goes away). Agents that keep a branch do not implement it.
type DetachedWorktreeReporter interface {
	WorktreeCreatesDetachedHead() bool
}

// AsDetachedWorktreeReporter returns the agent's DetachedWorktreeReporter, if it
// implements one.
func AsDetachedWorktreeReporter(a Agent) (DetachedWorktreeReporter, bool) {
	p, ok := a.(DetachedWorktreeReporter)
	return p, ok
}

// VersionProvider exposes how to read the installed agent version from a
// running image: the shell command to run and a parser for its output. Agents
// print their version in different formats, so parsing is per-agent.
type VersionProvider interface {
	VersionCmd() string
	ParseVersion(stdout string) (string, error)
}

// AsVersionProvider returns the agent's VersionProvider, if it implements one.
func AsVersionProvider(a Agent) (VersionProvider, bool) { p, ok := a.(VersionProvider); return p, ok }

// StreamEvent is a decoded daemon event: its type, the session it belongs to,
// and that session's parent (non-empty for a subagent session).
type StreamEvent struct {
	Type      string
	SessionID string
	ParentID  string
	// EventID is the event's envelope id, unique per emission. It tells two
	// input requests apart when a session already awaits input: a turn can ask
	// for two approvals back to back in one step, so neither a step boundary
	// nor the session state distinguishes them, but their ids do. Empty when
	// the envelope carries no id.
	EventID string
}

// EventStreamSpec describes the SSE event stream notify watches for an agent:
// the shell command that streams events in the VM, the event-type names that
// drive tracker transitions, and the agent name used in notification titles.
type EventStreamSpec struct {
	StreamCommand string
	BusyEvents    []string
	AwaitingInput []string
	IdleEvents    []string
	ErrorEvents   []string
	// StickyErrorEvents is the subset of ErrorEvents that latches the session
	// in the error state for the watcher's lifetime — reconnecting does not
	// clear it, since the watcher reuses one tracker across reconnects. An
	// error event not listed here ends only the current turn, so the session
	// notifies again on its next turn. Empty means no error event latches.
	StickyErrorEvents []string
	// SessionEvents are lifecycle event types whose payload may identify a
	// session's parent. A non-empty parent ID marks the session as a subagent
	// (child) session; no notification ever fires for a child session.
	SessionEvents []string
	// SessionList seeds the tracker with sessions created before the watcher
	// (re)connected, by running one shell command and parsing its output. Nil
	// disables seeding; a non-nil value always carries both the command and its
	// parser, so seeding can never be disabled by an omitted parser.
	SessionList *SessionListSpec
	Name        string
	// Parse decodes one SSE data payload into a StreamEvent. ok is false when
	// the payload carries no event.
	Parse func(payload []byte) (StreamEvent, bool)
}

// SessionListSpec is an agent's one-shot session-list command together with the
// parser for its stdout. The two belong together: a command without a parser
// cannot seed anything, and a nil parser once made the notify watcher silently
// skip seeding. Build one with NewSessionList.
type SessionListSpec struct {
	command string
	parse   func(payload []byte) []string
}

// NewSessionList returns the seeding spec for command, using parse to extract
// child session IDs from its stdout. Both are required: an agent that declares
// a session list must also declare how to read it, so an incomplete pair is a
// programming error and fails fast. A profile that omits its parser would
// otherwise silently lose subagent filtering. The parser owns the list's JSON
// shape, so the command needs no jq (or other tooling) in the runner image,
// which a custom base need not have.
func NewSessionList(command string, parse func(payload []byte) []string) *SessionListSpec {
	if command == "" {
		panic("agent.NewSessionList: empty command")
	}
	if parse == nil {
		panic("agent.NewSessionList: nil parser")
	}
	return &SessionListSpec{command: command, parse: parse}
}

// Command is the shell command that prints the daemon's raw session list.
func (s *SessionListSpec) Command() string { return s.command }

// Parse extracts the child session IDs from Command's stdout.
func (s *SessionListSpec) Parse(payload []byte) []string { return s.parse(payload) }

// EventStreamProvider exposes the SSE event stream an agent daemon serves.
type EventStreamProvider interface {
	EventStream() EventStreamSpec
}

// AsEventStreamProvider returns the agent's EventStreamProvider, if it
// implements one.
func AsEventStreamProvider(a Agent) (EventStreamProvider, bool) {
	p, ok := a.(EventStreamProvider)
	return p, ok
}
