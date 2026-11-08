package viperconfig

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	oneyaml "oneyaml.dev/go"

	"github.com/inoio/agents-sandbox/internal/atomicwrite"
	"github.com/inoio/agents-sandbox/internal/configdecode"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configread"
	"github.com/inoio/agents-sandbox/internal/yamlfmt"
)

// ErrUnsupportedConfigFormat reports that a config file the launcher would have
// to edit is not YAML, so automatic edits cannot preserve its format. The file
// is left untouched and the caller surfaces the miss.
var ErrUnsupportedConfigFormat = errors.New("unsupported config file format for automated edits")

const (
	extYAML = ".yaml"
	extYML  = ".yml"
)

// evalSymlinks is the link-resolution seam, substituted by tests to exercise the
// resolution error path deterministically.
var evalSymlinks = filepath.EvalSymlinks

// LayerSettingOwner returns the directory of the highest-precedence file layer
// that sets key (project config > per-slug user config > generic user config),
// or ok=false when no file layer sets it. A layer whose file cannot be read or
// parsed is skipped, so one broken file cannot abort the whole resolution.
func LayerSettingOwner(paths configpaths.ConfigPaths, slug, key string) (string, bool) {
	layers := ConfigLayers(paths, slug)
	// ConfigLayers is lowest to highest precedence; walk it backwards so the
	// first match is the highest-precedence owner.
	for _, layer := range slices.Backward(layers) {
		// A non-file layer has an empty Path, which FindConfigFile reports as
		// no file, so layerSetsKey returns false without a separate guard.
		if layerSetsKey(layer.Path, key) {
			return layer.Path, true
		}
	}
	return "", false
}

// layerSetsKey reports whether the config file in dir resolves key, in whatever
// format that file uses. An absent, unreadable, or unparseable file reports
// false.
func layerSetsKey(dir, key string) bool {
	path, _, ok := configpaths.FindConfigFile(dir)
	// gomutants:disable-next-line BRANCH_IF reason="without a config file the empty path fails configread's open and the next guard returns false, so the body is redundant"
	if !ok {
		return false
	}
	values, err := configdecode.DecodeFile(path, "launcher config")
	// A parse error must discard the values: the decoders fill earlier keys
	// before a later error, so the partial map can still hold the key.
	if err != nil {
		return false
	}
	return mapSetsKey(values, key)
}

// mapSetsKey reports whether the dotted key is present in values, descending
// through nested mappings. Keys are matched case-insensitively because viper
// resolves config keys that way, so a document spelling `Memory:` sets the
// effective `memory` key.
func mapSetsKey(values map[string]any, key string) bool {
	var current any = values
	for segment := range strings.SplitSeq(key, ".") {
		mapping, ok := current.(map[string]any)
		// gomutants:disable-next-line BRANCH_IF reason="a non-mapping yields a nil mapping, and lookupKeyFold on a nil map returns ok=false at the next guard"
		if !ok {
			return false
		}
		value, ok := lookupKeyFold(mapping, segment)
		if !ok {
			return false
		}
		current = value
	}
	return true
}

// lookupKeyFold returns the value stored under key, matching the key the way
// viper resolves config keys: case-insensitively at every level. An exact match
// is preferred so a document holding both spellings resolves deterministically.
func lookupKeyFold(mapping map[string]any, key string) (any, bool) {
	if value, ok := mapping[key]; ok {
		return value, true
	}
	for existing, value := range mapping {
		if strings.EqualFold(existing, key) {
			return value, true
		}
	}
	return nil, false
}

// WriteSetting sets key to value in the config file in dir, creating config.yaml
// when the directory has no config file. It returns ErrUnsupportedConfigFormat
// (writing nothing) when dir holds a non-YAML config file, so a JSON5/JSON/JSONC
// file is never corrupted. A YAML file is edited through its document tree, so
// unrelated keys and the user's comments, spacing and styles survive. An edited
// file keeps its permissions; a created one is private (0600).
func WriteSetting(dir, key string, value any) error {
	if dir == "" {
		return fmt.Errorf("config dir is empty; the config path did not resolve")
	}
	path, ext, found := configpaths.FindConfigFile(dir)
	if !found {
		path = filepath.Join(dir, "config.yaml")
		return writeYAMLSetting(path, nil, 0o600, key, value)
	}
	if ext != extYAML && ext != extYML {
		return ErrUnsupportedConfigFormat
	}
	// Edit the link target, not the link: atomicwrite renames its temporary
	// file over path, which would replace a symlinked config with a regular file.
	path, err := evalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve config file link %s: %w", path, err)
	}
	info, statErr := os.Stat(path)
	data, readErr := configread.ReadFile(path)
	if err := errors.Join(statErr, readErr); err != nil {
		return fmt.Errorf("read config file %s: %w", path, err)
	}
	return writeYAMLSetting(path, data, info.Mode().Perm(), key, value)
}

