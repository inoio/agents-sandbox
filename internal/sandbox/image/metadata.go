package image

import (
	"encoding/json"
	"os"
	"path/filepath"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
)

// envDir returns the user cache directory for image env info.
func envDir() string {
	return configpaths.GetConfigPaths().UserCacheDir()
}

// envMetaFile returns the JSON file path for image env metadata, keyed by the
// stable tag so the data survives image rebuilds and docker prune.
func envMetaFile(tag string) string {
	return filepath.Join(envDir(), "image-env-"+tag+".json")
}

// storeImageEnv writes image env vars to a JSON file.
// Survives docker prune and image rebuilds.
func storeImageEnv(tag string, envs map[string]string) {
	if tag == "" || len(envs) == 0 {
		return
	}
	dir := envDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return
	}
	data, _ := json.Marshal(envs)
	_ = os.WriteFile(envMetaFile(tag), data, 0o600)
}

// loadImageEnv reads a previously stored image env map from the cached JSON
// file keyed by tag. Returns nil if no file exists.
func loadImageEnv(tag string) map[string]string {
	path := envMetaFile(tag)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := make(map[string]string)
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}
