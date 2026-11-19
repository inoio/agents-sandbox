package analyser

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/atomicwrite"
	"github.com/inoio/agents-sandbox/internal/configdecode"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/projecttrust"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/resourceproposal"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

// applyFindingKey is the Select key that accepts one finding's writes.
const applyFindingKey = "a"

// skipFindingKey is the Select key that declines one finding. It is the prompt's
// default, so an unanswered prompt declines.
const skipFindingKey = "s"

// nonInteractiveNotice is the single line a non-interactive run prints. It names
// the commands that produce proposals, and the run writes nothing (invariant 6).
const nonInteractiveNotice = "Learn-mode proposals need an interactive terminal; run `agents-sandbox run --learn` or `agents-sandbox config harden` to review them."

// ApplyOptions carries the dependencies the applier writes through. Paths, Slug,
// UI and Mode are required; SecretValue is required only when a finding proposes
// a static secret; SetByFlag may be nil (a config-only run).
type ApplyOptions struct {
	Paths        configpaths.ConfigPaths
	Slug         string // repo slug (per-slug default + decline key)
	CheckoutSlug string // for a project-scope write's approval
	// Agent is the agent whose config-dir name a managed-config write targets.
	Agent string
	UI    termio.Prompter
	Mode  options.Mode
	// SetByFlag reports whether a flag set a launcher config key.
	SetByFlag func(key string) bool
	// SecretValue resolves a finding's static secret value in memory (from the
	// read-back); it is never persisted and never logged. nil for config-only.
	SecretValue func(w SecretWrite) (string, bool)
	// AcceptAll applies every finding without asking. It is set only by an
	// explicit top-level "Save all", never by --yes.
	AcceptAll bool
}

// Apply walks findings, applying each finding's writes together after one
// explicit acceptance. It writes nothing under a preview mode, prints one line
// and writes nothing when the terminal is non-interactive, skips a finding the
// decline memory holds for its evidence hash, and skips a per-slug write whose
// key the project layer owns (a shadowed no-op). A move applies its writes or
// none: if the project removal fails, the per-slug addition does not run.
func Apply(findings []Finding, opts ApplyOptions) []ApplyResult {
	if opts.Mode.Previews() {
		return allSkipped(findings, "preview: the run makes no changes")
	}
	if opts.UI == nil || !opts.UI.IsInteractive() {
		if opts.UI != nil {
			// Acknowledge prints a one-time notice unconditionally and, on a
			// non-interactive terminal, returns without waiting.
			_ = opts.UI.Acknowledge(nonInteractiveNotice)
		}
		return allSkipped(findings, "no interactive terminal")
	}
	results := make([]ApplyResult, 0, len(findings))
	for _, finding := range findings {
		results = append(results, applyFinding(finding, opts))
	}
	return results
}

// allSkipped reports every finding as skipped with note, without asking or
// writing.
func allSkipped(findings []Finding, note string) []ApplyResult {
	results := make([]ApplyResult, 0, len(findings))
	for _, finding := range findings {
		results = append(results, newApplyResult(finding, OutcomeSkipped, note, ""))
	}
	return results
}

// newApplyResult builds a result with every field set, so a partial literal
// never reaches the linter's exhaustiveness check.
func newApplyResult(finding Finding, outcome Outcome, note, errText string) ApplyResult {
	return ApplyResult{
		Finding:   finding,
		Outcome:   outcome,
		WroteDirs: nil,
		Note:      note,
		Err:       errText,
	}
}

// applyFinding decides and performs one finding's writes. It consults the
// decline memory, validates every write before touching the filesystem, asks
// once, and applies the writes in order.
func applyFinding(finding Finding, opts ApplyOptions) ApplyResult {
	result := newApplyResult(finding, "", "", "")

	if finding.EvidenceHash != "" {
		declined, err := state.LearnDeclined(opts.Slug, finding.EvidenceHash)
		if err != nil {
			result.Outcome = OutcomeFailed
			result.Err = err.Error()
			return result
		}
		if declined {
			result.Outcome = OutcomeSkipped
			result.Note = "declined for this evidence"
			return result
		}
	}

	plans, refusal := planWrites(finding, opts)
	if refusal != nil {
		result.Outcome = OutcomeSkipped
		result.Note = refusal.note
		result.Err = refusal.err
		return result
	}

	accepted, err := askApplyOrAcceptAll(opts, finding)
	if err != nil {
		result.Outcome = OutcomeFailed
		result.Err = err.Error()
		return result
	}
	if !accepted {
		result.Outcome = OutcomeDeclined
		if recordErr := recordDecline(opts.Slug, finding.EvidenceHash); recordErr != nil {
			result.Err = recordErr.Error()
		}
		return result
	}

	for _, plan := range plans {
		dir, err := executeWrite(plan, opts)
		if err != nil {
			// planWrites refuses a target it cannot edit before any write runs,
			// so an execution-time failure is an unexpected one: it fails the
			// finding rather than reading as a skip.
			result.Outcome = OutcomeFailed
			result.Err = err.Error()
			return result
		}
		result.WroteDirs = append(result.WroteDirs, dir)
	}
	result.Outcome = OutcomeApplied
	return result
}

