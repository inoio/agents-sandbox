package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestPrivateHomeUsesDataDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	got := PrivateHome()
	want := filepath.Join(configpaths.Get().UserDataDir(), "msb")
	if got != want || filepath.Base(got) != "msb" {
		t.Errorf("PrivateHome() = %q, want %q", got, want)
	}
}

func TestApplyHomeSetsMSBHome(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	unsetEnv(t, "MSB_HOME")
	home, err := ApplyHome()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("MSB_HOME") != home {
		t.Errorf("MSB_HOME = %q, want %q", os.Getenv("MSB_HOME"), home)
	}
}
func TestApplyHomeWrapsSetenvError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("setenv boom")
	orig := setenv
	setenv = func(string, string) error { return wantErr }
	t.Cleanup(func() { setenv = orig })

	if _, err := ApplyHome(); !errors.Is(err, wantErr) {
		t.Fatalf("ApplyHome() error = %v, want %v", err, wantErr)
	}
}

func TestLegacyHomeUsesUserHome(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	home, err := configpaths.Get().UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	got, err := LegacyHome()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".microsandbox"); got != want {
		t.Errorf("LegacyHome() = %q, want %q", got, want)
	}
}

func TestLegacyHomePropagatesHomeError(t *testing.T) {
	configpaths.WithMockConfigPathsHomeError(t, errors.New("home unavailable"))

	if _, err := LegacyHome(); err == nil {
		t.Fatal("LegacyHome() error = nil, want home resolution error")
	}
}
