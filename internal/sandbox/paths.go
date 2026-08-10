package sandbox

import "path/filepath"

const pathPrefix = "opencode-msb"

// ProjectConfigDir is the project-local metadata directory for the tool.
const ProjectConfigDir = "." + pathPrefix

// Shared names for config subdirectories and files, used by both the
// project-local and the user-level config path helpers.
const (
	configDirName     = "opencode"
	envFileName       = "env"
	envSecretFileName = "env.secret"
	dockerfileName    = "Dockerfile"
)

// Project-level filesystem paths, built with filepath.Join to mirror the
// user-level config path handling.
func projectDockerfile() string {
	return filepath.Join(ProjectConfigDir, dockerfileName)
}

// projectOpencodeConfigDir is the project-local opencode config directory.
func projectOpencodeConfigDir() string {
	return filepath.Join(ProjectConfigDir, configDirName)
}

func projectEnvFile() string {
	return filepath.Join(ProjectConfigDir, envFileName)
}

func projectEnvSecretFile() string {
	return filepath.Join(ProjectConfigDir, envSecretFileName)
}

// Mount point constants used by volume operations (prefill, copy, edit).
const (
	srcMount     = "/src"
	dstMount     = "/dst"
	tmpMountPath = "/tmp"
)
