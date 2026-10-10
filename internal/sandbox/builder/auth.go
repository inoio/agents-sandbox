package builder

import (
	"os"

	"github.com/docker/cli/cli/config"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
)

// registryAuthSession supplies registry credentials from the host's default
// Docker config to the BuildKit session.
func registryAuthSession() []session.Attachable {
	return []session.Attachable{
		authprovider.NewDockerAuthProvider(authprovider.DockerAuthProviderConfig{
			ConfigFile:       config.LoadDefaultConfigFile(os.Stderr),
			TLSConfigs:       nil,
			ExpireCachedAuth: nil,
		}),
	}
}
