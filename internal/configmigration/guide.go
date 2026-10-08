package configmigration

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	cp "github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/termio"
)

const (
	guideMigrationChoice = "m"
	guideNativeChoice    = "p"
	guideDismissChoice   = "d"
	guideHostFirstChoice = "host-first"
	guideManualChoice    = "manual"
	guideStartChoice     = "start"
	guideLoginChoice     = "login"
	guideHostFirstLabel  = "Set up on the host"
	guideManualLabel     = "Configure manually"
)

func Guide(
	a agent.Agent,
	hostHome string,
	policy network.Policy,
	provisionHostConfig bool,
	ui termio.UI,
) error {
	if provisionHostConfig {
		return nil
	}
	if !ui.IsInteractive() {
		if !warnLoginPending(a, ui) {
			warnUnmigratedNativeConfig(a, hostHome, ui)
		}
		return nil
	}
	plan, err := Build(a, hostHome, policy)
	if err != nil {
		if errors.Is(err, ErrUnsupported) {
			return nil
		}
		return err
	}
	if !plan.HasNativeConfig && !plan.HasManagedConfig && !plan.SetupOnly && !plan.Manual {
		return guideHostFirst(a, plan, ui)
	}
	if plan.LoginPending {
		return guideLoginVerification(a, plan, ui)
	}
	if plan.HasManagedConfig || plan.Dismissed || plan.Handled || plan.Manual {
		return nil
	}
	if !plan.NeedsAttention() {
		return nil
	}
	if plan.SetupOnly {
		return guideSetup(a, plan, ui)
	}
	if plan.HasLogin() && len(plan.Files) == 0 {
		return guideLoginHandoff(a, plan, ui)
	}
	if len(plan.Files) == 0 {
		return guideReviewHandoff(plan, ui)
	}
	return guideMigrationSelect(a, plan, ui)
}

func warnLoginPending(a agent.Agent, ui termio.UI) bool {
	if migrationManifestFor(a).State != migrationStateLoginPending {
		return false
	}
	ui.Warnf(
		"%s login is still pending; start agents-sandbox interactively to confirm it succeeded",
		a.Name(),
	)
	return true
}

func warnUnmigratedNativeConfig(a agent.Agent, hostHome string, ui termio.UI) {
	provider, supported := agent.AsMigrationSpecProvider(a)
	managed := false
	if supported {
		managed = hasManagedConfig(a, provider.MigrationSpec())
	}
	if nativeConfigExists(a, hostHome) && !managed {
		ui.Warnf(
			"native %s configuration was found but was not migrated; run 'agents-sandbox config migrate' interactively",
			a.Name(),
		)
	}
}

func guideMigrationSelect(a agent.Agent, plan *Plan, ui termio.UI) error {
	ui.Warnf("native %s configuration was found, but host-config provisioning is disabled", a.Name())
	key, err := ui.Select("Choose how to configure the sandbox", []termio.Choice{
		{
			Key:         guideMigrationChoice,
			Label:       "Migrate safely",
			Description: "replace supported credentials with microsandbox placeholders",
		},
		{
			Key:         guideNativeChoice,
			Label:       "Use native host config",
			Description: "enable copying raw host config and credentials into the VM",
		},
		{
			Key:         guideDismissChoice,
			Label:       "Continue without migration",
			Description: "configure the agent manually later",
		},
	}, guideMigrationChoice)
	if err != nil {
		return err
	}
	switch key {
	case guideMigrationChoice:
		return reviewApplyAndConfirmStart(a, plan, ui)
	case guideNativeChoice:
		if path, _, err := migrationLoadLauncherConfig(); err == nil {
			warnIfLauncherConfigLosesComments(path, ui)
		}
		if err := enableNativeProvisioningFn(); err != nil {
			return err
		}
		ui.Warn("native host-config provisioning is now enabled; rerun agents-sandbox to apply it")
		return ErrRerunRequired
	case guideDismissChoice:
		return dismissGuidedMigration(plan, ui)
	default:
		return fmt.Errorf("unknown migration choice %q", key)
	}
}

