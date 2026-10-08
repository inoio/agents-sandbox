package configfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/titanous/json5"
	"gopkg.in/yaml.v3"
)

const (
	yamlIndent        = 2
	defaultConfigFile = "config.yaml"
	extYAML           = ".yaml"
	extYML            = ".yml"
	extJSON           = ".json"
	extJSONC          = ".jsonc"
	extJSON5          = ".json5"
)

//nolint:gochecknoglobals // package-local seams restored by tests
var (
	marshalJSONIndentFn = json.MarshalIndent
	marshalYAMLFn       = marshalYAML
)

// Config is a parsed launcher config file that can be edited and written back.
// YAML files are edited on their node tree, so comments and key order survive;
// JSON-family files are rewritten from their decoded values and lose comments
// and formatting.
type Config struct {
	values map[string]any
	// yamlDocument is nil for JSON-family files.
	yamlDocument *yaml.Node
}

// Parse decodes a launcher config file. The path only selects the parser:
// JSON-family extensions are parsed as JSON5, everything else as YAML.
func Parse(path string, data []byte) (*Config, error) {
	var values map[string]any
	var yamlDocument *yaml.Node
	if IsJSON(path) {
		if err := json5.Unmarshal(data, &values); err != nil {
			return nil, fmt.Errorf("parse launcher config %s: %w", path, err)
		}
	} else {
		yamlDocument = &yaml.Node{}
		if err := yaml.Unmarshal(data, yamlDocument); err != nil {
			return nil, fmt.Errorf("parse launcher config %s: %w", path, err)
		}
		if err := yamlDocument.Decode(&values); err != nil {
			return nil, fmt.Errorf("parse launcher config %s: %w", path, err)
		}
	}
	if values == nil {
		values = make(map[string]any)
	}
	return &Config{values: values, yamlDocument: yamlDocument}, nil
}

// IsJSON reports whether the path has a JSON-family extension.
func IsJSON(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == extJSON || ext == extJSONC || ext == extJSON5
}

// LoadDir finds config.{yaml,yml,json,jsonc,json5} in dir in that order,
// returning the default path and an empty mapping when none exists.
func LoadDir(dir string) (string, []byte, error) {
	for _, ext := range []string{extYAML, extYML, extJSON, extJSONC, extJSON5} {
		path := filepath.Join(dir, "config"+ext)
		data, err := os.ReadFile(path)
		if err == nil {
			return path, data, nil
		}
		if !os.IsNotExist(err) {
			return "", nil, fmt.Errorf("read launcher config %s: %w", path, err)
		}
	}
	return filepath.Join(dir, defaultConfigFile), []byte("{}\n"), nil
}

// Values returns the decoded config mapping.
func (c *Config) Values() map[string]any {
	return c.values
}

// GetString returns the value at the nested key path as a string and whether
// the key is present.
func (c *Config) GetString(keyPath []string) (string, bool) {
	current := any(c.values)
	for _, key := range keyPath {
		mapping, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		value, ok := mapping[key]
		if !ok {
			return "", false
		}
		current = value
	}
	switch value := current.(type) {
	case string:
		return value, true
	case nil:
		return "", true
	default:
		return fmt.Sprint(value), true
	}
}

// SetString sets the string at the nested key path, creating missing mappings.
func (c *Config) SetString(keyPath []string, value string) {
	valueNode := &yaml.Node{}
	valueNode.SetString(value)
	c.setValue(keyPath, value, valueNode)
}

// SetBool sets the boolean at the nested key path, creating missing mappings.
func (c *Config) SetBool(keyPath []string, value bool) {
	c.setValue(keyPath, value, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(value)})
}

// SetInt sets the integer at the nested key path, creating missing mappings.
func (c *Config) SetInt(keyPath []string, value int) {
	c.setValue(keyPath, value, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(value)})
}

func (c *Config) setValue(keyPath []string, value any, valueNode *yaml.Node) {
	setMapValue(c.values, keyPath, value)
	if c.yamlDocument == nil {
		return
	}
	parent := yamlMappingAt(c.yamlRoot(), keyPath[:len(keyPath)-1])
	setYAMLMappingValue(parent, keyPath[len(keyPath)-1], valueNode)
}

// AppendListItems appends items to the list at the nested key path. An
// existing YAML sequence is extended in place to keep its comments; any other
// value is replaced by a sequence of its entries followed by the items.
func (c *Config) AppendListItems(keyPath []string, items []string) {
	parentValues := mapAt(c.values, keyPath[:len(keyPath)-1])
	key := keyPath[len(keyPath)-1]
	list := append(stringList(parentValues[key]), items...)
	parentValues[key] = list
	if c.yamlDocument == nil {
		return
	}
	parent := yamlMappingAt(c.yamlRoot(), keyPath[:len(keyPath)-1])
	sequence := yamlMappingValue(parent, key)
	if sequence == nil || sequence.Kind != yaml.SequenceNode {
		sequence = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		setYAMLMappingValue(parent, key, sequence)
		items = list
	}
	for _, item := range items {
		itemNode := &yaml.Node{}
		itemNode.SetString(item)
		sequence.Content = append(sequence.Content, itemNode)
	}
}

