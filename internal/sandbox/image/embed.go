package image

import _ "embed"

//go:embed data/Dockerfile
var embeddedDockerfile []byte

//go:embed data/Dockerfile.dind
var embeddedDindDockerfile []byte