// guideHostFirst guides a user who has no native configuration yet: set the
// agent up on the host, then rerun so agents-sandbox can migrate it.
func guideHostFirst(a agent.Agent, plan *Plan, ui termio.UI) error {
	ui.Infof("no native %s configuration was found", a.Name())
	ui.Infof(
		"set up %s on the host, then rerun agents-sandbox; it will migrate the configuration without copying credentials",
		a.Name(),
	)
	key, err := ui.Select("Choose how to continue", []termio.Choice{
		{
			Key:         guideHostFirstChoice,
			Label:       guideHostFirstLabel,
			Description: "exit and rerun after configuring the agent on the host",
		},
		{
			Key:         guideManualChoice,
			Label:       guideManualLabel,
			Description: "start the sandbox and configure the agent yourself",
		},
		{
			Key:         guideStartChoice,
			Label:       "Start anyway",
			Description: "start the sandbox with the current configuration",
		},
	}, guideHostFirstChoice)
	if err != nil {
		return err
	}
	switch key {
	case guideHostFirstChoice:
		return ErrStartDeferred
	case guideManualChoice, guideStartChoice:
		return persistManual(plan, ui)
	default:
		return fmt.Errorf("unknown host-first choice %q", key)
	}
}

// guideReviewHandoff surfaces everything a plan found but could not migrate,
// then offers explicit host-first, manual, or start-anyway outcomes.
func guideReviewHandoff(plan *Plan, ui termio.UI) error {
	ui.Warnf("native %s configuration was found, but nothing could be migrated automatically", plan.AgentName)
	surfacePlanWarnings(plan, ui)
	key, err := ui.Select("Choose how to configure the sandbox", []termio.Choice{
		{
			Key:         guideHostFirstChoice,
			Label:       guideHostFirstLabel,
			Description: "configure the agent on the host and rerun agents-sandbox to migrate it",
		},
		{
			Key:         guideManualChoice,
			Label:       guideManualLabel,
			Description: "start the sandbox and add managed configuration yourself later",
		},
		{
			Key:         guideStartChoice,
			Label:       "Start anyway",
			Description: "start the sandbox with the current configuration",
		},
	}, guideHostFirstChoice)
	if err != nil {
		return err
	}
	switch key {
	case guideHostFirstChoice:
		return ErrStartDeferred
	case guideManualChoice, guideStartChoice:
		return persistManual(plan, ui)
	default:
		return fmt.Errorf("unknown review choice %q", key)
	}
}

// guideLoginHandoff explains that refreshable logins are never copied and
// offers to allow the login hosts so the user can authenticate in the sandbox.
func guideLoginHandoff(a agent.Agent, plan *Plan, ui termio.UI) error {
	ui.Warnf(
		"native %s credentials for %s are refreshable logins; they are not copied into the sandbox",
		a.Name(),
		strings.Join(plan.LoginProviders, ", "),
	)
	surfacePlanWarnings(plan, ui)
	key, err := ui.Select("Choose how to continue with the required in-sandbox login", []termio.Choice{
		{
			Key:         guideLoginChoice,
			Label:       "Log in inside the sandbox",
			Description: "allow the login hosts and authenticate after the sandbox starts",
		},
		{
			Key:         guideHostFirstChoice,
			Label:       guideHostFirstLabel,
			Description: "exit and rerun after configuring an API key on the host",
		},
		{
			Key:         guideManualChoice,
			Label:       guideManualLabel,
			Description: "configure the agent yourself later",
		},
	}, guideLoginChoice)
	if err != nil {
		return err
	}
	switch key {
	case guideLoginChoice:
		if err := Apply(plan, ui); err != nil {
			return err
		}
		ui.Infof("%s login hosts were allowed; authenticate inside the sandbox once it starts", a.Name())
		return nil
	case guideHostFirstChoice:
		return ErrStartDeferred
	case guideManualChoice:
		return persistManual(plan, ui)
	default:
		return fmt.Errorf("unknown login choice %q", key)
	}
}

