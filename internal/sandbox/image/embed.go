package image

import _ "embed"

//go:embed data/Dockerfile
var embeddedBaseToolsBlock []byte
