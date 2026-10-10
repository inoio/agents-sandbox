package builder

import (
	"testing"

	"github.com/docker/cli/cli/config/configfile"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
)

func TestBuildKitDependenciesResolve(t *testing.T) {
	var (
		_ *client.Client
		_ *llb.State
		_ *session.Group
		_ authprovider.DockerAuthProviderConfig
		_ gwclient.Client
		_ configfile.ConfigFile
	)
}
