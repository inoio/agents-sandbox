package config

import _ "embed"

//go:embed data/provider-config.json5
var EmbeddedProviderConfig []byte
