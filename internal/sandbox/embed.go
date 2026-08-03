package sandbox

import _ "embed"

//go:embed data/Dockerfile
var EmbeddedDockerfile []byte

//go:embed data/Dockerfile.dind
var EmbeddedDindDockerfile []byte
