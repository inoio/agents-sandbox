package configpaths

import "testing"

// TestNewRealConfigPaths pins the seam the relocated test double builds on: the
// real resolver must be constructible without the package-internal type, and the
// bare-base helper must resolve an ordinary directory to the relative constant.
func TestNewRealConfigPaths(t *testing.T) {
	if NewRealConfigPaths() == nil {
		t.Fatal("nil real ConfigPaths")
	}
	if got := ProjectConfigBase(t.TempDir()); got != ProjectConfigDir {
		t.Errorf("ProjectConfigBase = %q, want %q", got, ProjectConfigDir)
	}
}

// TestNotInBareCheckoutReportsOutside pins the default locator: before the
// composition root installs one, no directory is inside a bare checkout.
func TestNotInBareCheckoutReportsOutside(t *testing.T) {
	if _, _, inside := notInBareCheckout(t.TempDir()); inside {
		t.Error("notInBareCheckout reported a directory inside a bare checkout")
	}
}
