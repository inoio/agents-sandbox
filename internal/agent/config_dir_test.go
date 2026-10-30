package agent

import (
	"slices"
	"testing"
)

// aliasedStubAgent declares config-dir aliases, so name order and alias
// validation can be tested without depending on a registered profile.
type aliasedStubAgent struct {
	stubAgent

	aliases []string
}

func (a aliasedStubAgent) ConfigDirAliases() []string { return a.aliases }

func TestConfigDirNamesListPrimaryThenAliases(t *testing.T) {
	a := aliasedStubAgent{name: "widget2", aliases: []string{"widget", "gadget"}}

	got, err := ConfigDirNames(a)
	if err != nil {
		t.Fatalf("ConfigDirNames error = %v", err)
	}
	if want := []string{"widget2", "widget", "gadget"}; !slices.Equal(got, want) {
		t.Errorf("ConfigDirNames = %v, want %v", got, want)
	}
}

func TestConfigDirNamesWithoutAliasesIsThePrimaryOnly(t *testing.T) {
	got, err := ConfigDirNames(stubAgent{name: "widget"})
	if err != nil {
		t.Fatalf("ConfigDirNames error = %v", err)
	}
	if want := []string{"widget"}; !slices.Equal(got, want) {
		t.Errorf("ConfigDirNames = %v, want %v", got, want)
	}
}

// TestConfigDirNamesRejectNamesThatAreNotASinglePathElement guards the config
// mirror: a name like ".." would resolve to the launcher config root or a parent
// and could copy unrelated host files, including launcher secrets, into the VM.
func TestConfigDirNamesRejectNamesThatAreNotASinglePathElement(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "/", "//", "./", "a/b", "a/../b", "/abs", "../sibling", "foo/"} {
		t.Run("primary "+bad, func(t *testing.T) {
			if _, err := ConfigDirNames(stubAgent{name: bad}); err == nil {
				t.Errorf("ConfigDirNames with primary %q error = nil, want error", bad)
			}
		})
		t.Run("alias "+bad, func(t *testing.T) {
			a := aliasedStubAgent{name: "valid-primary", aliases: []string{"ok", bad}}
			if _, err := ConfigDirNames(a); err == nil {
				t.Errorf("ConfigDirNames with alias %q error = nil, want error", bad)
			}
		})
	}
}
