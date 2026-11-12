package docker

import (
	"encoding/base64"
	"encoding/json"
	"os"

	"github.com/distribution/reference"
	"github.com/docker/cli/cli/config"
	"github.com/docker/cli/cli/config/configfile"
	clitypes "github.com/docker/cli/cli/config/types"
	"github.com/moby/moby/api/types/registry"
)

// The engine never reads the docker CLI configuration: the docker command
// resolves registry credentials (plain auths, credsStore, credHelpers) and sends
// them with each request. The real client does the same, so a private base image
// pulls and builds as it does with docker. Every lookup failure falls back to
// anonymous access, and the resolved credentials are never logged or stored.

// loadCLIConfig loads the docker CLI configuration from DOCKER_CONFIG or
// ~/.docker. A missing file yields an empty configuration. It is a package var so
// a test never reads the host's configuration.
var loadCLIConfig = func() *configfile.ConfigFile {
	return config.LoadDefaultConfigFile(os.Stderr)
}

// pullRegistryAuth returns the X-Registry-Auth value for the registry imageRef
// names, or "" to pull anonymously.
func pullRegistryAuth(imageRef string) string {
	named, err := reference.ParseNormalizedNamed(imageRef)
	if err != nil {
		return ""
	}
	credentials, err := loadCLIConfig().GetAuthConfig(reference.Domain(named))
	if err != nil || !hasCredentials(credentials) {
		return ""
	}
	return encodeRegistryAuth(registry.AuthConfig(credentials))
}

// buildRegistryAuths returns the credentials of every registry the docker CLI
// knows, keyed by registry, for the classic builder's X-Registry-Config. It is
// nil when there are none or the credential store cannot be read.
func buildRegistryAuths() map[string]registry.AuthConfig {
	all, err := loadCLIConfig().GetAllCredentials()
	if err != nil {
		return nil
	}
	var auths map[string]registry.AuthConfig
	for registryHost, credentials := range all {
		if !hasCredentials(credentials) {
			continue
		}
		if auths == nil {
			auths = make(map[string]registry.AuthConfig)
		}
		auths[registryHost] = registry.AuthConfig(credentials)
	}
	return auths
}

// hasCredentials reports whether a resolved entry carries a secret; the CLI
// returns an empty entry for a registry it has no credentials for.
func hasCredentials(credentials clitypes.AuthConfig) bool {
	return credentials.Username != "" || credentials.Password != "" ||
		credentials.IdentityToken != "" || credentials.RegistryToken != ""
}

// encodeRegistryAuth encodes credentials as the engine's X-Registry-Auth header
// value: base64url-encoded JSON. Marshaling a struct of strings cannot fail, and
// the header exists to carry the credentials.
func encodeRegistryAuth(credentials registry.AuthConfig) string {
	data, _ := json.Marshal(credentials) //nolint:gosec // see the function comment
	return base64.URLEncoding.EncodeToString(data)
}
