package builder

import "testing"

func TestMsbBackendExportIsOCI(t *testing.T) {
	spec := (&msbBackend{}).Export("any")
	if spec.Type != "oci" || len(spec.Attrs) != 0 {
		t.Fatalf("msb export = %+v, want type oci with no attrs", spec)
	}
}

func TestNewBackendConstructsMsb(t *testing.T) {
	if _, ok := newBackend().(*msbBackend); !ok {
		t.Fatal("newBackend must construct the msb backend")
	}
}
