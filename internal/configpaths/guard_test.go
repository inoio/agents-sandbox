package configpaths

import "testing"

func TestGetConfigPathsFailFastDefault(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("GetConfigPaths() should panic by default in tests when no mock is installed, but did not")
		}
	}()
	Get().UserConfigDir()
}
