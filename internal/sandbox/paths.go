package sandbox

const pathPrefix = "opencode-msb"

// ProjectDir is the project-local metadata directory for the tool.
const ProjectDir = "." + pathPrefix

// Project-level filesystem paths.
const (
	projDockerfile    = ProjectDir + "/Dockerfile"
	projConfigDir     = ProjectDir + "/opencode"
	projEnvFile       = ProjectDir + "/env"
	projEnvSecretFile = ProjectDir + "/env.secret"
)

// Mount point constants used by volume operations (prefill, copy, edit).
const (
	srcMount = "/src"
	dstMount = "/dst"
)
