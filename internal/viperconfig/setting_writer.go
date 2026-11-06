package viperconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/titanous/json5"
	"go.yaml.in/yaml/v3"

	"github.com/inoio/agents-sandbox/internal/atomicwrite"
	"github.com/inoio/agents-sandbox/internal/configpaths"
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

// marshalYAML is the YAML encoder seam. The document tree WriteSetting builds is
// always encodable, so tests substitute it to exercise the marshal error path.
var marshalYAML = marshalTwoSpaceIndent

// marshalTwoSpaceIndent encodes node with the two-space indentation config files
// are written in by hand; yaml.Marshal would use four.
func marshalTwoSpaceIndent(node any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	// gomutants:disable-next-line INTEGER_DECREMENT reason="yaml.v3's emitter raises an indent below 2 to 2"
	encoder.SetIndent(2)
	encodeErr := encoder.Encode(node)
	return buffer.Bytes(), errors.Join(encodeErr, encoder.Close())
}

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
	path, ext, ok := configpaths.FindConfigFile(dir)
	// gomutants:disable-next-line BRANCH_IF reason="without a config file the empty path fails os.ReadFile and the next guard returns false, so the body is redundant"
	if !ok {
		return false
	}
	values, err := readLayerValues(path, ext)
	// A parse error must discard the values: yaml.v3 fills earlier keys before
	// a later error, so the partial map can still hold the key.
	if err != nil {
		return false
	}
	return mapSetsKey(values, key)
}

