package vm

import "maps"

// buildProjectVMEnv returns the project VM environment: the host env merged
// with the env vars baked into the Docker image (set via Dockerfile ENV
// directives, including each agent's ImageSpec AgentEnv), with the image
// winning on collisions. These are parsed by docker.ImageInspect at build time
// and include everything from base images (debian defaults) through custom
// Dockerfile ENVs.
//
// The host env is not modified: it is the value loaded once per run and shared
// with the config fingerprints and the reconfig change detection.
func buildProjectVMEnv(hostEnv map[string]string, imageEnvs map[string]string) map[string]string {
	env := make(map[string]string)
	maps.Copy(env, hostEnv)
	maps.Copy(env, imageEnvs)
	return env
}
