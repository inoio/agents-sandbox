package state

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"

	"go.yaml.in/yaml/v3"
)

// homeStateSchemaFields and rootStateSchemaFields pin the persisted field set of
// every schema version, keyed by version. A field change that does not bump the
// schema lets an older binary read the new document as current and silently
// erase the new field on its next write (#337). When the test fails, bump
// HomeStateSchema or RootStateSchema and add the new field set under the new
// version; released versions' entries are history and must not be edited.
var (
	homeStateSchemaFields = map[int][]string{1: {
		"agent_source",
		"agent_version",
		"docker_source",
		"env_state",
		"env_state.hash",
		"env_state.names",
		"home_volume",
		"image_digest",
		"mount_state",
		"mount_state.hash",
		"mount_state.names",
		"network_state",
		"network_state.hash",
		"network_state.names",
		"offered_versions",
		"recovered_home_volume",
		"schema",
		"secret_state",
		"secret_state.hash",
		"secret_state.names",
	}}
	rootStateSchemaFields = map[int][]string{1: {
		"adopted_home_volumes",
		"adopted_home_volumes[].checkout_slug",
		"adopted_home_volumes[].volume",
		"migrations",
		"migrations[].copied",
		"migrations[].excluded",
		"migrations[].kept",
		"migrations[].status",
		"schema",
		"updates",
		"updates.agents",
		"updates.agents[].last_checked",
		"updates.agents[].latest_version",
		"updates.launcher",
		"updates.launcher.dismissed_versions",
		"updates.launcher.last_checked",
	}}
)

func TestHomeStateSchemaFieldsArePinned(t *testing.T) {
	assertSchemaFields(t, "HomeStateSchema", HomeStateSchema, reflect.TypeFor[HomeState](), homeStateSchemaFields)
}

func TestRootStateSchemaFieldsArePinned(t *testing.T) {
	assertSchemaFields(t, "RootStateSchema", RootStateSchema, reflect.TypeFor[RootState](), rootStateSchemaFields)
}

func assertSchemaFields(
	t *testing.T, schemaConstant string, schema int, documentType reflect.Type, pinnedByVersion map[int][]string,
) {
	t.Helper()
	pinned, ok := pinnedByVersion[schema]
	if !ok {
		t.Fatalf("no pinned field set for %s = %d; add one", schemaConstant, schema)
	}
	got := append(yamlFieldPaths(documentType, ""), "schema")
	slices.Sort(got)
	if !slices.Equal(got, pinned) {
		t.Fatalf("persisted fields differ from the pinned set of %s = %d; bump %s and pin the new set "+
			"under the new version.\ngot:    %v\npinned: %v", schemaConstant, schema, schemaConstant, got, pinned)
	}
}

// yamlFieldPaths lists the yaml key paths of a persisted struct, descending into
// nested structs and map values ("[]" marks a map value), so a nested field
// change is caught as well as a top-level one.
func yamlFieldPaths(structType reflect.Type, prefix string) []string {
	var paths []string
	for field := range structType.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		path := prefix + name
		paths = append(paths, path)
		paths = append(paths, nestedYAMLFieldPaths(field.Type, path)...)
	}
	return paths
}

func nestedYAMLFieldPaths(fieldType reflect.Type, path string) []string {
	switch {
	case fieldType.Kind() == reflect.Map:
		return nestedYAMLFieldPaths(fieldType.Elem(), path+"[]")
	case fieldType.Kind() == reflect.Struct && fieldType != reflect.TypeFor[time.Time]():
		return yamlFieldPaths(fieldType, path+".")
	default:
		return nil
	}
}

func TestParseRejectsNewerSchema(t *testing.T) {
	newer := []byte("schema: 99\nhome_volume: vol\n")
	if _, err := parseHomeState(newer); !errors.Is(err, ErrStateSchemaUnsupported) {
		t.Errorf("parseHomeState error = %v, want ErrStateSchemaUnsupported", err)
	}
	if _, err := parseRootState(newer); !errors.Is(err, ErrStateSchemaUnsupported) {
		t.Errorf("parseRootState error = %v, want ErrStateSchemaUnsupported", err)
	}
}

// TestParseRejectsNewerSchemaWithChangedFieldShapes pins that the schema is
// checked before decoding: a newer schema may change an existing field's shape,
// and classifying that document as unparsable would let recovery discard it.
func TestParseRejectsNewerSchemaWithChangedFieldShapes(t *testing.T) {
	for name, parse := range map[string]func([]byte) error{
		"parseHomeState": func(data []byte) error { _, err := parseHomeState(data); return err },
		"parseRootState": func(data []byte) error { _, err := parseRootState(data); return err },
	} {
		for _, document := range []string{
			"schema: 99\nhome_volume: {name: vol}\n",
			"schema: 99\nmigrations: [runtime_home]\nagents: [opencode]\n",
		} {
			err := parse([]byte(document))
			if !errors.Is(err, ErrStateSchemaUnsupported) || errors.Is(err, ErrStateUnparsable) {
				t.Errorf("%s(%q) error = %v, want ErrStateSchemaUnsupported only", name, document, err)
			}
		}
	}
}

