package builder

import (
	"os"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestBuilderRecordRoundTrip(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	if err := writeBuilderRecord("n", 4242); err != nil {
		t.Fatalf("writeBuilderRecord: %v", err)
	}

	record, ok := readBuilderRecord()
	if !ok {
		t.Fatal("readBuilderRecord reported absent after a write")
	}
	if record.Name != "n" || record.HostPort != 4242 {
		t.Errorf("readBuilderRecord = %+v, want {Name:n HostPort:4242}", record)
	}

	info, err := os.Stat(builderRecordPath())
	if err != nil {
		t.Fatalf("stat builder record: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("builder record mode = %o, want 600", perm)
	}
}

func TestBuilderRecordAbsentOrCorrupt(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	if _, ok := readBuilderRecord(); ok {
		t.Fatal("readBuilderRecord reported present with no file")
	}

	if err := os.WriteFile(builderRecordPath(), []byte("not json"), 0o600); err != nil {
		t.Fatalf("write garbage record: %v", err)
	}
	if _, ok := readBuilderRecord(); ok {
		t.Fatal("readBuilderRecord reported present for a corrupt file")
	}
}
