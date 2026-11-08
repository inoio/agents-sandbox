package state

import (
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

// relativeConfigPaths is a ConfigPaths whose roots are relative, standing in for
// the case where the state root fails to resolve (an unset $HOME, or a
// mutation-sweep helper returning the zero value). Every state path derived from
// it is relative to the process working directory, which is exactly what the
// absolute-path guards exist to reject.
type relativeConfigPaths struct{}

func (relativeConfigPaths) UserConfigDir() string                { return "cfg" }
func (relativeConfigPaths) UserSlugConfigDir(slug string) string { return filepath.Join("cfg", slug) }
func (relativeConfigPaths) UserCacheDir() string                 { return "cache" }
func (relativeConfigPaths) UserStateDir() string                 { return "state" }
func (relativeConfigPaths) UserDataDir() string                  { return "data" }
func (relativeConfigPaths) UserHomeDir() (string, error)         { return "home", nil }
func (relativeConfigPaths) ProjectConfigDir() string             { return ".agents-sandbox" }
func (relativeConfigPaths) ProjectConfigDirAbs() string          { return "" }
func (relativeConfigPaths) ProjectDockerfile() string {
	return filepath.Join(".agents-sandbox", "Dockerfile")
}
func (relativeConfigPaths) IgnoredWorktreeConfigDir() string { return "" }

// withRelativeStateRoot points the config-path factory at relativeConfigPaths
// and pins the CWD to a temp dir, so a write made with the guard removed lands
// in the temp tree rather than the package directory (the mutation-sweep
// residue this guards against).
func withRelativeStateRoot(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths { return relativeConfigPaths{} }
	t.Cleanup(func() { configpaths.Get = orig })
}

func TestRequireAbsoluteStatePath(t *testing.T) {
	if err := requireAbsoluteStatePath(filepath.Join(t.TempDir(), "state.yaml")); err != nil {
		t.Errorf("requireAbsoluteStatePath(absolute) = %v, want nil", err)
	}
	if err := requireAbsoluteStatePath("state.yaml"); err == nil {
		t.Error("requireAbsoluteStatePath(relative) = nil, want a rejection")
	}
}

func TestStateWriteFunnelsRejectARelativeRoot(t *testing.T) {
	k := Key{Slug: "proj", Agent: "opencode"}

	// Each call returns the (release, ok, err) tuple its funnel returns. A
	// rejection must set ok=false and a nil release as well as a non-nil error:
	// a caller that reads the tuple (rather than checking err first) must not be
	// handed a true or a release func on a failed acquire.
	tests := []struct {
		name string
		call func() (release func(), ok bool, err error)
	}{
		{"writeState", func() (func(), bool, error) {
			return nil, false, writeState(k, HomeState{HomeVolume: "vol"})
		}},
		{"lockHomeState", func() (func(), bool, error) {
			return lockHomeState(k, true)
		}},
		{"lockProjectVM", func() (func(), bool, error) {
			return lockProjectVM(k, true)
		}},
		{"lockRootState", func() (func(), bool, error) {
			return lockRootState(true)
		}},
		{"AcquireClaim", func() (func(), bool, error) {
			return AcquireClaim(k.Slug, "token")
		}},
		{"AcquireProjectVMLock", func() (func(), bool, error) {
			release, err := AcquireProjectVMLock(k)
			return release, false, err
		}},
		{"AcquireClientLease", func() (func(), bool, error) {
			release, err := AcquireClientLease(k)
			return release, false, err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withRelativeStateRoot(t)
			release, ok, err := tt.call()
			if err == nil {
				t.Fatalf("%s with a relative state root = nil error, want a rejection", tt.name)
			}
			if ok {
				t.Errorf("%s with a relative state root = ok true, want false on a rejection", tt.name)
			}
			if release != nil {
				release()
				t.Errorf("%s with a relative state root = non-nil release, want nil on a rejection", tt.name)
			}
		})
	}
}
