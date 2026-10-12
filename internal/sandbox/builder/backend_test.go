package builder

import "testing"

func TestMsbBackendExportIsOCI(t *testing.T) {
	spec := (&msbBackend{}).Export("any")
	if spec.Type != "oci" || len(spec.Attrs) != 0 {
		t.Fatalf("msb export = %+v, want type oci with no attrs", spec)
	}
}

func TestNewBackendDefaultsToMsb(t *testing.T) {
	t.Setenv(builderBackendEnv, "")
	if _, ok := newBackend().(*msbBackend); !ok {
		t.Fatal("want msbBackend")
	}
}

func TestNewBackendDockerEnv(t *testing.T) {
	t.Setenv(builderBackendEnv, builderBackendDocker)
	if _, ok := newBackend().(*dockerBackend); !ok {
		t.Fatal("want dockerBackend")
	}
}

func TestNewBackendUnknownFallsBackToMsb(t *testing.T) {
	t.Setenv(builderBackendEnv, "podman")
	if _, ok := newBackend().(*msbBackend); !ok {
		t.Fatal("unknown value must fall back to msb")
	}
}