// recordDecline remembers a declined finding. An empty evidence hash has no
// decline record, so it is not one to write.
func recordDecline(slug, evidenceHash string) error {
	if evidenceHash == "" {
		return nil
	}
	return state.RecordLearnDecline(slug, evidenceHash)
}

// plannedWrite is one write resolved to its target directory, with a static
// secret's value resolved in memory.
type plannedWrite struct {
	write       Write
	dir         string
	secretValue string
}

// refusal is a write the applier will not make: a shadowed key, a secret with no
// derivable host, or a config file it cannot edit. The finding is reported
// Skipped; err, when set, is the underlying condition's message.
type refusal struct {
	note string
	err  string
}

// planWrites resolves and validates every one of a finding's writes without
// writing anything, so a finding that cannot be written whole is refused before
// its first write runs. It returns the plans, or the first refusal.
func planWrites(finding Finding, opts ApplyOptions) ([]plannedWrite, *refusal) {
	plans := make([]plannedWrite, 0, len(finding.Writes))
	for _, write := range finding.Writes {
		dir, refusal := planTarget(write, opts)
		if refusal != nil {
			return nil, refusal
		}
		if write.Secret != nil {
			plan, refusal := planSecret(write, dir, opts)
			if refusal != nil {
				return nil, refusal
			}
			plans = append(plans, plan)
			continue
		}
		if write.ManagedConfig != nil {
			plans = append(plans, plannedWrite{write: write, dir: dir, secretValue: ""})
			continue
		}
		if refusal := planConfigKey(write, dir, opts); refusal != nil {
			return nil, refusal
		}
		plans = append(plans, plannedWrite{write: write, dir: dir, secretValue: ""})
	}
	return plans, nil
}

// planTarget resolves a write's directory and refuses a per-slug config write
// whose key the project layer owns, since the higher-precedence project value
// would shadow it. A project-scope managed-config write is refused: the applier
// has no per-file project approval, and the learn proposal never targets it.
func planTarget(write Write, opts ApplyOptions) (string, *refusal) {
	if write.ManagedConfig != nil {
		if write.Scope == ScopeProject {
			return "", &refusal{
				note: "a managed agent config file is not written to the project scope; " +
					"only the per-slug user scope owns it",
				err: "",
			}
		}
		dir, refusal := managedConfigDir(opts)
		if refusal != nil {
			return "", refusal
		}
		return dir, nil
	}
	if write.Scope == ScopeProject {
		if opts.CheckoutSlug == "" {
			return "", &refusal{note: "a project-scope write has no checkout slug for its approval", err: ""}
		}
		return opts.Paths.ProjectConfigDir(), nil
	}
	if write.Secret != nil {
		return opts.Paths.UserSlugConfigDir(opts.Slug), nil
	}
	owner, owned := viperconfig.LayerSettingOwner(opts.Paths, opts.Slug, write.ConfigKey)
	if owned && owner == opts.Paths.ProjectConfigDir() {
		return "", &refusal{
			note: fmt.Sprintf(
				"%s is set by the project config; a per-slug write would not take effect",
				write.ConfigKey,
			),
			err: "",
		}
	}
	return opts.Paths.UserSlugConfigDir(opts.Slug), nil
}

