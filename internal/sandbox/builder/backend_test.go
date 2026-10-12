package builder

import "testing"

func TestMsbBackendExportIsOCI(t *testing.T) {
	spec := (&msbBackend{}).Export("any")
	if spec.Type != "oci" || len(spec.Attrs) != 0 {
		t.Fatalf("msb export = %+v, want type oci with no attrs", spec)
	}
}
