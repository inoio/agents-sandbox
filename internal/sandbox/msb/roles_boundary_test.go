package msb

import (
	"reflect"
	"slices"
	"testing"
)

// methodNames returns the sorted exported method names of an interface type.
func methodNames(t reflect.Type) []string {
	names := make([]string, 0, t.NumMethod())
	for method := range t.Methods() {
		names = append(names, method.Name)
	}
	slices.Sort(names)
	return names
}

// unionMethodNames returns the sorted union of the method names of the given
// interface types, deduplicated across roles that share a method (Shell in
// SandboxExecutor/SandboxStreamer, Metrics/Logs in
// SandboxResourceControl/SandboxObserver, Destroy in
// SandboxLifecycleHandle/SandboxLifecycle).
func unionMethodNames(roles ...reflect.Type) []string {
	seen := make(map[string]struct{})
	for _, role := range roles {
		for _, name := range methodNames(role) {
			seen[name] = struct{}{}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// TestCompositeMethodSetsEqualTheirRoleUnions pins the phase-1 boundary: each
// composite (Client, SandboxHandle, Sandbox) is exactly the union of its
// constituent roles. A future edit that adds a method to a role without the
// composite carrying it, or adds a method to the composite that no role owns,
// fails with the missing/extra method names. Signatures are enforced by the
// compiler through the embedding, so names are the boundary's invariant here.
func TestCompositeMethodSetsEqualTheirRoleUnions(t *testing.T) {
	composites := []struct {
		name  string
		typ   reflect.Type
		roles []reflect.Type
	}{
		{
			name: "Client",
			typ:  reflect.TypeFor[Client](),
			roles: []reflect.Type{
				reflect.TypeFor[RuntimeInstaller](),
				reflect.TypeFor[SandboxStore](),
				reflect.TypeFor[VolumeStore](),
				reflect.TypeFor[ImageStore](),
				reflect.TypeFor[StorageStore](),
			},
		},
		{
			name: "SandboxHandle",
			typ:  reflect.TypeFor[SandboxHandle](),
			roles: []reflect.Type{
				reflect.TypeFor[SandboxIdentity](),
				reflect.TypeFor[SandboxLifecycleHandle](),
				reflect.TypeFor[SandboxConfigReader](),
				reflect.TypeFor[SandboxResourceControl](),
			},
		},
		{
			name: "Sandbox",
			typ:  reflect.TypeFor[Sandbox](),
			roles: []reflect.Type{
				reflect.TypeFor[SandboxExecutor](),
				reflect.TypeFor[SandboxStreamer](),
				reflect.TypeFor[SandboxAttacher](),
				reflect.TypeFor[SandboxJobs](),
				reflect.TypeFor[SandboxObserver](),
				reflect.TypeFor[SandboxLifecycle](),
			},
		},
	}

	for _, composite := range composites {
		t.Run(composite.name, func(t *testing.T) {
			got := methodNames(composite.typ)
			want := unionMethodNames(composite.roles...)
			var missing, extra []string
			for _, name := range want {
				if !slices.Contains(got, name) {
					missing = append(missing, name)
				}
			}
			for _, name := range got {
				if !slices.Contains(want, name) {
					extra = append(extra, name)
				}
			}
			if len(missing) > 0 || len(extra) > 0 {
				t.Fatalf(
					"%s method set does not equal the union of its roles\ncomposite: %v\nunion:     %v\nmissing:   %v\nextra:     %v",
					composite.name,
					got,
					want,
					missing,
					extra,
				)
			}
		})
	}
}

// TestClientMethodNamesDoNotCollideWithSandboxComposites pins that the client
// facade's method names stay disjoint from the per-sandbox composites'. Only
// Client is asserted: SandboxHandle and Sandbox legitimately share
// Metrics/Logs/Destroy with each other, but nothing on the client facade may
// reuse a sandbox-facing method name.
func TestClientMethodNamesDoNotCollideWithSandboxComposites(t *testing.T) {
	client := methodNames(reflect.TypeFor[Client]())
	for _, other := range []struct {
		name string
		typ  reflect.Type
	}{
		{"SandboxHandle", reflect.TypeFor[SandboxHandle]()},
		{"Sandbox", reflect.TypeFor[Sandbox]()},
	} {
		var shared []string
		for _, name := range client {
			if slices.Contains(methodNames(other.typ), name) {
				shared = append(shared, name)
			}
		}
		if len(shared) > 0 {
			t.Fatalf("Client shares method name(s) with %s: %v", other.name, shared)
		}
	}
}
