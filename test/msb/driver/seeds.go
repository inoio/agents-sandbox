package driver

import "maps"

// Seeds are the files a harness run on an isolated home starts from, by the
// route each takes into the VM. The startup smoke seeds a harness's Spec.Seeds;
// a journey seeds its manifest and snippets, with its adapter's stub seeds on
// top (Merge), but not its host files, which the first-run menu would read as
// the journey's host setup.
type Seeds struct {
	// HomeFiles maps a home-relative path to its content, written under the
	// isolated HOME so the launcher provisions it into the VM as host config
	// (the agent's ProvisionRules). It is how a harness that needs host-side
	// configuration (a provider block, a settled opt-in) starts headful
	// without credentials; the first-run menu reads it as host setup.
	HomeFiles map[string]string
	// HomeManifest maps a VM-home-relative target path to its content,
	// provisioned through the launcher config's `home:` key rather than the
	// agent's ProvisionRules: first-run state outside the paths the rules copy
	// (claude-code's ~/.claude.json), or files a journey must not present as
	// host setup.
	HomeManifest map[string]string
	// AgentSnippets maps a snippet file name to its content, written into the
	// agent's launcher config dir so the launcher merges it into the agent's
	// config file, which neither route above may write. Like any managed layer
	// it makes the first-run menu stay away.
	AgentSnippets map[string]string
}

// Merge returns s with overlay's entries added, overlay winning on a path both
// name.
func (s Seeds) Merge(overlay Seeds) Seeds {
	return Seeds{
		HomeFiles:     mergedFiles(s.HomeFiles, overlay.HomeFiles),
		HomeManifest:  mergedFiles(s.HomeManifest, overlay.HomeManifest),
		AgentSnippets: mergedFiles(s.AgentSnippets, overlay.AgentSnippets),
	}
}

// Apply writes the seeds into homeBase: the host files, the launcher config
// with its `home:` manifest (always, as SeedLauncherConfig opts into host
// provisioning), and the snippets under agentConfigDirName.
func (s Seeds) Apply(homeBase, agentConfigDirName string) error {
	if err := SeedHomeFiles(homeBase, s.HomeFiles); err != nil {
		return err
	}
	if err := SeedLauncherConfig(homeBase, s.HomeManifest); err != nil {
		return err
	}
	return SeedAgentSnippets(homeBase, agentConfigDirName, s.AgentSnippets)
}

func mergedFiles(base, overlay map[string]string) map[string]string {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	merged := maps.Clone(base)
	if merged == nil {
		merged = map[string]string{}
	}
	maps.Copy(merged, overlay)
	return merged
}