// writeYAMLSetting applies key=value to the YAML document in data and writes the
// result atomically to path with mode perm. An empty data set starts a new
// document.
func writeYAMLSetting(path string, data []byte, perm os.FileMode, key string, value any) error {
	document, err := oneyaml.Parse(data, oneyaml.Untrusted())
	if err != nil {
		return yamlfmt.WrapErr(path, err)
	}
	edited, err := setDocumentValue(document, strings.Split(key, "."), value)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return atomicwrite.WriteFileMode(path, edited, perm)
}

// setDocumentValue sets the dotted key to value on document and returns the new
// document bytes. An existing entry is replaced in place (keeping its spelling,
// comments and style); a missing key is added, creating intermediate mappings.
func setDocumentValue(document *oneyaml.Document, keyPath []string, value any) ([]byte, error) {
	root := document.Root()
	if !root.IsValid() {
		if err := document.Set("", nestedValue(keyPath, value)); err != nil {
			return nil, err
		}
		return document.Bytes(), nil
	}
	if root.Kind() != oneyaml.MappingNode {
		return nil, errors.New("config file root is not a mapping")
	}
	tokens, valueNode, found := resolveExistingPath(root, keyPath)
	if found {
		if err := ensureReplaceableValue(root, valueNode, keyPath); err != nil {
			return nil, err
		}
		pointer, err := valueNode.Pointer()
		if err != nil {
			return nil, err
		}
		if err := document.Set(pointer, value); err != nil {
			return nil, err
		}
		return document.Bytes(), nil
	}
	// The existing prefix resolves but the next segment does not: adding that
	// segment with the rest of the path nested creates the whole chain.
	target := append(append([]string{}, tokens...), keyPath[len(tokens)])
	if err := document.Set(oneyaml.Pointer(target...), nestedValue(keyPath[len(tokens)+1:], value)); err != nil {
		return nil, err
	}
	edited := document.Bytes()
	if !bytes.HasSuffix(edited, []byte("\n")) {
		edited = append(bytes.Clone(edited), '\n')
	}
	return edited, nil
}

// resolveExistingPath walks keyPath through root, matching each segment
// case-insensitively, and reports the actual key spellings of the longest
// existing prefix, the node the full path resolves to, and whether it resolved.
func resolveExistingPath(root oneyaml.Node, keyPath []string) ([]string, oneyaml.Node, bool) {
	var tokens []string
	current := root
	for _, segment := range keyPath {
		key, next, ok := findEntryFold(current, segment)
		if !ok {
			// gomutants:disable-next-line RETURN_ZERO reason="the caller reads the node only when found is true, so the not-found node is discarded"
			return tokens, current, false
		}
		tokens = append(tokens, key.Value())
		current = next
	}
	// gomutants:disable-next-line RETURN_ZERO reason="the caller reads tokens only on the not-found path, so the found-path tokens are discarded"
	return tokens, current, true
}

// findEntryFold returns the key and value nodes of the mapping entry whose key
// matches segment case-insensitively, mirroring viper's case-insensitive
// resolution so an existing `Memory:` entry is edited there rather than
// duplicated. Entries yields nothing for a non-mapping, so a scalar or sequence
// yields no entry.
func findEntryFold(mapping oneyaml.Node, segment string) (oneyaml.Node, oneyaml.Node, bool) {
	for existingKey, existingValue := range mapping.Entries() {
		if strings.EqualFold(existingKey.Value(), segment) {
			return existingKey, existingValue, true
		}
	}
	return oneyaml.Node{}, oneyaml.Node{}, false
}

// nestedValue wraps value in nested mappings for the dotted keyPath suffix, so
// setting the first segment of the suffix creates the whole remaining chain.
func nestedValue(keyPath []string, value any) any {
	for _, segment := range slices.Backward(keyPath) {
		value = map[string]any{segment: value}
	}
	return value
}

// ensureReplaceableValue refuses to replace an anchored value that an alias
// elsewhere in the document references: replacing it would change every alias
// too, so the value is left untouched rather than edited surprisingly.
func ensureReplaceableValue(root, value oneyaml.Node, keyPath []string) error {
	anchor := value.Anchor()
	// gomutants:disable-next-line BRANCH_IF reason="no alias names the empty anchor, so anchorReferenced reports false and the fall-through returns nil too"
	if anchor == "" {
		return nil
	}
	if anchorReferenced(root, anchor) {
		return fmt.Errorf(
			"value %s is a YAML anchor referenced elsewhere; refusing to edit",
			strings.Join(keyPath, "."),
		)
	}
	return nil
}

// anchorReferenced reports whether any alias in the document references anchor.
func anchorReferenced(node oneyaml.Node, anchor string) bool {
	switch node.Kind() {
	case oneyaml.AliasNode:
		return node.AliasName() == anchor
	case oneyaml.MappingNode:
		for key, value := range node.Entries() {
			if anchorReferenced(key, anchor) || anchorReferenced(value, anchor) {
				return true
			}
		}
	case oneyaml.SequenceNode:
		for item := range node.Items() {
			if anchorReferenced(item, anchor) {
				return true
			}
		}
	case oneyaml.ScalarNode, oneyaml.InvalidNode:
		// gomutants:disable-next-line BRANCH_CASE reason="removing the body falls through to the trailing return false, the same value"
		return false
	}
	return false
}