// FilterListItems drops items from the list at the nested key path for which
// keep returns false. An existing YAML sequence is filtered in place to keep
// its comments; any other value is replaced by a sequence of the kept entries.
func (c *Config) FilterListItems(keyPath []string, keep func(string) bool) {
	parentValues := mapAt(c.values, keyPath[:len(keyPath)-1])
	key := keyPath[len(keyPath)-1]
	var kept []string
	for _, item := range stringList(parentValues[key]) {
		if keep(item) {
			kept = append(kept, item)
		}
	}
	if len(kept) == 0 {
		delete(parentValues, key)
	} else {
		parentValues[key] = kept
	}
	if c.yamlDocument == nil {
		return
	}
	parent := yamlMappingAt(c.yamlRoot(), keyPath[:len(keyPath)-1])
	sequence := yamlMappingValue(parent, key)
	if sequence == nil || sequence.Kind != yaml.SequenceNode {
		if len(kept) == 0 {
			removeYAMLMappingKey(parent, key)
			return
		}
		sequence = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		setYAMLMappingValue(parent, key, sequence)
		for _, item := range kept {
			itemNode := &yaml.Node{}
			itemNode.SetString(item)
			sequence.Content = append(sequence.Content, itemNode)
		}
		return
	}
	var content []*yaml.Node
	for _, item := range sequence.Content {
		if keep(item.Value) {
			content = append(content, item)
		}
	}
	sequence.Content = content
	if len(sequence.Content) == 0 {
		removeYAMLMappingKey(parent, key)
	}
}

// Marshal encodes the config, preserving YAML comments and key order.
func (c *Config) Marshal() ([]byte, error) {
	if c.yamlDocument == nil {
		data, err := marshalJSONIndentFn(c.values, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal launcher config: %w", err)
		}
		return append(data, '\n'), nil
	}
	data, err := marshalYAMLFn(c.yamlDocument)
	if err != nil {
		return nil, fmt.Errorf("marshal launcher config: %w", err)
	}
	return data, nil
}

// WriteAtomic writes data to path through a temporary file in the same
// directory, then renames it into place.
func WriteAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agents-sandbox-migrate-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	return commitTempFile(tmp, path, data, mode)
}

// commitTempFile writes data to the temporary file, syncs and closes it, and
// moves it to path. The file is always closed, also when an earlier step fails.
func commitTempFile(tmp *os.File, path string, data []byte, mode os.FileMode) error {
	chmodErr := tmp.Chmod(mode)
	_, writeErr := tmp.Write(data)
	if err := errors.Join(chmodErr, writeErr, tmp.Sync(), tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// yamlRoot returns the document's top-level mapping, creating it for an
// empty file.
func (c *Config) yamlRoot() *yaml.Node {
	if c.yamlDocument.Kind != yaml.DocumentNode || len(c.yamlDocument.Content) == 0 {
		c.yamlDocument.Kind = yaml.DocumentNode
		c.yamlDocument.Content = []*yaml.Node{newYAMLMapping()}
	}
	root := c.yamlDocument.Content[0]
	if root.Kind != yaml.MappingNode {
		*root = *newYAMLMapping()
	}
	return root
}

func marshalYAML(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(yamlIndent)
	encodeErr := encoder.Encode(value)
	if err := errors.Join(encodeErr, encoder.Close()); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func mapAt(values map[string]any, keyPath []string) map[string]any {
	for _, key := range keyPath {
		child, ok := values[key].(map[string]any)
		if !ok {
			child = make(map[string]any)
			values[key] = child
		}
		values = child
	}
	return values
}

func setMapValue(values map[string]any, keyPath []string, value any) {
	mapAt(values, keyPath[:len(keyPath)-1])[keyPath[len(keyPath)-1]] = value
}

func yamlMappingAt(mapping *yaml.Node, keyPath []string) *yaml.Node {
	for _, key := range keyPath {
		child := yamlMappingValue(mapping, key)
		if child == nil || child.Kind != yaml.MappingNode {
			child = newYAMLMapping()
			setYAMLMappingValue(mapping, key, child)
		}
		mapping = child
	}
	return mapping
}

func newYAMLMapping() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

func yamlMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

// setYAMLMappingValue replaces the value of key, keeping its comments, or
// appends the key. A flow-style mapping like the default "{}" becomes block
// style so that added keys are written one per line.
func setYAMLMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value != key {
			continue
		}
		previous := mapping.Content[index+1]
		value.HeadComment = previous.HeadComment
		value.LineComment = previous.LineComment
		value.FootComment = previous.FootComment
		mapping.Content[index+1] = value
		return
	}
	mapping.Style &^= yaml.FlowStyle
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	mapping.Content = append(mapping.Content, keyNode, value)
}

func removeYAMLMappingKey(mapping *yaml.Node, key string) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value != key {
			continue
		}
		mapping.Content = append(mapping.Content[:index], mapping.Content[index+2:]...)
		return
	}
}

// stringList reads a config list given as a sequence or a comma-separated
// string, trimming each entry and dropping empty ones.
func stringList(value any) []string {
	var entries []string
	switch values := value.(type) {
	case []string:
		entries = values
	case []any:
		for _, value := range values {
			if text, ok := value.(string); ok {
				entries = append(entries, text)
			}
		}
	case string:
		entries = strings.Split(values, ",")
	}
	var result []string
	for _, entry := range entries {
		if trimmed := strings.TrimSpace(entry); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
