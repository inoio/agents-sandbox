package configpaths

import (
	"os"
	"path/filepath"
)

// ExtJSONC and ExtJSON5 are the two config-file extensions that carry JSON5
// syntax (comments, trailing commas). The values are shared so the
// launcher-config and home-manifest loaders recognize and normalize them
// identically.
const (
	ExtJSONC = ".jsonc"
	ExtJSON5 = ".json5"
)

// supportedExts lists the config-file extensions in the order FindConfigFile
// tries them. Both the launcher config and the home manifest are discovered
// through FindConfigFile, so this is the single order the two agree on.
var supportedExts = []string{".yaml", ".yml", ".json", ExtJSONC, ExtJSON5}

// FindConfigFile returns the first existing config file in dir: the path, the
// matching extension, and whether one was found. An empty dir reports no file;
// the extension lets callers normalize JSONC/JSON5 before parsing.
func FindConfigFile(dir string) (string, string, bool) {
	if dir == "" {
		return "", "", false
	}
	for _, ext := range supportedExts {
		path := filepath.Join(dir, "config"+ext)
		if _, err := os.Stat(path); err == nil {
			return path, ext, true
		}
	}
	return "", "", false
}
