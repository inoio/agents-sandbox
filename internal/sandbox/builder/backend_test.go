package builder

import "testing"

func TestMsbBackendExportPushesToRegistry(t *testing.T) {
	spec := (&msbBackend{}).Export("host.microsandbox.internal:5000/agents-sandbox/runner-p:opencode-latest")
	if spec.Type != "image" {
		t.Fatalf("type = %q, want image", spec.Type)
	}
	if spec.Stream {
		t.Fatal("registry push must not stream a local output")
	}
	for key, want := range map[string]string{
		"name":              "host.microsandbox.internal:5000/agents-sandbox/runner-p:opencode-latest",
		"push":              "true",
		"oci-mediatypes":    "true",
		"registry.insecure": "true",
	} {
		if got := spec.Attrs[key]; got != want {
			t.Fatalf("attr %s = %q, want %q", key, got, want)
		}
	}
	if spec.Attrs["compression"] != builderCompression {
		t.Errorf("export compression = %q, want %q", spec.Attrs["compression"], builderCompression)
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
