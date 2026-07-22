package config

import _ "embed"

//go:embed data/provider-config.json
var EmbeddedProviderConfig []byte
