package vm

import "maps"

// buildProjectVMEnv merges the env vars baked into the Docker image (set via
// Dockerfile ENV directives, including each agent's ImageSpec AgentEnv) into
// the project VM environment. These are parsed by docker.ImageInspect at build
// time and include everything from base images (debian defaults) through custom
// Dockerfile ENVs.
func buildProjectVMEnv(envMap map[string]string, imageEnvs map[string]string) {
	maps.Copy(envMap, imageEnvs)
}
