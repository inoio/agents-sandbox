// Package configdecode reads and decodes launcher, home-manifest, and agent
// snippet config files through one core, so a file one loader accepts cannot be
// rejected — or parsed differently — by another. It layers over configread's
// bounded read and the extension vocabulary the loaders share; the accepted
// format set, error label, and parse-failure policy stay with each caller.
package configdecode

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/titanous/json5"
	"go.yaml.in/yaml/v4"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configread"
	"github.com/inoio/agents-sandbox/internal/projectguard"
	"github.com/inoio/agents-sandbox/internal/yamlfmt"
)

// Format is the serialization family a config file's extension implies.
type Format int

const (
	FormatYAML Format = iota
	FormatJSON
	FormatTOML
)

// FormatFor classifies a file name's extension (case-insensitively). ok is
// false for an unknown extension, so a caller that accepts only some formats
// can reject it itself.
func FormatFor(name string) (Format, bool) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml":
		return FormatYAML, true
	case ".json", ".jsonc", ".json5":
		return FormatJSON, true
	case ".toml":
		return FormatTOML, true
	}
	return FormatYAML, false
}

// Decode parses data into a string-keyed map using format. The JSON family
// (JSON, JSONC, JSON5) is parsed with titanous/json5, YAML with
// go.yaml.in/yaml/v4, TOML with pelletier/go-toml/v2. On a parse failure the
// partially filled map is returned alongside the error; callers must check the
// error before using the map.
func Decode(format Format, data []byte) (map[string]any, error) {
	values := map[string]any{}
	if format == FormatJSON {
		return values, json5.Unmarshal(data, &values)
	}
	if format == FormatTOML {
		return values, toml.Unmarshal(data, &values)
	}
	return values, yaml.Unmarshal(data, &values)
}

// FindFile returns the config file in dir as configpaths.FindConfigFile does.
// When there is none, it checks the project config approval: an approved
// config file that is gone is a refusal, so deleting it cannot make a reader
// fall back to the lower layers.
func FindFile(dir string) (string, string, bool, error) {
	if path, ext, found := configpaths.FindConfigFile(dir); found {
		return path, ext, true, nil
	}
	return "", "", false, projectguard.Missing(configpaths.ConfigFileCandidates(dir)...)
}

// DecodeFile reads path under configread.MaxBytes and decodes it, deriving the
// format from the path extension. label names the file in errors. An unknown
// extension is an error. A YAML parse failure is reported through yamlfmt so
// the line/column detail matches the loaders' user-facing messages. The read
// goes through projectguard, so a project config file returns only its
// approved bytes.
func DecodeFile(path, label string) (map[string]any, error) {
	data, err := projectguard.ReadFile(path, configread.ReadFile)
	if err != nil {
		return nil, fmt.Errorf("read %s %s: %w", label, path, err)
	}
	return DecodeData(path, label, data)
}

// DecodeData decodes data already read from path, as DecodeFile would.
func DecodeData(path, label string, data []byte) (map[string]any, error) {
	format, ok := FormatFor(path)
	if !ok {
		return nil, fmt.Errorf("parse %s %s: unsupported config extension %q", label, path, filepath.Ext(path))
	}
	values, err := Decode(format, data)
	if err != nil {
		if format == FormatYAML {
			return nil, yamlfmt.WrapErr(path, err)
		}
		return nil, fmt.Errorf("parse %s %s: %w", label, path, err)
	}
	return values, nil
}
