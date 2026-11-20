// Package firstrun owns the first-run learn-mode decision: whether to offer the
// first-run menu, what the agent's host config looks like, and — in later work —
// the menu itself and the UC4 host-config seed.
package firstrun

import (
	"fmt"
	"os"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// Choice is the first-run path the user picks (the hub's vocabulary).
type Choice string

const (
	// ChoiceLearnByUsing starts a learning session with no host seed (UC1).
	ChoiceLearnByUsing Choice = "learn-by-using"
	// ChoiceLearnFromHost seeds the per-slug scope from the host config first (UC4).
	ChoiceLearnFromHost Choice = "learn-from-host"
	// ChoiceManual configures it by hand, opting into raw host provisioning.
	ChoiceManual Choice = "configure-it-myself"
)

// Facts is what the decision needs about this checkout and agent. Facts holds no
// credential value: HostFiles lists paths and HostSourceHash hashes their
// contents, so nothing secret crosses this boundary.
type Facts struct {
	Agent string
	// Slug is the repo slug: the per-slug scope and the decision key.
	Slug string
	// ManagedConfig is true when a managed config layer already exists for the
	// agent, so the first run has nothing to offer.
	ManagedConfig bool
	// HostFiles are the agent's host config files the seed would read; empty
	// means the host has no setup for the agent.
	HostFiles []string
	// HostSourceHash hashes HostFiles' home-relative paths and contents; empty
	// when HostFiles is empty.
	HostSourceHash string
	Prior          state.FirstRunRecord
}

// Offer reports whether the first-run menu should be shown: no managed config,
// and either no decision or one whose source hash no longer matches. A decided
// run whose host config is unchanged stays quiet; a host-config change re-arms
// it exactly once (the next decision records the new hash). An undecided run has
// an empty Prior.Choice.
func Offer(f Facts) bool {
	if f.ManagedConfig {
		return false
	}
	if f.Prior.Choice == "" {
		return true
	}
	return f.Prior.SourceHash != f.HostSourceHash
}

// BuildFacts assembles the facts the first-run decision needs for agentName in
// slug: whether a managed config layer already exists, the agent's host config
// files and their source hash, and any prior decision. hostHome is the host
// user's home the provision rules read from; paths resolves the per-slug user
// scope. An agent name the registry does not know is an error: the caller
// resolves the agent before offering anything.
func BuildFacts(agentName, slug, hostHome string, paths configpaths.ConfigPaths) (Facts, error) {
	a, ok := agent.Lookup(agentName)
	if !ok {
		return Facts{}, fmt.Errorf("firstrun: unknown agent %q", agentName)
	}
	managedConfig, err := managedConfigPresent(a, slug, paths)
	if err != nil {
		return Facts{}, err
	}
	hostFiles, hostSourceHash := HostFiles(a, hostHome)
	prior, _, err := state.ReadFirstRun(slug, agentName)
	if err != nil {
		return Facts{}, fmt.Errorf("firstrun: read prior decision: %w", err)
	}
	return Facts{
		Agent:          agentName,
		Slug:           slug,
		ManagedConfig:  managedConfig,
		HostFiles:      hostFiles,
		HostSourceHash: hostSourceHash,
		Prior:          prior,
	}, nil
}

// managedConfigPresent reports whether a managed config layer already exists for
// a: at least one regular file directly in one of the agent's launcher config
// dirs (primary or alias), or a scope's env.secret.yaml, in any scope the config
// merge reads (generic user, per-slug user, project). An empty slug has no
// per-slug scope and no decision key, so it reports false. A missing dir, or one
// holding only subdirectories, is not a managed config layer.
func managedConfigPresent(a agent.Agent, slug string, paths configpaths.ConfigPaths) (bool, error) {
	if slug == "" {
		return false, nil
	}
	dirNames, err := agent.ConfigDirNames(a)
	if err != nil {
		return false, fmt.Errorf("firstrun: agent %s config dir names: %w", a.Name(), err)
	}
	for _, scope := range configpaths.ConfigScopes(paths, slug) {
		for _, dirName := range dirNames {
			if dirHasRegularFile(scope.AgentConfigDir(dirName)) {
				return true, nil
			}
		}
		if fileExists(scope.EnvSecretYAMLFile()) {
			return true, nil
		}
	}
	return false, nil
}

// dirHasRegularFile reports whether dir holds at least one regular file at its
// top level. A missing or unreadable dir reports false, so an unreadable config
// dir is honestly not a managed layer.
func dirHasRegularFile(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return true
		}
	}
	return false
}

// fileExists reports whether path exists as a file; any stat error reports
// false.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
