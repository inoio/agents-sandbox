package builder

import (
	"fmt"
	"maps"
	"os"

	"github.com/docker/cli/cli/config"
	"github.com/docker/cli/cli/config/configfile"
	"github.com/docker/cli/cli/config/credentials"
	"github.com/docker/cli/cli/config/types"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
)

// dockerConfigDir resolves the Docker config directory. It is a seam so tests
// can point at a temp config without the process-global config.Dir() memoization.
var dockerConfigDir = config.Dir

// registryAuthSession supplies registry credentials from the host's default
// Docker config to the BuildKit session, merged with any extra per-host
// credentials. Bearer-only entries set RegistryToken, which BuildKit presents
// directly; the embedded cache registry's token is passed here.
func registryAuthSession(extra map[string]types.AuthConfig) []session.Attachable {
	return []session.Attachable{
		authprovider.NewDockerAuthProvider(authprovider.DockerAuthProviderConfig{
			ConfigFile:       registryAuthConfig(extra),
			TLSConfigs:       nil,
			ExpireCachedAuth: nil,
		}),
	}
}

// registryAuthConfig loads the host's default Docker config and merges the extra
// per-host credentials into it.
//
// A global credsStore or a per-host credHelper makes configfile.GetAuthConfig
// consult the credential helper instead of the in-memory AuthConfigs, which
// would silently drop an extra entry. Pin an empty helper for each extra host so
// it resolves from AuthConfigs, without touching the user's global credential
// configuration (those entries are added to the loaded in-memory copy only).
func registryAuthConfig(extra map[string]types.AuthConfig) *configfile.ConfigFile {
	configFile := loadDockerConfig()
	if len(extra) == 0 {
		return configFile
	}
	if configFile.AuthConfigs == nil {
		configFile.AuthConfigs = map[string]types.AuthConfig{}
	}
	maps.Copy(configFile.AuthConfigs, extra)
	if configFile.CredentialHelpers == nil {
		configFile.CredentialHelpers = map[string]string{}
	}
	for host := range extra {
		configFile.CredentialHelpers[host] = ""
	}
	return configFile
}

// loadDockerConfig loads the Docker config from dockerConfigDir(), mirroring
// config.LoadDefaultConfigFile: a missing file yields a default config, a
// malformed one warns on stderr, and a config with no credentials-store set
// falls back to the platform default.
func loadDockerConfig() *configfile.ConfigFile {
	configFile, err := config.Load(dockerConfigDir())
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "WARNING: Error", err)
	}
	if !configFile.ContainsAuth() {
		configFile.CredentialsStore = credentials.DetectDefaultStore(configFile.CredentialsStore)
	}
	return configFile
}
