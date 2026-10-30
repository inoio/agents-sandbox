package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/moby/moby/client"
)

const colimaSocket = "unix:///Users/tester/.colima/default/docker.sock"

// writeDockerConfig creates a docker config directory with currentContext set
// to name and points DOCKER_CONFIG at it.
func writeDockerConfig(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	if name != "" {
		data, err := json.Marshal(map[string]string{"currentContext": name})
		if err != nil {
			t.Fatalf("marshal config: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
			t.Fatalf("write config.json: %v", err)
		}
	}
	t.Setenv(envDockerConfig, dir)
	return dir
}

// writeContextMeta stores a context metadata file the way the docker CLI does,
// in a directory named after the sha256 digest of the context name.
func writeContextMeta(t *testing.T, configDir, name, host string) {
	t.Helper()
	digest := sha256.Sum256([]byte(name))
	metaDir := filepath.Join(configDir, "contexts", "meta", hex.EncodeToString(digest[:]))
	if err := os.MkdirAll(metaDir, 0o700); err != nil {
		t.Fatalf("mkdir meta: %v", err)
	}
	meta := map[string]any{
		"Name":      name,
		"Endpoints": map[string]any{"docker": map[string]any{"Host": host}},
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, "meta.json"), data, 0o600); err != nil {
		t.Fatalf("write meta.json: %v", err)
	}
}

func TestResolveHostFromActiveContext(t *testing.T) {
	t.Setenv(envDockerHost, "")
	configDir := writeDockerConfig(t, "colima")
	writeContextMeta(t, configDir, "colima", colimaSocket)

	if got := resolveHost(); got != colimaSocket {
		t.Errorf("resolveHost() = %q, want %q", got, colimaSocket)
	}
}

func TestResolveHostPrefersDockerHostEnv(t *testing.T) {
	t.Setenv(envDockerHost, "unix:///var/run/docker.sock")
	configDir := writeDockerConfig(t, "colima")
	writeContextMeta(t, configDir, "colima", colimaSocket)

	if got := resolveHost(); got != "" {
		t.Errorf("resolveHost() = %q, want %q so DOCKER_HOST keeps precedence", got, "")
	}
}

func TestResolveHostPrefersDockerContextEnvOverConfig(t *testing.T) {
	const remote = "tcp://docker.example:2376"
	t.Setenv(envDockerHost, "")
	configDir := writeDockerConfig(t, "colima")
	writeContextMeta(t, configDir, "colima", colimaSocket)
	writeContextMeta(t, configDir, "remote", remote)
	t.Setenv(envDockerContext, "remote")

	if got := resolveHost(); got != remote {
		t.Errorf("resolveHost() = %q, want %q", got, remote)
	}
}

func TestResolveHostIgnoresUnusableContexts(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T)
	}{
		{
			name:    "no config file",
			prepare: func(t *testing.T) { writeDockerConfig(t, "") },
		},
		{
			name:    "default context",
			prepare: func(t *testing.T) { writeDockerConfig(t, "default") },
		},
		{
			name: "default context with stored metadata",
			prepare: func(t *testing.T) {
				configDir := writeDockerConfig(t, "default")
				writeContextMeta(t, configDir, "default", colimaSocket)
			},
		},
		{
			name: "no active context with stored metadata",
			prepare: func(t *testing.T) {
				configDir := writeDockerConfig(t, "")
				writeContextMeta(t, configDir, "", colimaSocket)
			},
		},
		{
			name: "missing metadata",
			prepare: func(t *testing.T) {
				writeDockerConfig(t, "colima")
			},
		},
		{
			name: "unparsable metadata",
			prepare: func(t *testing.T) {
				configDir := writeDockerConfig(t, "colima")
				digest := sha256.Sum256([]byte("colima"))
				metaDir := filepath.Join(configDir, "contexts", "meta", hex.EncodeToString(digest[:]))
				if err := os.MkdirAll(metaDir, 0o700); err != nil {
					t.Fatalf("mkdir meta: %v", err)
				}
				if err := os.WriteFile(filepath.Join(metaDir, "meta.json"), []byte("{"), 0o600); err != nil {
					t.Fatalf("write meta.json: %v", err)
				}
			},
		},
		{
			name: "context without docker endpoint",
			prepare: func(t *testing.T) {
				configDir := writeDockerConfig(t, "kube-only")
				writeContextMeta(t, configDir, "kube-only", "")
			},
		},
		{
			name: "unparsable config file",
			prepare: func(t *testing.T) {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{"), 0o600); err != nil {
					t.Fatalf("write config.json: %v", err)
				}
				t.Setenv(envDockerConfig, dir)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envDockerHost, "")
			tt.prepare(t)

			if got := resolveHost(); got != "" {
				t.Errorf("resolveHost() = %q, want %q", got, "")
			}
		})
	}
}

