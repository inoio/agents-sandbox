package sandbox

import _ "embed"

//go:embed data/Dockerfile
var EmbeddedDockerfile []byte
