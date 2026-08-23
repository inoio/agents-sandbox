package vm

import (
	"maps"
)

const experimentalWorkspacesValue = "true"

func buildProjectVMEnv(envMap map[string]string, imageEnvs map[string]string) {
	// Merge env vars baked into the Docker image (set via Dockerfile ENV directives).
	// These are parsed by docker.ImageInspect at build time and include everything
	// from base images (debian defaults) through custom Dockerfile ENVs.
	maps.Copy(envMap, imageEnvs)
	// enable the experimental workspaces feature for --workspace support
	envMap["OPENCODE_EXPERIMENTAL_WORKSPACES"] = experimentalWorkspacesValue
}
