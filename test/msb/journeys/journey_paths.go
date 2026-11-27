package journeys

import (
	"fmt"
	"path/filepath"
	"sync"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
)

// The isolated XDG tree driver.IsolatedEnv roots under a journey's home base:
// the launcher's config lives under config/agents-sandbox, its project state
// under state/agents-sandbox, and its learn artefacts under
// state/agents-sandbox/learn/<checkout-slug>. The names mirror driver.env.go
// and channel's private constants, which the journeys package cannot import.
const (
	isolatedConfigDir     = "config"
	isolatedStateDir      = "state"
	isolatedLearnDirName  = "learn"
	launcherConfigDirName = "agents-sandbox"
)

// journeyStatePaths is the isolated config-path resolver the seeding steps use:
// the real resolver with only the user-state dir redirected under the journey's
// home. state.RecordLearnSession and channel.Save resolve through
// configpaths.Get(), which a test build installs as a fail-fast double and which
// would otherwise write the developer's real XDG state; the redirect keeps the
// production state and artefact formats while confining every write to the
// journey's isolated tree.
type journeyStatePaths struct {
	configpaths.ConfigPaths

	stateDir string
}

// UserStateDir returns the journey's isolated state root.
func (p journeyStatePaths) UserStateDir() string { return p.stateDir }

// journeyStatePathsLock serializes the steps that swap the process-global
// resolver (withJourneyStatePaths): a journey's harnesses run in parallel.
var journeyStatePathsLock sync.Mutex

// withJourneyStatePaths installs the isolated resolver rooted at home and
// returns the restore. A step that calls it holds journeyStatePathsLock for
// the swap's lifetime; nothing else in the suite resolves state in-process.
func withJourneyStatePaths(home string) func() {
	previous := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths {
		return journeyStatePaths{
			ConfigPaths: configpaths.NewRealConfigPaths(),
			stateDir:    filepath.Join(home, isolatedStateDir, launcherConfigDirName),
		}
	}
	return func() { configpaths.Get = previous }
}

// journeyIdentity resolves the checkout identity of the journey's project
// directory, the same way the launcher does, so the slug the scope and state
// paths are keyed by matches the run.
func journeyIdentity(c *Context) (git.Identity, error) {
	dir := c.ProjectDir()
	identity, err := git.ResolveIdentity(dir)
	if err != nil {
		return git.Identity{}, fmt.Errorf("resolve checkout identity for %s: %w", dir, err)
	}
	return identity, nil
}

// journeyScopeDir is the per-slug user config scope the run writes into, keyed
// by the repo slug under the isolated XDG config dir.
func journeyScopeDir(c *Context) (string, error) {
	identity, err := journeyIdentity(c)
	if err != nil {
		return "", err
	}
	return filepath.Join(c.Home, isolatedConfigDir, launcherConfigDirName, identity.RepoSlug), nil
}

// journeyStateFile is the project's state file under the isolated XDG state dir,
// keyed by the checkout slug and agent.
func journeyStateFile(c *Context) (string, error) {
	identity, err := journeyIdentity(c)
	if err != nil {
		return "", err
	}
	return filepath.Join(
		c.Home, isolatedStateDir, launcherConfigDirName, identity.CheckoutSlug, c.Agent, "state.yaml",
	), nil
}
