package upgrade

import (
	"testing"
)

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

func TestRunningUnderHomebrewFalseForTestBinary(t *testing.T) {
	// The go test binary never lives in a Homebrew keg, so the real detector
	// must report false; a false positive here would suppress upgrades for
	// every non-Homebrew install.
	if runningUnderHomebrew() {
		t.Fatal("runningUnderHomebrew() = true for the go test binary, want false")
	}
}
