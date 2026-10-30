package state

import (
	"errors"
	"fmt"

	"go.yaml.in/yaml/v3"
)

// HomeStateSchema and RootStateSchema are the persisted schema versions this
// binary reads and writes. Bump one whenever its document's persisted fields
// change (TestHomeStateSchemaFieldsArePinned / TestRootStateSchemaFieldsArePinned
// enforce it): a binary refuses to mutate a document with a newer schema, so an
// older revision can never round-trip a newer revision's fields away (#337).
//
// Schema 0 (no schema: key) is the unversioned document written before
// versioning; it is read as the current schema and stamped on the next write.
const (
	HomeStateSchema = 1
	RootStateSchema = 1
)

// ErrStateSchemaUnsupported classifies a state document written by a newer
// agents-sandbox with a schema this binary does not know. It is deliberately
// distinct from ErrStateUnparsable: the document is valid, so no recovery path
// may discard it.
var ErrStateSchemaUnsupported = errors.New("state file was written by a newer agents-sandbox; upgrade agents-sandbox")

// versionedDocument is the on-disk envelope of a state document: the schema
// key followed by the document's own fields inline. The version lives only in
// the envelope, so the in-memory documents never carry (or compare) it.
type versionedDocument[T any] struct {
	Schema   int `yaml:"schema"`
	Document T   `yaml:",inline"`
}

// checkDocumentSchema reads a document's schema key and validates it against
// the newest schema this binary supports. A missing key is schema 0.
func checkDocumentSchema(data []byte, supportedSchema int) error {
	var header struct {
		Schema int `yaml:"schema"`
	}
	if err := yaml.Unmarshal(data, &header); err != nil {
		return fmt.Errorf("%w: %w", ErrStateUnparsable, err)
	}
	return checkSchema(header.Schema, supportedSchema)
}

func checkSchema(documentSchema, supportedSchema int) error {
	if documentSchema < 0 {
		return fmt.Errorf("%w: invalid schema %d", ErrStateUnparsable, documentSchema)
	}
	if documentSchema > supportedSchema {
		return fmt.Errorf("%w (schema %d, supported %d)", ErrStateSchemaUnsupported, documentSchema, supportedSchema)
	}
	return nil
}
