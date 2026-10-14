package builder

import "testing"

func TestMsbBackendExportIsOCI(t *testing.T) {
	spec := (&msbBackend{}).Export("any")
	if spec.Type != "oci" {
		t.Fatalf("msb export type = %q, want oci", spec.Type)
	}
	if spec.Attrs["compression"] != builderCompression {
		t.Errorf("export compression = %q, want %q", spec.Attrs["compression"], builderCompression)
	}
	if spec.Attrs["oci-mediatypes"] != "true" {
		t.Errorf("export oci-mediatypes = %q, want true", spec.Attrs["oci-mediatypes"])
	}
}

func TestMsbBackendExportCompressionOverrides(t *testing.T) {
	t.Setenv(builderCompressionEnv, "uncompressed")
	attrs := (&msbBackend{}).Export("any").Attrs
	if attrs["compression"] != "uncompressed" {
		t.Errorf("export compression = %q, want the env override", attrs["compression"])
	}
	if _, ok := attrs["compression-level"]; ok {
		t.Errorf("compression-level = %q, want absent without the level env", attrs["compression-level"])
	}

	t.Setenv(builderCompressionLevelEnv, "1")
	if got := (&msbBackend{}).Export("any").Attrs["compression-level"]; got != "1" {
		t.Errorf("compression-level = %q, want the level env override", got)
	}
}

func TestNewBackendConstructsMsb(t *testing.T) {
	if _, ok := newBackend().(*msbBackend); !ok {
		t.Fatal("newBackend must construct the msb backend")
	}
}
