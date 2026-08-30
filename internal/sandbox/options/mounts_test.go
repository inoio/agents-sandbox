package options

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeMountsShortAndLongForms(t *testing.T) {
	got, err := DecodeMounts(map[string]any{
		"/home/dev/.m2": "~/.m2",
		"/home/dev/ref": map[string]any{
			"source":   "/opt/company/reference",
			"readonly": true,
		},
	})
	if err != nil {
		t.Fatalf("DecodeMounts: %v", err)
	}
	if mount := got["/home/dev/.m2"]; mount.Source != "~/.m2" || mount.Readonly {
		t.Errorf("short mount = %+v", mount)
	}
	if mount := got["/home/dev/ref"]; mount.Source != "/opt/company/reference" || !mount.Readonly {
		t.Errorf("long mount = %+v", mount)
	}
}

func TestDecodeMountsRejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		raw  any
	}{
		{"not a mapping", []string{"~/.m2"}},
		{"invalid value", map[string]any{"/home/dev/.m2": true}},
		{"source not string", map[string]any{"/home/dev/.m2": map[string]any{"source": true}}},
		{"readonly not boolean", map[string]any{"/home/dev/.m2": map[string]any{"readonly": "yes"}}},
		{"unknown field", map[string]any{"/home/dev/.m2": map[string]any{"read-only": true}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeMounts(tc.raw); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestDecodeMountsNil(t *testing.T) {
	got, err := DecodeMounts(nil)
	if err != nil {
		t.Fatalf("DecodeMounts: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("DecodeMounts(nil) = %+v, want empty", got)
	}
}

func TestResolveBindMountsExpandsHome(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, ".m2")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	got, err := ResolveBindMounts(Mounts{
		"/home/dev/.m2": {Source: "~/.m2"},
	})
	if err != nil {
		t.Fatalf("ResolveBindMounts: %v", err)
	}
	mount, ok := got["/home/dev/.m2"]
	if !ok {
		t.Fatalf("missing resolved mount: %+v", got)
	}
	if mount.Source != source {
		t.Errorf("source = %q, want %q", mount.Source, source)
	}
	if mount.Readonly {
		t.Error("expected a writable mount by default")
	}
}

// TestResolveBindMountsExpandsBareTilde guards the `source: ~` form, where
// TrimPrefix must not leave the tilde in place (which would yield $HOME/~).
func TestResolveBindMountsExpandsBareTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := ResolveBindMounts(Mounts{"/home/dev/host": {Source: "~"}})
	if err != nil {
		t.Fatalf("ResolveBindMounts: %v", err)
	}
	if got["/home/dev/host"].Source != home {
		t.Errorf("source = %q, want %q", got["/home/dev/host"].Source, home)
	}
}

func TestResolveBindMountsKeepsReadonly(t *testing.T) {
	got, err := ResolveBindMounts(Mounts{
		"/home/dev/ref": {Source: t.TempDir(), Readonly: true},
	})
	if err != nil {
		t.Fatalf("ResolveBindMounts: %v", err)
	}
	if !got["/home/dev/ref"].Readonly {
		t.Error("expected readonly to be preserved")
	}
}

func TestResolveBindMountsCleansTarget(t *testing.T) {
	got, err := ResolveBindMounts(Mounts{
		"/home/dev/./cache/": {Source: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("ResolveBindMounts: %v", err)
	}
	if _, ok := got["/home/dev/cache"]; !ok {
		t.Errorf("expected cleaned target /home/dev/cache, got %+v", got)
	}
}

func TestResolveBindMountsRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "settings.xml")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		mounts Mounts
	}{
		{"reserved workspace", Mounts{"/workspace": {Source: dir}}},
		{"reserved tmp", Mounts{"/tmp": {Source: dir}}},
		{"reserved home", Mounts{"/home/dev": {Source: dir}}},
		{"ancestor of home", Mounts{"/home": {Source: dir}}},
		{"root target", Mounts{"/": {Source: dir}}},
		{"nested in workspace", Mounts{"/workspace/vendor": {Source: dir}}},
		{"nested in tmp", Mounts{"/tmp/cache": {Source: dir}}},
		{"relative target", Mounts{"home/dev/.m2": {Source: dir}}},
		{"empty source", Mounts{"/home/dev/.m2": {Source: ""}}},
		{"relative source", Mounts{"/home/dev/.m2": {Source: "relative/path"}}},
		{"missing source", Mounts{"/home/dev/.m2": {Source: filepath.Join(dir, "nope")}}},
		{"source is a file", Mounts{"/home/dev/.m2": {Source: file}}},
		{"duplicate target", Mounts{"/home/dev/.m2": {Source: dir}, "/home/dev/./.m2": {Source: dir}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ResolveBindMounts(tc.mounts); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

// TestResolveBindMountsAllowsNestedHomeMount documents that mounting into the
// home volume is supported; it is the primary use case (e.g. ~/.m2).
func TestResolveBindMountsAllowsNestedHomeMount(t *testing.T) {
	if _, err := ResolveBindMounts(Mounts{
		"/home/dev/.m2": {Source: t.TempDir()},
	}); err != nil {
		t.Fatalf("expected a nested home mount to be allowed: %v", err)
	}
}

func TestFingerprintIsStableAndOrderIndependent(t *testing.T) {
	a := Mounts{
		"/home/dev/.m2": {Source: "/host/.m2"},
		"/home/dev/ref": {Source: "/host/ref", Readonly: true},
	}
	b := Mounts{
		"/home/dev/ref": {Source: "/host/ref", Readonly: true},
		"/home/dev/.m2": {Source: "/host/.m2"},
	}
	if Fingerprint(a) != Fingerprint(b) {
		t.Error("fingerprint must not depend on map iteration order")
	}
	if Fingerprint(nil) == Fingerprint(a) {
		t.Error("empty and non-empty mount sets must differ")
	}
}

func TestFingerprintDetectsFieldChanges(t *testing.T) {
	base := Mounts{"/home/dev/.m2": {Source: "/host/.m2"}}
	readonly := Mounts{"/home/dev/.m2": {Source: "/host/.m2", Readonly: true}}
	otherSource := Mounts{"/home/dev/.m2": {Source: "/host/other"}}
	otherTarget := Mounts{"/home/dev/m2": {Source: "/host/.m2"}}

	if Fingerprint(base) == Fingerprint(readonly) {
		t.Error("readonly change must alter the fingerprint")
	}
	if Fingerprint(base) == Fingerprint(otherSource) {
		t.Error("source change must alter the fingerprint")
	}
	if Fingerprint(base) == Fingerprint(otherTarget) {
		t.Error("target change must alter the fingerprint")
	}
}

func TestMountTargetsSorted(t *testing.T) {
	got := MountTargets(Mounts{
		"/home/dev/z": {},
		"/home/dev/a": {},
	})
	if len(got) != 2 || got[0] != "/home/dev/a" || got[1] != "/home/dev/z" {
		t.Errorf("MountTargets = %v, want sorted", got)
	}
}

// TestResolveBindMountsHomeDirUnresolvable covers the branch where the host
// home directory cannot be determined, so `~/` cannot be expanded.
func TestResolveBindMountsHomeDirUnresolvable(t *testing.T) {
	t.Setenv("HOME", "")

	if _, err := ResolveBindMounts(Mounts{
		"/home/dev/.m2": {Source: "~/.m2"},
	}); err == nil {
		t.Fatal("expected an error when the home directory cannot be resolved")
	}
}