func TestResolveHostFallsBackToHomeDockerConfig(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".docker")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir .docker: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(configDir, "config.json"),
		[]byte(`{"currentContext":"colima"}`),
		0o600,
	); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	writeContextMeta(t, configDir, "colima", colimaSocket)
	t.Setenv(envDockerHost, "")
	t.Setenv(envDockerConfig, "")
	t.Setenv("HOME", home)

	if got := resolveHost(); got != colimaSocket {
		t.Errorf("resolveHost() = %q, want %q", got, colimaSocket)
	}
}

func TestResolveHostWithoutResolvableHomeDirectory(t *testing.T) {
	t.Setenv(envDockerHost, "")
	t.Setenv(envDockerConfig, "")
	t.Setenv("HOME", "")

	if got := resolveHost(); got != "" {
		t.Errorf("resolveHost() = %q, want %q", got, "")
	}
}

// TestResolveHostIgnoresConfigWithoutHomeDirectory places a valid docker config
// and context metadata in the working directory: an unresolvable home must stop
// resolveHost before it reads them from the relative path.
func TestResolveHostIgnoresConfigWithoutHomeDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "config.json"),
		[]byte(`{"currentContext":"colima"}`),
		0o600,
	); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	writeContextMeta(t, dir, "colima", colimaSocket)
	t.Chdir(dir)

	t.Setenv(envDockerHost, "")
	t.Setenv(envDockerContext, "")
	t.Setenv(envDockerConfig, "")
	t.Setenv("HOME", "")

	if got := resolveHost(); got != "" {
		t.Errorf("resolveHost() = %q, want %q", got, "")
	}
}

func TestDockerConfigDirWithoutHomeDirectory(t *testing.T) {
	t.Setenv(envDockerConfig, "")
	t.Setenv("HOME", "")

	dir, err := dockerConfigDir()
	if err == nil {
		t.Error("dockerConfigDir() error = nil, want an error without a home directory")
	}
	if dir != "" {
		t.Errorf("dockerConfigDir() = %q, want %q", dir, "")
	}
}

func TestActiveContextNameIgnoresUnparsableConfig(t *testing.T) {
	dir := t.TempDir()
	// The duplicate key sets CurrentContext before the wrongly typed second value
	// makes json.Unmarshal fail; the guard must discard the partial decode.
	if err := os.WriteFile(
		filepath.Join(dir, "config.json"),
		[]byte(`{"currentContext":"colima","currentContext":123}`),
		0o600,
	); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	t.Setenv(envDockerContext, "")

	if got := activeContextName(dir); got != "" {
		t.Errorf("activeContextName() = %q, want %q", got, "")
	}
}

func TestContextEndpointIgnoresUnparsableMetadata(t *testing.T) {
	dir := t.TempDir()
	digest := sha256.Sum256([]byte("colima"))
	metaDir := filepath.Join(dir, "contexts", "meta", hex.EncodeToString(digest[:]))
	if err := os.MkdirAll(metaDir, 0o700); err != nil {
		t.Fatalf("mkdir meta: %v", err)
	}
	// The duplicate key sets the docker endpoint before the wrongly typed second
	// value makes json.Unmarshal fail; the guard must discard the partial decode.
	if err := os.WriteFile(
		filepath.Join(metaDir, "meta.json"),
		[]byte(`{"Endpoints":{"docker":{"Host":"h"}},"Endpoints":123}`),
		0o600,
	); err != nil {
		t.Fatalf("write meta.json: %v", err)
	}

	if got := contextEndpoint(dir, "colima"); got != "" {
		t.Errorf("contextEndpoint() = %q, want %q", got, "")
	}
}

func TestReadJSONFileMissingPath(t *testing.T) {
	var target map[string]any
	err := readJSONFile(filepath.Join(t.TempDir(), "absent.json"), &target)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("readJSONFile() error = %v, want fs.ErrNotExist", err)
	}
}

func TestClientOptionsConnectToActiveContext(t *testing.T) {
	t.Setenv(envDockerHost, "")
	configDir := writeDockerConfig(t, "colima")
	writeContextMeta(t, configDir, "colima", colimaSocket)

	c, err := client.New(clientOptions()...)
	if err != nil {
		t.Fatalf("client.New() error = %v, want nil", err)
	}
	t.Cleanup(func() { c.Close() })

	if got := c.DaemonHost(); got != colimaSocket {
		t.Errorf("DaemonHost() = %q, want %q", got, colimaSocket)
	}
}

func TestClientOptionsFallBackToEnvironment(t *testing.T) {
	const envHost = "unix:///Users/tester/.docker/run/docker.sock"
	t.Setenv(envDockerHost, envHost)
	configDir := writeDockerConfig(t, "colima")
	writeContextMeta(t, configDir, "colima", colimaSocket)

	c, err := client.New(clientOptions()...)
	if err != nil {
		t.Fatalf("client.New() error = %v, want nil", err)
	}
	t.Cleanup(func() { c.Close() })

	if got := c.DaemonHost(); got != envHost {
		t.Errorf("DaemonHost() = %q, want %q", got, envHost)
	}
}