func loginProviderNames(logins []LoginProvider) string {
	names := make([]string, 0, len(logins))
	for _, provider := range logins {
		names = append(names, provider.Name)
	}
	return strings.Join(names, ", ")
}

// guideLoginVerification closes the login-pending loop: it asks whether the
// in-sandbox login worked and either completes onboarding, re-enters the host
// hand-off, or defers the reminder to a later run.
func guideLoginVerification(a agent.Agent, plan *Plan, ui termio.UI) error {
	names := loginProviderNames(plan.PendingLogins)
	key, err := ui.Select(
		fmt.Sprintf("Did the in-sandbox login for %s succeed?", names),
		[]termio.Choice{
			{Key: "yes", Label: "Yes", Description: "mark onboarding complete"},
			{Key: "no", Label: "No", Description: "review the host configuration again"},
			{Key: "later", Label: "Not yet", Description: "remind me next time"},
		},
		"yes",
	)
	if err != nil {
		return err
	}
	switch key {
	case "yes":
		ui.Infof("%s login confirmed", a.Name())
		return Complete(plan)
	case "no":
		return guideLoginHandoff(a, plan, ui)
	case "later":
		ui.Infof("%s login is still pending; agents-sandbox will ask again next time", a.Name())
		return nil
	default:
		return fmt.Errorf("unknown login verification choice %q", key)
	}
}

func surfacePlanWarnings(plan *Plan, ui termio.UI) {
	for _, warning := range plan.Warnings {
		ui.Warn(warning)
	}
	for _, warning := range plan.ReviewWarnings {
		if !slices.Contains(plan.Warnings, warning) {
			ui.Warn(warning)
		}
	}
}

// guideSetup offers the setup of an agent without native host config, e.g.
// the VM egress a fresh Claude Code user needs to log in inside the sandbox.
func guideSetup(a agent.Agent, plan *Plan, ui termio.UI) error {
	ui.Infof("no native %s configuration was found; the sandbox needs a one-time setup", a.Name())
	key, err := ui.Select("Set up the sandbox now?", []termio.Choice{
		{
			Key:         guideSetupChoice,
			Label:       "Set up now",
			Description: "choose how to authenticate and allow the required VM egress",
		},
		{Key: "d", Label: "Continue without setup", Description: "configure the agent manually later"},
	}, guideSetupChoice)
	if err != nil {
		return err
	}
	switch key {
	case guideSetupChoice:
		return reviewApplyAndConfirmStart(a, plan, ui)
	case "d":
		return dismissGuidedMigration(plan, ui)
	default:
		return fmt.Errorf("unknown setup choice %q", key)
	}
}

func reviewApplyAndConfirmStart(a agent.Agent, plan *Plan, ui termio.UI) error {
	if err := Review(plan, ui); err != nil {
		return err
	}
	if err := Apply(plan, ui); err != nil {
		return err
	}
	ui.Infof("safe %s configuration migration completed", a.Name())
	ui.Info("")
	choice, err := ui.Select(
		"Start agents-sandbox now? The files are already written and can be reviewed or edited either way.",
		[]termio.Choice{
			{Key: "y", Label: "Start now", Description: "start the sandbox with the generated configuration"},
			{
				Key:         "n",
				Label:       "Quit",
				Description: "exit (and keep generated files)",
			},
		},
		"y",
	)
	if err != nil {
		return err
	}
	if choice == "n" {
		return ErrStartDeferred
	}
	if choice != "y" {
		return fmt.Errorf("unknown start choice %q", choice)
	}
	return nil
}

func dismissGuidedMigration(plan *Plan, ui termio.UI) error {
	if err := dismissMigrationFn(plan); err != nil {
		return err
	}
	ui.Info("continuing without migrated agent configuration")
	return nil
}

func persistManual(plan *Plan, ui termio.UI) error {
	if err := RecordManual(plan); err != nil {
		return err
	}
	ui.Info("continuing with manual configuration")
	return nil
}

