package docker

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/cli/cli/config"
	"github.com/docker/cli/cli/config/configfile"
	"github.com/moby/moby/api/types/registry"
)

// fakeCredentialHelper answers the docker credential-helper protocol for one
// registry: get returns helper-user/helper-secret for registry.example.com and
// "not found" for anything else, list names that one registry. A helper named
// broken fails every call.
const fakeCredentialHelper = `#!/bin/sh
case "$1" in
get)
  read -r server
  if [ "$server" = "registry.example.com" ]; then
    printf '{"ServerURL":"%s","Username":"helper-user","Secret":"helper-secret"}' "$server"
  else
    printf 'credentials not found in native keychain'
    exit 1
  fi
  ;;
list)
  printf '{"registry.example.com":"helper-user"}'
  ;;
esac
`

const brokenCredentialHelper = `#!/bin/sh
printf 'helper exploded'
exit 2
`

// useDockerConfig writes configJSON as the docker CLI config and points the
// loadCLIConfig seam at it, so a test never reads the host's configuration. An
// empty configJSON leaves the directory without a config.json.
func useDockerConfig(t *testing.T, configJSON string) {
	t.Helper()
	dir := t.TempDir()
	if configJSON != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(configJSON), 0o600); err != nil {
			t.Fatalf("write config.json: %v", err)
		}
	}
	orig := loadCLIConfig
	loadCLIConfig = func() *configfile.ConfigFile {
		loaded, err := config.Load(dir)
		if err != nil {
			t.Fatalf("load docker config: %v", err)
		}
		return loaded
	}
	t.Cleanup(func() { loadCLIConfig = orig })
}

// installCredentialHelpers puts the fake and broken credential helpers on PATH
// as docker-credential-fake and docker-credential-broken.
func installCredentialHelpers(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for name, script := range map[string]string{"fake": fakeCredentialHelper, "broken": brokenCredentialHelper} {
		if err := os.WriteFile(filepath.Join(dir, "docker-credential-"+name), []byte(script), 0o700); err != nil {
			t.Fatalf("write credential helper %s: %v", name, err)
		}
	}
	t.Setenv("PATH", dir)
}

func basicAuth(username, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
}

// decodeRegistryAuth decodes an X-Registry-Auth value the way the engine does.
func decodeRegistryAuth(t *testing.T, encoded string) registry.AuthConfig {
	t.Helper()
	data, err := base64.URLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode registry auth %q: %v", encoded, err)
	}
	var auth registry.AuthConfig
	if err := json.Unmarshal(data, &auth); err != nil {
		t.Fatalf("unmarshal registry auth: %v", err)
	}
	return auth
}

// TestPullRegistryAuthResolvesTheImageRegistry pins which credentials a pull
// sends: those the docker CLI would use for the registry the reference names,
// from plain auths, a per-registry credHelper, or the credsStore; Docker Hub
// credentials live under the CLI's index key. Anything unresolvable pulls
// anonymously.
func TestPullRegistryAuthResolvesTheImageRegistry(t *testing.T) {
	plainAuths := `{"auths":{"registry.example.com":{"auth":"` + basicAuth("file-user", "file-secret") + `"},` +
		`"https://index.docker.io/v1/":{"auth":"` + basicAuth("hub-user", "hub-secret") + `"}}}`
	for _, tc := range []struct {
		name         string
		configJSON   string
		ref          string
		wantUsername string
		wantPassword string
	}{
		{
			name:         "plain auths",
			configJSON:   plainAuths,
			ref:          "registry.example.com/team/base:1.0",
			wantUsername: "file-user",
			wantPassword: "file-secret",
		},
		{
			name:         "docker hub under the index key",
			configJSON:   plainAuths,
			ref:          "debian:trixie-slim",
			wantUsername: "hub-user",
			wantPassword: "hub-secret",
		},
		{
			name:         "credential helper for the registry",
			configJSON:   `{"credHelpers":{"registry.example.com":"fake"}}`,
			ref:          "registry.example.com/team/base:1.0",
			wantUsername: "helper-user",
			wantPassword: "helper-secret",
		},
		{
			name:         "credentials store",
			configJSON:   `{"credsStore":"fake"}`,
			ref:          "registry.example.com/team/base:1.0",
			wantUsername: "helper-user",
			wantPassword: "helper-secret",
		},
		{name: "registry without credentials", configJSON: plainAuths, ref: "ghcr.io/team/base:1.0"},
		{name: "credentials store without an entry", configJSON: `{"credsStore":"fake"}`, ref: "ghcr.io/team/base"},
		{name: "failing credential helper", configJSON: `{"credsStore":"broken"}`, ref: "registry.example.com/base"},
		{name: "missing config", ref: "registry.example.com/team/base:1.0"},
		{name: "unparseable reference", configJSON: plainAuths, ref: "Not A Reference"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installCredentialHelpers(t)
			useDockerConfig(t, tc.configJSON)

			encoded := pullRegistryAuth(tc.ref)
			if tc.wantUsername == "" {
				if encoded != "" {
					t.Fatalf("pullRegistryAuth(%q) = %q, want anonymous", tc.ref, encoded)
				}
				return
			}
			auth := decodeRegistryAuth(t, encoded)
			if auth.Username != tc.wantUsername || auth.Password != tc.wantPassword {
				t.Errorf("pullRegistryAuth(%q) credentials = %q/%q, want %q/%q",
					tc.ref, auth.Username, auth.Password, tc.wantUsername, tc.wantPassword)
			}
		})
	}
}

// TestBuildRegistryAuthsResolvesEveryRegistry pins the credentials a classic
// build sends: every registry the docker CLI knows credentials for, with a
// per-registry credHelper overriding the default store, and none when the
// configuration is missing or the store fails.
func TestBuildRegistryAuthsResolvesEveryRegistry(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configJSON string
		want       map[string]string
	}{
		{
			name: "plain auths",
			configJSON: `{"auths":{"registry.example.com":{"auth":"` + basicAuth("file-user", "file-secret") + `"},` +
				`"empty.example.com":{}}}`,
			want: map[string]string{"registry.example.com": "file-user"},
		},
		{
			name:       "credentials store",
			configJSON: `{"credsStore":"fake"}`,
			want:       map[string]string{"registry.example.com": "helper-user"},
		},
		{
			name: "credential helper overrides plain auths",
			configJSON: `{"auths":{"registry.example.com":{"auth":"` + basicAuth("file-user", "file-secret") + `"}},` +
				`"credHelpers":{"registry.example.com":"fake"}}`,
			want: map[string]string{"registry.example.com": "helper-user"},
		},
		{name: "failing credentials store", configJSON: `{"credsStore":"broken"}`},
		{name: "missing config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installCredentialHelpers(t)
			useDockerConfig(t, tc.configJSON)

			auths := buildRegistryAuths()
			if len(auths) != len(tc.want) {
				t.Fatalf("buildRegistryAuths() = %d registries, want %v", len(auths), tc.want)
			}
			for registryHost, wantUsername := range tc.want {
				if got := auths[registryHost].Username; got != wantUsername {
					t.Errorf("buildRegistryAuths()[%q].Username = %q, want %q", registryHost, got, wantUsername)
				}
			}
		})
	}
}
