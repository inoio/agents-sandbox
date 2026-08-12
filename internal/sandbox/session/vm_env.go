package session

import (
	"maps"
	"os"
	"strings"
)

const experimentalWorkspacesValue = "true"

func buildProjectVMEnv(envMap map[string]string, imageEnvs map[string]string) {
	// Merge env vars baked into the Docker image (set via Dockerfile ENV directives).
	// These are parsed by docker.ImageInspect at build time and include everything
	// from base images (debian defaults) through custom Dockerfile ENVs.
	maps.Copy(envMap, imageEnvs)
	if _, ok := envMap["PATH"]; !ok {
		// Fallback: if image env does not provide a PATH, inherit from the
		// host. This covers the case where the Dockerfile has no ENV
		// directives OR the image was pruned with no stored metadata.
		for _, e := range os.Environ() {
			if i := strings.Index(e, "="); i > 0 {
				key := e[:i]
				if _, exist := envMap[key]; !exist {
					envMap[key] = e[i+1:]
				}
			}
		}
	}
	envMap["OPENCODE_EXPERIMENTAL_WORKSPACES"] = experimentalWorkspacesValue
}
