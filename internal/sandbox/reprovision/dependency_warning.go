package reprovision

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	cp "github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/docslink"
	"github.com/inoio/agents-sandbox/internal/termio"
)

const nodeModulesDirName = "node_modules"

// dependencyManifestName is the file whose dependencies opencode documents as
// the way to give config-dir plugins external packages.
const dependencyManifestName = "package.json"

// WarnUnprovisionedDependencies warns when a host agent config dir that the
// drop-in copy provisions without its node_modules has a package.json declaring
// dependencies, and nothing else supplies them in the VM. Local plugins that
// import those packages would otherwise fail to load silently. The launcher
// cannot see an in-VM install, so it warns rather than fails; shipping the
// manifest via home: or the mirror counts as handling the install.
func WarnUnprovisionedDependencies(
	a agent.Agent,
	files *ConfigFiles,
	mountTargets []string,
	provisionHostConfig bool,
	ui termio.UI,
) {
	if !provisionHostConfig {
		return
	}
	provisioner, ok := agent.AsProvisioner(a)
	if !ok {
		return
	}
	hostHome, err := cp.Get().UserHomeDir()
	if err != nil {
		return
	}
	manifests := unprovisionedDependencyManifests(
		provisioner.ProvisionRules(), hostHome, suppliedVMPaths(files), mountTargets,
	)
	for _, manifest := range manifests {
		ui.Warnf(
			"%s declares dependencies, but node_modules is not copied into the VM (host packages are not portable to "+
				"the Linux VM), so local plugins and tools importing them fail to load there. Install them inside the VM "+
				"and declare every npm import in package.json: %s (continuing)",
			manifest,
			docslink.SectionURL("recipes/opencode-plugin-dependencies", "install-the-dependencies-in-the-vm"),
		)
	}
}

// suppliedVMPaths lists the VM paths that home: entries and the verbatim
// mirror write, the sources besides mounts that can provide a manifest or
// node_modules in the VM.
func suppliedVMPaths(files *ConfigFiles) []string {
	paths := make([]string, 0, len(files.HomeFiles)+len(files.Mirror))
	for path := range files.HomeFiles {
		paths = append(paths, path)
	}
	for path := range files.Mirror {
		paths = append(paths, path)
	}
	return paths
}

// unprovisionedDependencyManifests returns the host package.json of every
// provision rule dir that excludes node_modules from the copy, declares
// dependencies, and gets neither its manifest nor a node_modules on its
// resolution path from suppliedPaths or mountTargets.
func unprovisionedDependencyManifests(
	rules []agent.ProvisionRule,
	hostHome string,
	suppliedPaths, mountTargets []string,
) []string {
	var manifests []string
	for _, rule := range rules {
		if rule.Dir == "" || agent.SelectProvisionRule(rule, nodeModulesDirName, true) {
			continue
		}
		hostManifest := filepath.Join(hostHome, rule.Dir, dependencyManifestName)
		if !declaresDependencies(hostManifest) {
			continue
		}
		vmDir := filepath.Join(VMHomeDir, rule.Dir)
		if dependenciesSupplied(vmDir, suppliedPaths, mountTargets) {
			continue
		}
		manifests = append(manifests, hostManifest)
	}
	return manifests
}

// declaresDependencies reports whether the manifest at path has a non-empty
// dependencies map. A missing or unparsable manifest declares none.
func declaresDependencies(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var manifest struct {
		Dependencies map[string]json.RawMessage `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return false
	}
	return len(manifest.Dependencies) > 0
}

// dependenciesSupplied reports whether something other than the drop-in copy
// provides vmDir's dependencies: a supplied manifest in vmDir (the user
// installs from it in the VM), a supplied file inside a node_modules dir that
// module resolution from vmDir reaches, or a mount covering one of those dirs.
func dependenciesSupplied(vmDir string, suppliedPaths, mountTargets []string) bool {
	vmManifest := filepath.Join(vmDir, dependencyManifestName)
	resolutionDirs := nodeModulesResolutionDirs(vmDir)
	for _, path := range suppliedPaths {
		if path == vmManifest {
			return true
		}
		for _, dir := range resolutionDirs {
			if isWithin(path, dir) {
				return true
			}
		}
	}
	for _, target := range mountTargets {
		for _, dir := range resolutionDirs {
			if target == dir || isWithin(dir, target) {
				return true
			}
		}
	}
	return false
}

// nodeModulesResolutionDirs returns the node_modules dirs a bare import from
// a file in dir resolves through, nearest first, up to /node_modules.
func nodeModulesResolutionDirs(dir string) []string {
	var dirs []string
	for {
		dirs = append(dirs, filepath.Join(dir, nodeModulesDirName))
		parent := filepath.Dir(dir)
		if parent == dir {
			return dirs
		}
		dir = parent
	}
}

// isWithin reports whether path lies strictly inside dir.
func isWithin(path, dir string) bool {
	return strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/")
}