// readLayerValues parses a config file into a nested string-keyed map using the
// parser its extension implies, mirroring mergeDir.
func readLayerValues(path, ext string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	values := map[string]any{}
	switch ext {
	case extYAML, extYML:
		err = yaml.Unmarshal(data, &values)
	case configpaths.ExtJSON5, configpaths.ExtJSONC:
		err = json5.Unmarshal(data, &values)
	default:
		err = json.Unmarshal(data, &values)
	}
	return values, err
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
// file is never corrupted. A YAML file is edited in place on its node tree, so
// unrelated keys and the user's comments survive. An edited file keeps its
// permissions; a created one is private (0600).
func WriteSetting(dir, key string, value any) error {
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
	data, readErr := os.ReadFile(path)
	if err := errors.Join(statErr, readErr); err != nil {
		return fmt.Errorf("read config file %s: %w", path, err)
	}
	return writeYAMLSetting(path, data, info.Mode().Perm(), key, value)
}

// writeYAMLSetting applies key=value to the YAML document in data and writes
// the result atomically to path with mode perm. An empty data set starts a new
// document.
func writeYAMLSetting(path string, data []byte, perm os.FileMode, key string, value any) error {
	document, err := parseYAMLDocument(path, data)
	if err != nil {
		return err
	}
	root, err := documentRootMapping(document)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	keyPath := strings.Split(key, ".")
	if err = ensureReplaceableValue(root, keyPath); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if edited, ok := editInPlace(data, root, keyPath, value); ok {
		return atomicwrite.WriteFileMode(path, edited, perm)
	}
	if err = setMappingKey(root, keyPath, value); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	encoded, err := marshalYAML(document)
	if err != nil {
		return fmt.Errorf("marshal config file %s: %w", path, err)
	}
	return atomicwrite.WriteFileMode(path, encoded, perm)
}

// editInPlace applies key=value as a text edit of data, so everything but the
// edited value stays byte-identical: an existing value is replaced where it
// stands, and a missing key is appended as one top-level line. The edited text
// must read back as value; otherwise (a nested key to create, a block or
// multi-line value, a flow-style document) it reports false and the caller
// re-encodes the document.
func editInPlace(data []byte, root *yaml.Node, keyPath []string, value any) ([]byte, bool) {
	rendered, ok := renderInline(value)
	if !ok {
		return nil, false
	}
	var edited []byte
	if current := mappingValue(root, keyPath); current != nil {
		edited = replaceValueText(data, current, rendered)
	} else {
		// For a nested key the top-level line lands at the wrong level and
		// fails the read-back.
		edited = appendTopLevelKey(data, keyPath[0], rendered)
	}
	return edited, readsBackAs(edited, keyPath, value)
}

// renderInline renders value as one line of YAML, or reports false when its
// rendering spans several lines.
func renderInline(value any) (string, bool) {
	// A marshal error leaves the text empty, which fails the read-back; the
	// re-encode then reports the error.
	text, _ := yaml.Marshal(value)
	rendered := strings.TrimSuffix(string(text), "\n")
	return rendered, !strings.Contains(rendered, "\n")
}

// replaceValueText replaces the text of node's value on its line with rendered.
// The value runs from node's column to a trailing comment or the end of the
// line, without trailing whitespace. It returns nil, which fails the read-back,
// when that cut is not valid YAML: a cut that misjudges the value (a "#" inside
// quotes or a flow collection) always ends inside an open quote or bracket.
func replaceValueText(data []byte, node *yaml.Node, rendered string) []byte {
	lines := strings.SplitAfter(string(data), "\n")
	line := []rune(lines[node.Line-1])
	start := node.Column - 1
	tail := string(line[start:])
	end := valueTextLength(tail)
	var cut any
	if yaml.Unmarshal([]byte(tail[:end]), &cut) != nil {
		return nil
	}
	lines[node.Line-1] = string(line[:start]) + rendered + tail[end:]
	return []byte(strings.Join(lines, ""))
}

// trailingComment matches the whitespace-led "#" that starts a YAML comment.
var trailingComment = regexp.MustCompile(`[ \t]#`)

// valueTextLength returns the byte length of the value text that rest starts
// with: up to a trailing comment, without trailing whitespace.
func valueTextLength(rest string) int {
	if comment := trailingComment.FindStringIndex(rest); comment != nil {
		rest = rest[:comment[0]]
	}
	return len(strings.TrimRight(rest, " \t\r\n"))
}

// appendTopLevelKey appends "key: rendered" as the document's last line.
func appendTopLevelKey(data []byte, key, rendered string) []byte {
	edited := bytes.Clone(data)
	// gomutants:disable-next-line INTEGER_INCREMENT reason="a one-byte config is either a newline or not a mapping, which WriteSetting refuses before editing"
	if len(edited) > 0 && !bytes.HasSuffix(edited, []byte("\n")) {
		edited = append(edited, '\n')
	}
	return append(edited, key+": "+rendered+"\n"...)
}

// readsBackAs reports whether edited parses to a document holding value at
// keyPath, matching keys the way the resolver does.
func readsBackAs(edited []byte, keyPath []string, value any) bool {
	// yaml.v3 parses the whole document before decoding, so an edit that does
	// not parse leaves current nil and fails the comparison.
	var current any
	_ = yaml.Unmarshal(edited, &current)
	for _, segment := range keyPath {
		mapping, _ := current.(map[string]any)
		current, _ = lookupKeyFold(mapping, segment)
	}
	return reflect.DeepEqual(current, value)
}

// parseYAMLDocument parses data into a yaml.Node document. An empty data set
// yields an empty document the caller fills in.
func parseYAMLDocument(path string, data []byte) (*yaml.Node, error) {
	document := &yaml.Node{Kind: yaml.DocumentNode}
	// gomutants:disable-next-line INTEGER_DECREMENT,BRANCH_IF reason="for empty data the guard only skips a yaml.Unmarshal that yields the same empty document; len cannot be -1"
	if len(bytes.TrimSpace(data)) == 0 {
		return document, nil
	}
	if err := yaml.Unmarshal(data, document); err != nil {
		return nil, yamlfmt.WrapErr(path, err)
	}
	return document, nil
}

// documentRootMapping returns the document's root mapping node, creating an
// empty one for an empty document. A root that is not a mapping cannot take a
// top-level key without discarding the user's content, so it is an error.
func documentRootMapping(document *yaml.Node) (*yaml.Node, error) {
	if len(document.Content) == 0 {
		root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		document.Content = []*yaml.Node{root}
		return root, nil
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("config file root is not a mapping")
	}
	return root, nil
}

// setMappingKey sets the dotted key to value on mapping, creating intermediate
// mappings as needed. Descending through an existing non-mapping value is an
// error rather than a silent overwrite.
func setMappingKey(mapping *yaml.Node, path []string, value any) error {
	segment := path[0]
	index := mappingKeyIndex(mapping, segment)
	if len(path) == 1 {
		encoded, err := encodeValue(value)
		if err != nil {
			return fmt.Errorf("encode %s: %w", strings.Join(path, "."), err)
		}
		if index >= 0 {
			copyComments(encoded, mapping.Content[index*2+1])
			mapping.Content[index*2+1] = encoded
		} else {
			mapping.Content = append(mapping.Content, scalarNode(segment), encoded)
		}
		return nil
	}
	if index < 0 {
		nested := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		mapping.Content = append(mapping.Content, scalarNode(segment), nested)
		return setMappingKey(nested, path[1:], value)
	}
	child := mapping.Content[index*2+1]
	if child.Kind != yaml.MappingNode {
		return fmt.Errorf("%s is not a mapping", segment)
	}
	return setMappingKey(child, path[1:], value)
}

// mappingKeyIndex returns the pair index of key in mapping's content, or -1.
// The key is matched case-insensitively, mirroring the owner scan and viper's
// case-insensitive resolution, so the value under an existing `Memory:` entry is
// replaced there (preserving that spelling) rather than added as a new key.
func mappingKeyIndex(mapping *yaml.Node, key string) int {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if strings.EqualFold(mapping.Content[i].Value, key) {
			return i / 2
		}
	}
	// gomutants:disable-next-line INTEGER_INCREMENT reason="a negative index is the not-found sentinel; every caller tests index >= 0 or index < 0, so -1 and -2 behave identically"
	return -1
}

