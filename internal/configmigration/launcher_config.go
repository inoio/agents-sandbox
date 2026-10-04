package configmigration

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"

	"github.com/titanous/json5"
	"gopkg.in/yaml.v3"
)

const yamlIndent = 2

// launcherConfig is a parsed launcher config file that can be edited and
// written back. YAML files are edited on their node tree, so comments and key
// order survive; JSON-family files are rewritten from their decoded values and
// lose comments and formatting.
type launcherConfig struct {
	values map[string]any
	// yamlDocument is nil for JSON-family files.
	yamlDocument *yaml.Node
}

func parseLauncherConfig(path string, data []byte) (*launcherConfig, error) {
	var values map[string]any
	var yamlDocument *yaml.Node
	if migrationIsJSONConfig(path) {
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
	return &launcherConfig{values: values, yamlDocument: yamlDocument}, nil
}

// setString sets the string at the nested key path, creating missing mappings.
func (c *launcherConfig) setString(keyPath []string, value string) {
	valueNode := &yaml.Node{}
	valueNode.SetString(value)
	c.setValue(keyPath, value, valueNode)
}

// setBool sets the boolean at the nested key path, creating missing mappings.
func (c *launcherConfig) setBool(keyPath []string, value bool) {
	c.setValue(keyPath, value, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(value)})
}

func (c *launcherConfig) setValue(keyPath []string, value any, valueNode *yaml.Node) {
	setMapValue(c.values, keyPath, value)
	if c.yamlDocument == nil {
		return
	}
	parent := yamlMappingAt(c.yamlRoot(), keyPath[:len(keyPath)-1])
	setYAMLMappingValue(parent, keyPath[len(keyPath)-1], valueNode)
}

// appendListItems appends items to the list at the nested key path. An
// existing YAML sequence is extended in place to keep its comments; any other
// value is replaced by a sequence of its entries followed by the items.
func (c *launcherConfig) appendListItems(keyPath []string, items []string) {
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

func (c *launcherConfig) marshal() ([]byte, error) {
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

// yamlRoot returns the document's top-level mapping, creating it for an
// empty file.
func (c *launcherConfig) yamlRoot() *yaml.Node {
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
