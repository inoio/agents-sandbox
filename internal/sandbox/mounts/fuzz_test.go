package mounts

import (
	"path/filepath"
	"strings"
	"testing"
)

// The guest paths agents-sandbox mounts itself, and the exclusive subset whose
// contents must stay visible. These are spelled out instead of reusing
// managedMountTargets/exclusiveMountTargets so the check cannot inherit a
// mistake from the code it tests: an entry dropped from a production list must
// fail this test rather than agree with it.
var (
	fuzzManagedTargets   = []string{"/home/dev", "/workspace", "/tmp"}
	fuzzExclusiveTargets = []string{"/workspace", "/tmp"}
)

// fuzzInside reports whether path lies inside base, computed independently of
// the production isWithin helper.
func fuzzInside(path, base string) bool {
	return strings.HasPrefix(path, base+"/")
}

// FuzzResolveMountTarget drives arbitrary guest paths from a user's `mounts:`
// config through the validation that decides which host directories land where.
// An accepted target must never equal or be an ancestor of a managed mount
// (/home/dev, /workspace, /tmp) — such a parent would shadow it — and must
// never nest inside /workspace or /tmp, whose contents must stay visible. A
// child of /home/dev is deliberately allowed. Accepted targets are also
// cleaned, so re-validating the result is a no-op.
func FuzzResolveMountTarget(f *testing.F) {
	for _, seed := range []string{
		"", "/", "/workspace", "/workspace/x", "/workspace/..", "/tmp", "/tmp/x",
		"/home/dev", "/home/dev/.m2", "/home", "/mnt/data", "relative", "//workspace",
		"/workspace/../etc", "/tmp/../workspace", "./x", "/home/devx", "/foo/./bar",
		"/foo/bar/", "/foo//bar", "/foo/bar/../baz",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, target string) {
		clean, err := ResolveMountTarget(target)
		if err != nil {
			return
		}
		if !filepath.IsAbs(clean) || clean == "/" {
			t.Fatalf("ResolveMountTarget(%q) = %q, want an absolute non-root path", target, clean)
		}
		if filepath.Clean(clean) != clean {
			t.Fatalf("ResolveMountTarget(%q) = %q, want a cleaned target", target, clean)
		}
		for _, managed := range fuzzManagedTargets {
			if clean == managed || fuzzInside(managed, clean) {
				t.Fatalf("ResolveMountTarget(%q) = %q collides with managed mount %q", target, clean, managed)
			}
		}
		for _, exclusive := range fuzzExclusiveTargets {
			if fuzzInside(clean, exclusive) {
				t.Fatalf("ResolveMountTarget(%q) = %q would shadow managed mount %q", target, clean, exclusive)
			}
		}
		if again, err := ResolveMountTarget(clean); err != nil || again != clean {
			t.Fatalf("ResolveMountTarget(%q) = %q is not a fixed point: got %q, %v", target, clean, again, err)
		}
	})
}
