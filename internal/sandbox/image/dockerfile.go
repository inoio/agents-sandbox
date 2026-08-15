package image

import (
	"os"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
)

// ResolveDockerfile returns the project Dockerfile bytes, falling back to the
// embedded runner Dockerfile. It is owned by the image module; session must
// not redeclare it.
func ResolveDockerfile() []byte {
	cp := configpaths.Get()
	if data, err := os.ReadFile(cp.ProjectDockerfile()); err == nil {
		return data
	}
	return embeddedDockerfile
}