func nativeConfigExists(a agent.Agent, hostHome string) bool {
	provider, ok := agent.AsMigrationSpecProvider(a)
	if !ok {
		return false
	}
	spec := provider.MigrationSpec()
	configDir, authPath := nativeAgentPaths(hostHome, spec)
	pathsToCheck := append([]string{}, spec.NativeConfigFiles...)
	pathsToCheck = append(pathsToCheck, spec.NativeSupplementalFiles...)
	for _, name := range pathsToCheck {
		if _, err := os.Stat(filepath.Join(configDir, name)); err == nil {
			return true
		}
	}
	if spec.NativeCredential == "" {
		return false
	}
	_, err := os.Stat(authPath)
	return err == nil
}

type nativeFile struct {
	Path string
	Data []byte
}

func collectNativeFiles(hostHome string, spec agent.ConfigMigrationSpec) ([]nativeFile, error) {
	var files []nativeFile
	configDir, authPath := nativeAgentPaths(hostHome, spec)
	paths := make([]string, 0, len(spec.NativeConfigFiles)+len(spec.NativeSupplementalFiles)+1)
	for _, name := range append(spec.NativeConfigFiles, spec.NativeSupplementalFiles...) {
		paths = append(paths, filepath.Join(configDir, name))
	}
	if spec.NativeCredential != "" {
		paths = append(paths, authPath)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("inspect native config %s: %w", path, err)
		}
		files = append(files, nativeFile{Path: path, Data: data})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func nativeAgentPaths(hostHome string, spec agent.ConfigMigrationSpec) (string, string) {
	configDir := filepath.Join(hostHome, spec.NativeConfigDir)
	if spec.NativeConfigEnv != "" {
		if value := os.Getenv(spec.NativeConfigEnv); value != "" {
			configDir = migrationEnvPath(hostHome, value, spec.NativeConfigEnvIsPath, spec.NativeConfigSubdir)
		} else if !spec.NativeConfigEnvIsPath {
			configDir = filepath.Join(hostHome, filepath.Dir(spec.NativeConfigDir), spec.NativeConfigSubdir)
		}
	}
	if spec.NativeCredential == "" {
		return configDir, ""
	}

	credentialDir := filepath.Join(hostHome, filepath.Dir(spec.NativeCredential))
	if spec.NativeDataEnv != "" {
		if value := os.Getenv(spec.NativeDataEnv); value != "" {
			credentialDir = migrationEnvPath(hostHome, value, spec.NativeDataEnvIsPath, spec.NativeDataSubdir)
		} else if !spec.NativeDataEnvIsPath {
			credentialDir = filepath.Join(
				hostHome,
				filepath.Dir(filepath.Dir(spec.NativeCredential)),
				spec.NativeDataSubdir,
			)
		}
	}
	return configDir, filepath.Join(credentialDir, filepath.Base(spec.NativeCredential))
}

func migrationEnvPath(hostHome, value string, isPath bool, subdir string) string {
	value = strings.TrimPrefix(strings.TrimSpace(value), "~/")
	if !filepath.IsAbs(value) {
		value = filepath.Join(hostHome, value)
	}
	if isPath {
		return value
	}
	return filepath.Join(value, subdir)
}

func hasManagedConfig(a agent.Agent, spec agent.ConfigMigrationSpec) bool {
	_, ok := agent.AsConfigMerger(a)
	if !ok {
		return false
	}
	for _, dir := range []string{cp.Get().UserAgentConfigDir(a), cp.Get().ProjectAgentConfigDir(a)} {
		for _, name := range append(
			[]string{spec.ManagedCredential, spec.ManagedSnippet},
			spec.ManagedSupplementalFiles...,
		) {
			if name == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return true
			}
		}
	}
	return false
}

func hashNativeFiles(files []nativeFile) string {
	h := sha256.New()
	for _, file := range files {
		_, _ = h.Write([]byte(file.Path))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(file.Data)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
