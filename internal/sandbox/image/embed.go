package image

import _ "embed"

//go:embed data/Dockerfile
var embeddedBaseToolsBlock []byte

//go:embed data/agents-sandbox-path.sh
var embeddedPathMergeScript []byte

// pathMergeScript returns the profile.d merge script shipped in the build
// context and installed into the image.
func pathMergeScript() []byte { return embeddedPathMergeScript }
