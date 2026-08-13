package session

import (
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/options"
)

// projectVMName generates the VM name from the project slug.
func projectVMName(slug string) string {
	name := naming.VmPrefix + slug
	if len(name) > options.MaxSandboxNameLen {
		name = name[:options.MaxSandboxNameLen]
	}
	return name
}
