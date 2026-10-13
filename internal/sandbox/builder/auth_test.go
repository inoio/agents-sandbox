package builder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/cli/cli/config/types"

	"github.com/inoio/agents-sandbox/internal/sandbox/builder/cacheregistry"
)

func TestRegistryAuthSessionIncludesCacheToken(t *testing.T) {
	endpoint := cacheregistry.Endpoint{Host: cacheregistry.GuestHost, Port: 5000, Token: "cache-token-abc"}

	configFile := registryAuthConfig(cacheRegistryCredentials(endpoint))
	got, ok := configFile.AuthConfigs[endpoint.RegistryHost()]
	if !ok {
		t.Fatalf("merged config has no credential for %q", endpoint.RegistryHost())
	}
	if got.RegistryToken != endpoint.Token {
		t.Errorf("credential RegistryToken = %q, want %q", got.RegistryToken, endpoint.Token)
	}

	attachable := registryAuthSession(cacheRegistryCredentials(endpoint))
	if len(attachable) != 1 || attachable[0] == nil {
		t.Fatalf("registryAuthSession returned %v, want one non-nil attachable", attachable)
	}
}

// TestCacheRegistryAuthKeysCredentialByRegistryHost drives the production
// derivation: the credential key must be the full host:port that both the cache
// reference authority and BuildKit's auth lookup use, never the bare host.
func TestCacheRegistryAuthKeysCredentialByRegistryHost(t *testing.T) {
	endpoint := cacheregistry.Endpoint{Host: cacheregistry.GuestHost, Port: 5000, Token: "tok"}
	stubCacheRegistry(t, endpoint)

	cacheRef, session, err := (&Client{}).cacheRegistryAuth(context.Background(), BuildRequest{
		Tag:      "project/opencode:1.2.3",
		Platform: "linux/amd64",
	})
	if err != nil {
		t.Fatalf("cacheRegistryAuth: %v", err)
	}
	if len(session) != 1 || session[0] == nil {
		t.Fatalf("cacheRegistryAuth session = %v, want one non-nil attachable", session)
	}

	refHost, _, _ := strings.Cut(cacheRef, "/")
	if refHost != endpoint.RegistryHost() {
		t.Errorf("cache ref host = %q, want %q", refHost, endpoint.RegistryHost())
	}

	credentials := cacheRegistryCredentials(endpoint)
	if _, ok := credentials[endpoint.RegistryHost()]; !ok {
		t.Errorf("credentials keyed by %q, want %q", credentials, endpoint.RegistryHost())
	}
	if _, ok := credentials[endpoint.Host]; ok {
		t.Errorf("credentials keyed by the bare host %q; the port is required", endpoint.Host)
	}
}

// TestRegistryAuthConfigBypassesCredentialStore asserts the embedded token still
// resolves when the user's Docker config sets a global credsStore (the default on
// Docker Desktop), which would otherwise shadow the in-memory entry. It points
// the dockerConfigDir seam at a temp dir so it is hermetic and independent of the
// process-global config.Dir() memoization (and thus of test order).
func TestRegistryAuthConfigBypassesCredentialStore(t *testing.T) {
	const host = cacheregistry.GuestHost + ":5000"
	const token = "cache-token"
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	credsStoreConfig := []byte(`{"credsStore":"desktop"}`)
	if err := os.WriteFile(configPath, credsStoreConfig, 0o600); err != nil {
		t.Fatalf("write docker config: %v", err)
	}
	restore := dockerConfigDir
	t.Cleanup(func() { dockerConfigDir = restore })
	dockerConfigDir = func() string { return dir }

	configFile := registryAuthConfig(map[string]types.AuthConfig{
		host: {RegistryToken: token},
	})
	got, err := configFile.GetAuthConfig(host)
	if err != nil {
		t.Fatalf("GetAuthConfig(%q): %v", host, err)
	}
	if got.RegistryToken != token {
		t.Errorf("RegistryToken = %q, want %q despite credsStore", got.RegistryToken, token)
	}

	onDisk, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read back docker config: %v", err)
	}
	if string(onDisk) != string(credsStoreConfig) {
		t.Errorf("on-disk docker config changed: %q", onDisk)
	}
}
