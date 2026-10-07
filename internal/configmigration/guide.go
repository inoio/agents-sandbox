package configmigration

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	cp "github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/termio"
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
		return nil
	}
	plan, err := Build(a, hostHome, policy)
	if err != nil {
		if errors.Is(err, ErrUnsupported) {
			return nil
		}
		return err
	}
	if !plan.HasChanges() {
		return nil
	}
	if plan.SetupOnly {
		return guideSetup(a, plan, ui)
	}
	ui.Warnf("native %s configuration was found, but host-config provisioning is disabled", a.Name())
	key, err := ui.Select("Choose how to configure the sandbox", []termio.Choice{
		{
			Key:         "m",
			Label:       "Migrate safely",
			Description: "replace supported credentials with microsandbox placeholders",
		},
		{
			Key:         "p",
			Label:       "Use native host config",
			Description: "enable copying raw host config and credentials into the VM",
		},
		{Key: "d", Label: "Continue without migration", Description: "configure the agent manually later"},
	}, "m")
	if err != nil {
		return err
	}
	switch key {
	case "m":
		return reviewApplyAndConfirmStart(a, plan, ui)
	case "p":
		if path, _, err := migrationLoadLauncherConfig(); err == nil {
			warnIfLauncherConfigLosesComments(path, ui)
		}
		if err := enableNativeProvisioningFn(); err != nil {
			return err
		}
		ui.Warn("native host-config provisioning is now enabled; rerun agents-sandbox to apply it")
		return ErrRerunRequired
	case "d":
		return dismissGuidedMigration(plan, ui)
	default:
		return fmt.Errorf("unknown migration choice %q", key)
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