// managedConfigDir resolves the agent config directory a managed-config write
// targets: the per-slug user scope's dir joined with the agent's primary
// config-dir name, which is what provisioning merges snippets from. It refuses
// an unknown agent or one whose profile declares an unusable config-dir name,
// rather than writing into a directory no scope reads.
func managedConfigDir(opts ApplyOptions) (string, *refusal) {
	profile, ok := agent.Lookup(opts.Agent)
	if !ok {
		return "", &refusal{
			note: fmt.Sprintf("unknown agent %q; a managed agent config file has no target dir", opts.Agent),
			err:  "",
		}
	}
	names, err := agent.ConfigDirNames(profile)
	if err != nil {
		return "", &refusal{
			note: fmt.Sprintf("agent %s has no usable config-dir name: %v", opts.Agent, err),
			err:  err.Error(),
		}
	}
	scope := configpaths.ConfigScope{Kind: configpaths.ScopePerSlugUser, Dir: opts.Paths.UserSlugConfigDir(opts.Slug)}
	return scope.AgentConfigDir(names[0]), nil
}

// planSecret validates that the target secret file can be edited, resolves a
// static secret's value in memory, and refuses a secret with no derivable host
// or no resolvable value. Validating the file first means a finding whose
// removal or addition would fail on an unparseable file is refused whole, before
// any of its writes run. A static secret is never migrated without a host and
// never gains allow_any_host_dangerous; a login credential is never read.
func planSecret(write Write, dir string, opts ApplyOptions) (plannedWrite, *refusal) {
	if err := envsecret.ValidateSecretFile(dir); err != nil {
		return plannedWrite{}, &refusal{
			note: fmt.Sprintf("secret %s cannot be edited in %s: %v", write.Secret.EnvVar, dir, err),
			err:  err.Error(),
		}
	}
	if write.Secret.Remove {
		return plannedWrite{write: write, dir: dir, secretValue: ""}, nil
	}
	if len(write.Secret.Hosts) == 0 {
		return plannedWrite{}, &refusal{
			note: fmt.Sprintf("secret %s has no derivable host; it stays in the sandbox home", write.Secret.EnvVar),
			err:  "",
		}
	}
	value, ok := resolveSecretValue(write.Secret, opts)
	if !ok || value == "" {
		return plannedWrite{}, &refusal{
			note: fmt.Sprintf("secret %s has no resolvable value; it is not migrated", write.Secret.EnvVar),
			err:  "",
		}
	}
	return plannedWrite{write: write, dir: dir, secretValue: value}, nil
}

// resolveSecretValue resolves a static secret's value through the caller's
// in-memory source, and reports false when there is none.
func resolveSecretValue(secret *SecretWrite, opts ApplyOptions) (string, bool) {
	if opts.SecretValue == nil {
		return "", false
	}
	return opts.SecretValue(*secret)
}

// planConfigKey refuses a config-key write that a flag or environment variable
// would shadow, and one whose target file is not an editable YAML document.
func planConfigKey(write Write, dir string, opts ApplyOptions) *refusal {
	if source, shadowed := resourceproposal.ShadowSource(write.ConfigKey, setByFlag(opts, write.ConfigKey)); shadowed {
		return &refusal{
			note: fmt.Sprintf("%s is set by %s; a written config value would not take effect", write.ConfigKey, source),
			err:  "",
		}
	}
	editable, err := configDirEditable(dir)
	if err != nil {
		return &refusal{note: fmt.Sprintf("%s cannot be read for editing: %v", dir, err), err: err.Error()}
	}
	if !editable {
		return &refusal{
			note: fmt.Sprintf("%s holds a non-YAML config file; it cannot be edited format-preservingly", dir),
			err:  viperconfig.ErrUnsupportedConfigFormat.Error(),
		}
	}
	return nil
}

// configDirEditable reports whether the config file a write would edit in dir
// can be edited format-preservingly: a directory with no config file (a new YAML
// file may be created) or one holding a YAML file. A JSON, JSONC, JSON5, or TOML
// file is not editable, so the caller skips rather than corrupt the document.
func configDirEditable(dir string) (bool, error) {
	// A directory with no config file reports no path, whose empty extension
	// configdecode reads as YAML, so the fall-through allows creating one.
	path, _, _, err := configdecode.FindFile(dir)
	if err != nil {
		return false, err
	}
	// FindFile only returns a path whose extension configdecode knows, so the
	// format is always recognised here.
	format, _ := configdecode.FormatFor(path)
	return format == configdecode.FormatYAML, nil
}

// setByFlag reports whether the caller says a flag set key, treating a nil
// predicate as "no flag".
func setByFlag(opts ApplyOptions, key string) bool {
	return opts.SetByFlag != nil && opts.SetByFlag(key)
}

