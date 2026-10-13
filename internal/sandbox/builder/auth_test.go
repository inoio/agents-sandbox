package builder

import (
	"testing"

	"github.com/docker/cli/cli/config/types"
)

func TestRegistryAuthSessionIncludesCacheToken(t *testing.T) {
	const host = "172.30.0.1:5000"
	const token = "cache-token-abc"

	configFile := registryAuthConfig(map[string]types.AuthConfig{
		host: {RegistryToken: token},
	})
	got, ok := configFile.AuthConfigs[host]
	if !ok {
		t.Fatalf("merged config has no credential for %q", host)
	}
	if got.RegistryToken != token {
		t.Errorf("credential RegistryToken = %q, want %q", got.RegistryToken, token)
	}

	attachable := registryAuthSession(map[string]types.AuthConfig{host: {RegistryToken: token}})
	if len(attachable) != 1 || attachable[0] == nil {
		t.Fatalf("registryAuthSession returned %v, want one non-nil attachable", attachable)
	}
}