func TestParseAcceptsUnversionedAndCurrentSchema(t *testing.T) {
	for _, document := range []string{"home_volume: vol\n", "schema: 1\nhome_volume: vol\n"} {
		if _, err := parseHomeState([]byte(document)); err != nil {
			t.Errorf("parseHomeState(%q) = %v", document, err)
		}
		if _, err := parseRootState([]byte(document)); err != nil {
			t.Errorf("parseRootState(%q) = %v", document, err)
		}
	}
}

func TestParseRejectsNegativeSchemaAsUnparsable(t *testing.T) {
	if _, err := parseHomeState([]byte("schema: -1\n")); !errors.Is(err, ErrStateUnparsable) {
		t.Errorf("parseHomeState error = %v, want ErrStateUnparsable", err)
	}
	if _, err := parseRootState([]byte("schema: -1\n")); !errors.Is(err, ErrStateUnparsable) {
		t.Errorf("parseRootState error = %v, want ErrStateUnparsable", err)
	}
}

// TestCheckDocumentSchemaReportsMalformedDocumentsAsUnparsable pins that a
// schema header the YAML decoder cannot read is classified ErrStateUnparsable
// — the classification RepairRootState quarantines on — and that the decoder's
// cause stays reachable through the error chain.
func TestCheckDocumentSchemaReportsMalformedDocumentsAsUnparsable(t *testing.T) {
	for name, document := range map[string]string{
		"syntax error": "schema: [\n",
		"type error":   "schema: [1, 2]\n",
	} {
		if err := checkDocumentSchema([]byte(document), RootStateSchema); !errors.Is(err, ErrStateUnparsable) {
			t.Errorf("%s: checkDocumentSchema error = %v, want ErrStateUnparsable", name, err)
		}
	}

	err := checkDocumentSchema([]byte("schema: [1, 2]\n"), RootStateSchema)
	if _, ok := errors.AsType[*yaml.TypeError](err); !ok {
		t.Errorf("checkDocumentSchema error = %v, want its *yaml.TypeError cause reachable", err)
	}

	// A document from a newer schema is refused, not quarantined as unparsable.
	if err := checkDocumentSchema([]byte("schema: 1\n"), 0); !errors.Is(err, ErrStateSchemaUnsupported) {
		t.Errorf("checkDocumentSchema with an older supported schema = %v, want ErrStateSchemaUnsupported", err)
	}
}

func TestWriteStampsCurrentSchema(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}
	if err := writeState(k, HomeState{HomeVolume: "vol"}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	if err := writeRootState(RootState{}); err != nil {
		t.Fatalf("writeRootState: %v", err)
	}
	assertFileSchema(t, stateFile(k), HomeStateSchema)
	assertFileSchema(t, rootStateFile(), RootStateSchema)
}

func assertFileSchema(t *testing.T, path string, want int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var header struct {
		Schema int `yaml:"schema"`
	}
	if err := yaml.Unmarshal(data, &header); err != nil || header.Schema != want {
		t.Errorf("%s schema = %d (err %v), want %d; document:\n%s", path, header.Schema, err, want, data)
	}
}

func TestTransactionsRefuseNewerSchemaWithoutWriting(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}
	newer := []byte("schema: 99\nhome_volume: vol\nfuture_field: keep\n")
	seedFile(t, stateFile(k), newer)
	seedFile(t, rootStateFile(), newer)

	for name, run := range map[string]func() error{
		"WithHomeState": func() error {
			return WithHomeState(k, func(*HomeState, bool) (bool, error) { return true, nil })
		},
		"RecoverHomeState": func() error {
			return RecoverHomeState(k, func(*HomeState, bool) (bool, error) { return true, nil })
		},
		"WithRootState": func() error {
			_, err := WithRootState(true, func(txn *RootTxn) error { return txn.Commit() })
			return err
		},
	} {
		if err := run(); !errors.Is(err, ErrStateSchemaUnsupported) {
			t.Errorf("%s error = %v, want ErrStateSchemaUnsupported", name, err)
		}
	}
	for _, path := range []string{stateFile(k), rootStateFile()} {
		if got, _ := os.ReadFile(path); string(got) != string(newer) {
			t.Errorf("%s was rewritten: %q", path, got)
		}
	}
}

func seedFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}
}
