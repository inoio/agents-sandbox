package firstrun

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// provisionProfile is a test agent with a host provision rule set.
type provisionProfile struct {
	rules []agent.ProvisionRule
}

func (provisionProfile) Name() string          { return "provision-stub" }
func (provisionProfile) ConfigDirName() string { return "provision-stub" }
func (provisionProfile) Experimental() bool    { return false }

func (provisionProfile) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

func (p provisionProfile) ProvisionRules() []agent.ProvisionRule { return p.rules }

// plainProfile is a test agent that declares no host provision rules.
type plainProfile struct{}

func (plainProfile) Name() string               { return "plain-stub" }
func (plainProfile) ConfigDirName() string      { return "plain-stub" }
func (plainProfile) Experimental() bool         { return false }
func (plainProfile) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

// TestHostFilesFindsProvisionedConfig pins that the probe returns the host-side
// paths of every file the agent's provision rule selects, sorted, with a
// non-empty source hash.
func TestHostFilesFindsProvisionedConfig(t *testing.T) {
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".config/widget/a.json", `{"a":1}`)
	writeHostFile(t, hostHome, ".config/widget/nested/b.json", `{"b":2}`)

	a := provisionProfile{rules: []agent.ProvisionRule{
		{Dir: ".config/widget", Patterns: []string{"**"}},
	}}

	files, sourceHash := HostFiles(a, hostHome)

	want := []string{
		filepath.Join(hostHome, ".config/widget/a.json"),
		filepath.Join(hostHome, ".config/widget/nested/b.json"),
	}
	if !slices.Equal(files, want) {
		t.Errorf("HostFiles = %v, want %v", files, want)
	}
	if sourceHash == "" {
		t.Error("HostFiles returned an empty source hash for non-empty host config")
	}
}

// TestHostFilesEmptyForAnAgentWithoutHostSetup pins that an agent declaring no
// provision rules (or none at all) yields no files and no hash, so the caller
// omits "learn from my host setup".
func TestHostFilesEmptyForAnAgentWithoutHostSetup(t *testing.T) {
	files, sourceHash := HostFiles(plainProfile{}, t.TempDir())
	if len(files) != 0 {
		t.Errorf("HostFiles = %v, want no files for an agent without host setup", files)
	}
	if sourceHash != "" {
		t.Errorf("sourceHash = %q, want empty for an agent without host setup", sourceHash)
	}
}

// TestHostSourceHashChangesWithContent pins that editing a host config file
// changes the source hash, so a recorded decision re-arms.
func TestHostSourceHashChangesWithContent(t *testing.T) {
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".config/widget/a.json", `{"a":1}`)
	a := provisionProfile{rules: []agent.ProvisionRule{
		{Dir: ".config/widget", Patterns: []string{"**"}},
	}}

	_, before := HostFiles(a, hostHome)
	writeHostFile(t, hostHome, ".config/widget/a.json", `{"a":2}`)
	_, after := HostFiles(a, hostHome)

	if before == after {
		t.Errorf("source hash %q did not change when a host file's content did", before)
	}
}

// TestHostSourceHashIndependentOfHostHome pins that the hash covers the
// home-relative path, not the absolute host path: the same config under two
// different homes hashes the same, so a decision survives a moved home.
func TestHostSourceHashIndependentOfHostHome(t *testing.T) {
	a := provisionProfile{rules: []agent.ProvisionRule{
		{Dir: ".config/widget", Patterns: []string{"**"}},
	}}
	firstHome := t.TempDir()
	secondHome := t.TempDir()
	writeHostFile(t, firstHome, ".config/widget/a.json", `{"a":1}`)
	writeHostFile(t, secondHome, ".config/widget/a.json", `{"a":1}`)

	_, firstHash := HostFiles(a, firstHome)
	_, secondHash := HostFiles(a, secondHome)

	if firstHash != secondHash {
		t.Errorf("source hash depends on the host home: %q != %q", firstHash, secondHash)
	}
}
