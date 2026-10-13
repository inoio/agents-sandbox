package builder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
)

func TestBuilderIdleTimeoutHonoursOverride(t *testing.T) {
	if got := builderIdleTimeout(); got != builderDefaultIdleTimeout {
		t.Errorf("default builderIdleTimeout() = %s, want %s", got, builderDefaultIdleTimeout)
	}
	t.Setenv(builderIdleTimeoutEnv, "30m")
	if got := builderIdleTimeout(); got != 30*time.Minute {
		t.Errorf("builderIdleTimeout() = %s, want 30m", got)
	}
}

func TestBuilderIdleTimeoutRejectsInvalidOrTooShort(t *testing.T) {
	for _, raw := range []string{"not-a-duration", "0s", "1s"} {
		t.Setenv(builderIdleTimeoutEnv, raw)
		if got := builderIdleTimeout(); got != builderDefaultIdleTimeout {
			t.Errorf("builderIdleTimeout() with %q = %s, want default %s", raw, got, builderDefaultIdleTimeout)
		}
	}
}

func TestBuilderMemoryDefaultsToProductionValue(t *testing.T) {
	want := options.DefaultMemoryMiB
	if maxMiB := builderMaxMemoryMiB(); want > maxMiB {
		want = maxMiB
	}
	if got := builderMemoryMiB(); got != want {
		t.Errorf("builderMemoryMiB() = %d, want %d", got, want)
	}
}

func TestBuilderMemoryHonoursOverrideAndClampsToHostRAM(t *testing.T) {
	t.Setenv(builderMemoryEnv, "8192")
	maxMiB := builderMaxMemoryMiB()

	got := builderMemoryMiB()
	switch {
	case maxMiB >= 8192 && got != 8192:
		t.Errorf("builderMemoryMiB() = %d, want 8192", got)
	case maxMiB < 8192 && got != maxMiB:
		t.Errorf("builderMemoryMiB() = %d, want clamp to %d", got, maxMiB)
	}
}

func TestBuilderMemoryIgnoresGarbageOverride(t *testing.T) {
	t.Setenv(builderMemoryEnv, "not-a-number")
	want := options.DefaultMemoryMiB
	if maxMiB := builderMaxMemoryMiB(); want > maxMiB {
		want = maxMiB
	}
	if got := builderMemoryMiB(); got != want {
		t.Errorf("builderMemoryMiB() = %d, want %d", got, want)
	}
}

func TestBuilderCacheModeDefaultsToVolume(t *testing.T) {
	if got := builderCacheMode(); got != cacheModeVolume {
		t.Errorf("builderCacheMode() = %q, want %q", got, cacheModeVolume)
	}
	mount := builderCacheMount()
	if mount.Named != cacheVolumeName {
		t.Errorf("default cache mount named = %q, want %q", mount.Named, cacheVolumeName)
	}
}

func TestBuilderCacheModeBindAndTmpfs(t *testing.T) {
	t.Setenv(builderCacheModeEnv, cacheModeBind)
	if got := builderCacheMode(); got != cacheModeBind {
		t.Fatalf("builderCacheMode() = %q, want %q", got, cacheModeBind)
	}
	if mount := builderCacheMount(); mount.Bind != builderCacheBindPath() {
		t.Errorf("bind cache mount = %q, want %q", mount.Bind, builderCacheBindPath())
	}

	t.Setenv(builderCacheModeEnv, cacheModeTmpfs)
	t.Setenv(builderCacheSizeEnv, "2048")
	mount := builderCacheMount()
	if !mount.Tmpfs || mount.SizeMiB != 2048 {
		t.Errorf("tmpfs cache mount = %+v, want tmpfs with 2048 MiB", mount)
	}
}

func TestBuilderCacheModeFallsBackToVolumeOnUnknown(t *testing.T) {
	t.Setenv(builderCacheModeEnv, "btrfs")
	if got := builderCacheMode(); got != cacheModeVolume {
		t.Errorf("builderCacheMode() = %q, want fallback %q", got, cacheModeVolume)
	}
}

func TestBuilderCacheModeTmpfsExportUsesTmpfsWorkerOnly(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv(builderCacheModeEnv, cacheModeTmpfsExport)

	if got := builderCacheMode(); got != cacheModeTmpfsExport {
		t.Fatalf("builderCacheMode() = %q, want %q", got, cacheModeTmpfsExport)
	}
	if !builderCacheExportEnabled() {
		t.Fatal("builderCacheExportEnabled() = false, want true")
	}
	if worker := builderCacheMount(); !worker.Tmpfs {
		t.Errorf("worker mount = %+v, want tmpfs", worker)
	}

	var cfg msbSdk.SandboxConfig
	applySandboxOpts(&cfg, builderSandboxOptions(11234))
	if worker, ok := cfg.Volumes[cacheMountPath]; !ok || !worker.Tmpfs {
		t.Errorf("worker volume at %q = %+v, want tmpfs", cacheMountPath, worker)
	}
	if len(cfg.Volumes) != 1 {
		t.Errorf("expected only the worker mount, got %v", cfg.Volumes)
	}
	if path := builderCacheExportPath(); !filepath.IsAbs(path) {
		t.Errorf("builderCacheExportPath() = %q, want absolute host path", path)
	}
}

func TestBuilderCacheExportDisabledByDefault(t *testing.T) {
	if builderCacheExportEnabled() {
		t.Fatal("builderCacheExportEnabled() = true by default, want false")
	}
	imports, exports, err := builderCacheExportOptions()
	if err != nil || imports != nil || exports != nil {
		t.Errorf("cache options = %v/%v err=%v, want nil/nil/nil", imports, exports, err)
	}
}

func TestBuilderCacheExportOptionsUseHostPersistentPath(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv(builderCacheModeEnv, cacheModeTmpfsExport)

	dir := builderCacheExportPath()
	imports, exports, err := builderCacheExportOptions()
	if err != nil {
		t.Fatalf("builderCacheExportOptions: %v", err)
	}
	if len(imports) != 1 || imports[0].Attrs["src"] != dir {
		t.Errorf("imports = %+v, want src %q", imports, dir)
	}
	if len(exports) != 1 || exports[0].Attrs["dest"] != dir || exports[0].Attrs["mode"] != "max" {
		t.Errorf("exports = %+v, want dest %q mode max", exports, dir)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("export dir %q not prepared: %v", dir, err)
	}
}

func TestBuildkitdCommandSnapshotterOverride(t *testing.T) {
	if cmd := buildkitdCommand(); !strings.Contains(cmd, "--oci-worker-snapshotter native") {
		t.Errorf("default buildkitd command = %q, want native snapshotter", cmd)
	}
	t.Setenv(builderSnapshotterEnv, "overlayfs")
	if cmd := buildkitdCommand(); !strings.Contains(cmd, "--oci-worker-snapshotter overlayfs") {
		t.Errorf("overridden buildkitd command = %q, want overlayfs snapshotter", cmd)
	}
}
