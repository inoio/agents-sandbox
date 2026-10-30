package agent

import (
	"slices"
	"testing"
)

type stubAgent struct{ name string }

func (s stubAgent) Name() string          { return s.name }
func (s stubAgent) ConfigDirName() string { return s.name }
func (s stubAgent) ImageSpec() ImageSpec  { return ImageSpec{} }

func TestRegisterMakesAgentLookupable(t *testing.T) {
	const name = "test-stub-agent"
	Register(stubAgent{name: name})
	t.Cleanup(func() { delete(registry, name) })

	got, ok := Lookup(name)
	if !ok {
		t.Fatalf("Lookup(%q) not found after Register", name)
	}
	if got.Name() != name {
		t.Errorf("Lookup(%q).Name() = %q, want %q", name, got.Name(), name)
	}
	if !slices.Contains(Names(), name) {
		t.Errorf("Names() = %v, want to include %q", Names(), name)
	}
}
