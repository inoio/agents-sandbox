package image

import _ "embed"

//go:embed data/Dockerfile
var embeddedBaseToolsBlock []byte

//go:embed data/agents-sandbox-path.sh
var embeddedPathMergeScript []byte

//go:embed data/msb-trust.sh
var embeddedTrustHelperScript []byte

//go:embed data/msb-trust.LICENSE
var embeddedTrustHelperLicense []byte

//go:embed data/xdg-open.sh
var embeddedHostOpenerShim []byte

// pathMergeScript returns the profile.d merge script shipped in the build
// context and installed into the image.
func pathMergeScript() []byte { return embeddedPathMergeScript }

// trustHelperScript returns the CA-trust helper shipped in the build context and
// installed into the image. The bytes are vendored verbatim from microsandbox;
// data/msb-trust.LICENSE records the upstream version and re-sync command.
func trustHelperScript() []byte { return embeddedTrustHelperScript }

// trustHelperLicense returns the license shipped beside the CA-trust helper.
// The helper is upstream's Apache-2.0 work, so every recipient — the release
// binary that embeds these bytes, and the runner image that installs them — has
// to receive the license copy section 4(a) requires.
func trustHelperLicense() []byte { return embeddedTrustHelperLicense }

// hostOpenerShimScript returns the in-VM xdg-open shim that hands targets to the
// launcher's host opener (internal/opener).
func hostOpenerShimScript() []byte { return embeddedHostOpenerShim }
