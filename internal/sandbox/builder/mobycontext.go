package builder

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/moby/moby/client"
)

// The moby client only reads DOCKER_HOST, while the docker CLI resolves its
// endpoint from the active docker context. colima and Docker Desktop publish
// their socket that way, so their sockets stay invisible here unless the
// endpoint is resolved like the CLI does.
const (
	envDockerHost    = "DOCKER_HOST"
	envDockerContext = "DOCKER_CONTEXT"

	// defaultContextName is the docker CLI context that means "DOCKER_HOST or
	// the built-in socket"; it has no stored endpoint.
	defaultContextName = "default"

	dockerEndpointName = "docker"
)

// clientOptions builds the moby client options, resolving the docker endpoint
// the way the docker CLI does: DOCKER_HOST first, then the active docker
// context, then the client's built-in default socket.
func clientOptions() []client.Opt {
	options := []client.Opt{client.FromEnv}
	if host := resolveHost(); host != "" {
		options = append(options, client.WithHost(host))
	}
	return options
}

// resolveHost returns the endpoint of the active docker context, or "" when the
// moby client's own resolution should stay in charge.
func resolveHost() string {
	if os.Getenv(envDockerHost) != "" {
		return ""
	}
	configDir := dockerConfigDir()
	name := activeContextName(configDir)
	if name == "" || name == defaultContextName {
		return ""
	}
	return contextEndpoint(configDir, name)
}

// activeContextName reports the selected docker context, preferring
// DOCKER_CONTEXT over the persisted CLI selection, and returns "" when neither
// names one.
func activeContextName(configDir string) string {
	if name := os.Getenv(envDockerContext); name != "" {
		return name
	}
	var config struct {
		CurrentContext string `json:"currentContext"`
	}
	if err := readJSONFile(filepath.Join(configDir, "config.json"), &config); err != nil {
		return ""
	}
	return config.CurrentContext
}

// contextEndpoint returns the docker endpoint stored for the named context, or
// "" when the context has no readable docker endpoint. The docker CLI keeps the
// metadata in a directory named after the sha256 digest of the context name.
func contextEndpoint(configDir, name string) string {
	digest := sha256.Sum256([]byte(name))
	metaPath := filepath.Join(configDir, "contexts", "meta", hex.EncodeToString(digest[:]), "meta.json")
	var meta struct {
		Endpoints map[string]struct {
			Host string `json:"Host"`
		} `json:"Endpoints"`
	}
	if err := readJSONFile(metaPath, &meta); err != nil {
		return ""
	}
	return meta.Endpoints[dockerEndpointName].Host
}

// readJSONFile decodes the JSON file at path into target.
func readJSONFile(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
