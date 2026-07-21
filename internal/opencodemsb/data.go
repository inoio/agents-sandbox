package opencodemsb

import _ "embed"

//go:embed data/Dockerfile
var EmbeddedDockerfile []byte

//go:embed data/provider-config.json
var EmbeddedProviderConfig []byte