// ensureReplaceableValue refuses to replace a value that is an anchor referenced
// elsewhere in the document: replacing the node would remove the anchor and leave
// the aliases dangling, which the next config read would reject.
func ensureReplaceableValue(root *yaml.Node, path []string) error {
	value := mappingValue(root, path)
	if value == nil || value.Anchor == "" {
		return nil
	}
	if aliasReferences(root, value.Anchor) {
		return fmt.Errorf("value %s is a YAML anchor referenced elsewhere; refusing to edit", strings.Join(path, "."))
	}
	return nil
}

// mappingValue returns the value node at the dotted path, or nil when the path
// does not resolve to a mapping entry.
func mappingValue(root *yaml.Node, path []string) *yaml.Node {
	current := root
	for _, segment := range path {
		// gomutants:disable-next-line BRANCH_IF reason="a non-mapping current has no Content pairs, so the following mappingKeyIndex returns -1 and the index<0 guard returns nil anyway"
		if current.Kind != yaml.MappingNode {
			return nil
		}
		index := mappingKeyIndex(current, segment)
		if index < 0 {
			return nil
		}
		current = current.Content[index*2+1]
	}
	return current
}

// aliasReferences reports whether any alias node in the document references
// anchor.
func aliasReferences(node *yaml.Node, anchor string) bool {
	if node.Kind == yaml.AliasNode && node.Value == anchor {
		return true
	}
	for _, child := range node.Content {
		if aliasReferences(child, anchor) {
			return true
		}
	}
	return false
}

// scalarNode returns a plain string scalar node.
func scalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

// copyComments transfers an existing value node's comments onto its replacement
// so that overwriting a key preserves the user's annotations on that key.
func copyComments(to, from *yaml.Node) {
	to.HeadComment = from.HeadComment
	to.LineComment = from.LineComment
	to.FootComment = from.FootComment
}

// encodeValue renders value as a node so the YAML encoder decides its style and
// tag. It returns the error a value's yaml.Marshaler/encoding.TextMarshaler
// reports; an unencodable type panics inside the encoder, which a caller
// passing one deserves.
func encodeValue(value any) (*yaml.Node, error) {
	encoded := &yaml.Node{}
	if err := encoded.Encode(value); err != nil {
		return nil, err
	}
	return encoded, nil
}
