package reprovision

import (
	"maps"
	"os"
	"path/filepath"

	config "github.com/inoio/agents-sandbox/internal/configmerge"
)

// mainConfigBaseDir is where, under the VM home, the launcher records the
// content it last wrote to each agent's main config file. It lives on the home
// volume beside the file it describes, so both are reset together.
const mainConfigBaseDir = ".local/state/agents-sandbox/config-base"

// baseFileMode keeps a recorded base private: a drop-in copy can carry keys.
const baseFileMode = 0o600

// mainConfigBasePath returns where the base of the main config at mergedPath is
// recorded, or "" when mergedPath is not under vmHome.
func mainConfigBasePath(vmHome, mergedPath string) string {
	// A failed Rel returns "", which is not local either.
	rel, _ := filepath.Rel(vmHome, mergedPath)
	if !filepath.IsLocal(rel) {
		return ""
	}
	return filepath.Join(vmHome, mainConfigBaseDir, rel)
}

// mainConfigStore reads and writes the main config and its base: the VM through
// the sandbox SDK, or a host directory for Materialize.
type mainConfigStore interface {
	// read returns a file's content, or nil when it does not exist or cannot be
	// read: either way the launcher's content replaces it.
	read(path string) []byte
	// write writes data with mode, or with the store's default for mode 0.
	write(path string, data []byte, mode os.FileMode) error
	remove(path string)
}

// mainConfigContent is the launcher's content for the agent's main config: the
// merged snippets, or without snippets the drop-in copy of the host file, or nil.
func (cf *ConfigFiles) mainConfigContent() []byte {
	if cf.HasSnippets {
		return cf.Merged
	}
	return cf.Provisioned[cf.MergedPath]
}

// filesBesideMainConfig returns files without the main config, which
// reconcileMainConfig writes.
func (cf *ConfigFiles) filesBesideMainConfig(files map[string][]byte) map[string][]byte {
	rest := maps.Clone(files)
	delete(rest, cf.MergedPath)
	return rest
}

// reconcileMainConfig brings the agent's main config file in line with the
// launcher's content (mainConfigContent) without discarding the agent's own
// settings: configmerge.Reconcile against the recorded base decides, and the
// launcher's content becomes the new base. A file that cannot be read or
// decoded is replaced by the launcher's content, as before the merge existed.
func reconcileMainConfig(store mainConfigStore, cf *ConfigFiles) error {
	// gomutants:disable-next-line BRANCH_IF reason="without a merger there is no launcher content and no base path, so the reconciliation would keep and record nothing; the guard only spares the store a read of an empty path"
	if cf.MergedPath == "" {
		return nil
	}
	launcher := cf.mainConfigContent()
	outcome := decideMainConfig(store, cf, launcher)
	switch outcome.Action {
	case config.ReconcileWrite:
		if err := store.write(cf.MergedPath, outcome.Content, cf.Modes[cf.MergedPath]); err != nil {
			return err
		}
	case config.ReconcileRemove:
		store.remove(cf.MergedPath)
	case config.ReconcileKeep:
	}
	return recordMainConfigBase(store, cf.MergedBasePath, launcher)
}

// decideMainConfig reads the live file and the base and returns what to do with
// the main config.
func decideMainConfig(store mainConfigStore, cf *ConfigFiles, launcher []byte) config.Reconciliation {
	var base []byte
	if cf.MergedBasePath != "" {
		base = store.read(cf.MergedBasePath)
	}
	outcome, err := config.Reconcile(cf.MergedPath, base, store.read(cf.MergedPath), launcher)
	if err != nil {
		if len(launcher) == 0 {
			return config.Reconciliation{Action: config.ReconcileKeep, Content: nil}
		}
		return config.Reconciliation{Action: config.ReconcileWrite, Content: launcher}
	}
	return outcome
}

// recordMainConfigBase records the launcher's content as the base of the next
// reconciliation, or removes the base when the launcher has none.
func recordMainConfigBase(store mainConfigStore, basePath string, launcher []byte) error {
	if basePath == "" {
		return nil
	}
	if len(launcher) == 0 {
		store.remove(basePath)
		return nil
	}
	return store.write(basePath, launcher, baseFileMode)
}
