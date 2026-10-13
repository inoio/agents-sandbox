package builder

import (
	"maps"
	"os"

	"github.com/docker/cli/cli/config"
	"github.com/docker/cli/cli/config/configfile"
	"github.com/docker/cli/cli/config/types"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
)

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
func registryAuthConfig(extra map[string]types.AuthConfig) *configfile.ConfigFile {
	configFile := config.LoadDefaultConfigFile(os.Stderr)
	if len(extra) == 0 {
		return configFile
	}
	if configFile.AuthConfigs == nil {
		configFile.AuthConfigs = map[string]types.AuthConfig{}
	}
	maps.Copy(configFile.AuthConfigs, extra)
	return configFile
}