// askApplyOrAcceptAll resolves whether a finding is accepted. AcceptAll (the
// top-level "Save all") skips the per-finding prompt; otherwise the finding is
// put to the user through askApply.
func askApplyOrAcceptAll(opts ApplyOptions, finding Finding) (bool, error) {
	if opts.AcceptAll {
		return true, nil
	}
	return askApply(opts.UI, finding)
}

// askApply puts one finding to the user and reports whether it was accepted.
// Select is not answered by assume-yes, mirroring the project trust gate, and
// its default declines, so an unanswered prompt never writes.
func askApply(ui termio.Prompter, finding Finding) (bool, error) {
	key, err := ui.Select(applyPrompt(finding), []termio.Choice{
		{Key: applyFindingKey, Label: "Apply", Description: "write the proposed change"},
		{Key: skipFindingKey, Label: "Skip", Description: "leave it unchanged"},
	}, skipFindingKey)
	if err != nil {
		return false, fmt.Errorf("ask to apply the finding: %w", err)
	}
	return key == applyFindingKey, nil
}

// applyPrompt renders the one question a finding is accepted through: its
// evidence summary and each write's text, never a secret value.
func applyPrompt(finding Finding) string {
	var lines []string
	if finding.Evidence.Summary != "" {
		lines = append(lines, finding.Evidence.Summary)
	}
	for _, write := range finding.Writes {
		if write.Text != "" {
			lines = append(lines, write.Text)
		}
	}
	lines = append(lines, "Apply this change?")
	return strings.Join(lines, "\n")
}

// executeWrite performs one planned write and returns the directory it wrote to.
func executeWrite(plan plannedWrite, opts ApplyOptions) (string, error) {
	write := plan.write
	if write.Secret != nil {
		return plan.dir, executeSecret(plan)
	}
	if write.ManagedConfig != nil {
		return plan.dir, executeManagedConfig(plan)
	}
	if write.Scope == ScopeProject {
		return plan.dir, executeProjectSetting(plan, opts)
	}
	if write.Remove {
		return plan.dir, viperconfig.RemoveSetting(plan.dir, write.ConfigKey)
	}
	return plan.dir, viperconfig.WriteSetting(plan.dir, write.ConfigKey, write.ConfigValue)
}

// executeManagedConfig writes one managed agent config file atomically, creating
// the agent config directory. The file is world-readable (0o644) because it is
// configuration, not a credential: the secret values in it are placeholders.
func executeManagedConfig(plan plannedWrite) error {
	managed := plan.write.ManagedConfig
	return atomicwrite.WriteFileMode(filepath.Join(plan.dir, managed.Name), managed.Content, 0o644)
}

// executeSecret writes or removes one static secret of a planned write.
func executeSecret(plan plannedWrite) error {
	secret := plan.write.Secret
	if secret.Remove {
		return envsecret.RemoveSecretSpec(plan.dir, secret.EnvVar)
	}
	return envsecret.WriteSecretSpec(plan.dir, secret.EnvVar, envsecret.SecretSpec{
		Value:                 plan.secretValue,
		Host:                  "",
		Hosts:                 secret.Hosts,
		HeaderFields:          secret.HeaderFields,
		Placeholder:           envsecret.PlaceholderFor(secret.EnvVar),
		AllowAnyHostDangerous: false,
	})
}

// executeProjectSetting writes or removes a project-scope config key through the
// project trust gate: the approval is extended before the write, and restored
// when the write fails, so the launcher's own change never needs re-approval.
func executeProjectSetting(plan plannedWrite, opts ApplyOptions) error {
	write := plan.write
	restore, err := projecttrust.ApproveSettingWrite(
		opts.CheckoutSlug,
		write.ConfigKey,
		approvalValue(write.ConfigValue),
	)
	if err != nil {
		return err
	}
	var writeErr error
	if write.Remove {
		writeErr = viperconfig.RemoveSetting(plan.dir, write.ConfigKey)
	} else {
		writeErr = viperconfig.WriteSetting(plan.dir, write.ConfigKey, write.ConfigValue)
	}
	if writeErr != nil {
		restore()
		return writeErr
	}
	return nil
}

// approvalValue renders a config value as the string the project trust gate
// records for a key. A string value is recorded as itself and round-trips to the
// same approval entry the projection computes; any other value is encoded as
// JSON, which still records the key the write adds.
func approvalValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
}
