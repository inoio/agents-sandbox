package upgrade

import (
	"errors"
	"testing"
)

// errBoom marks the failure cases exercised in TestUnderCellar.
var errBoom = errors.New("boom")

func TestPathUnderHomebrewCellar(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"apple silicon keg", "/opt/homebrew/Cellar/agents-sandbox/0.3.1/bin/agents-sandbox", true},
		{"intel keg", "/usr/local/Cellar/agents-sandbox/0.3.1/bin/agents-sandbox", true},
		{"linuxbrew keg", "/home/linuxbrew/.linuxbrew/Cellar/agents-sandbox/0.3.1/bin/agents-sandbox", true},
		{"homebrew bin symlink", "/opt/homebrew/bin/agents-sandbox", false},
		{"manual install", "/Users/me/.local/bin/agents-sandbox", false},
		{"cellar-like binary name", "/opt/Cellarfoo/bin/agents-sandbox", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pathUnderHomebrewCellar(tc.path); got != tc.want {
				t.Fatalf("pathUnderHomebrewCellar(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestUnderCellar(t *testing.T) {
	const cellar = "/opt/homebrew/Cellar/agents-sandbox/0.3.1/bin/agents-sandbox"
	const symlink = "/opt/homebrew/bin/agents-sandbox"
	const other = "/Users/me/.local/bin/agents-sandbox"

	failingResolve := func(string) (string, error) {
		t.Fatal("resolve must not be called on a short-circuit path")
		return "", nil
	}

	tests := []struct {
		name    string
		exe     func() (string, error)
		resolve func(string) (string, error)
		want    bool
	}{
		{
			name:    "executable lookup fails",
			exe:     func() (string, error) { return "", errBoom },
			resolve: failingResolve,
			want:    false,
		},
		{
			name:    "invoked path is already a keg",
			exe:     func() (string, error) { return cellar, nil },
			resolve: failingResolve,
			want:    true,
		},
		{
			name:    "resolved target is a keg",
			exe:     func() (string, error) { return symlink, nil },
			resolve: func(string) (string, error) { return cellar, nil },
			want:    true,
		},
		{
			name:    "resolved target is not a keg",
			exe:     func() (string, error) { return other, nil },
			resolve: func(string) (string, error) { return other, nil },
			want:    false,
		},
		{
			name:    "symlink resolution fails",
			exe:     func() (string, error) { return symlink, nil },
			resolve: func(string) (string, error) { return "", errBoom },
			want:    false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := underCellar(tc.exe, tc.resolve); got != tc.want {
				t.Fatalf("underCellar() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRunningUnderHomebrewFalseForTestBinary(t *testing.T) {
	// The go test binary never lives in a Homebrew keg, so the real detector
	// must report false; a false positive here would suppress upgrades for
	// every non-Homebrew install.
	if runningUnderHomebrew() {
		t.Fatal("runningUnderHomebrew() = true for the go test binary, want false")
	}
}
