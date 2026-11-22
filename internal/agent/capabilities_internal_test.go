package agent

import "testing"

// noMergerAgent is a registered Agent that declares no ConfigMerger, so the
// shared snippet-name predicate must not second-guess it. No built-in profile
// lacks a merger, so the registry extension point is how the branch is reached.
type noMergerAgent struct{ name string }

func (a noMergerAgent) Name() string          { return a.name }
func (a noMergerAgent) Experimental() bool    { return false }
func (a noMergerAgent) ConfigDirName() string { return a.name }
func (a noMergerAgent) ImageSpec() ImageSpec  { return ImageSpec{} }

// A registered agent that declares no ConfigMerger is not second-guessed: the
// shared snippet-name predicate reports true rather than refusing a name it
// cannot classify. The registration is removed again so the process-global
// registry is left as it was found, keeping the suite idempotent under
// -count>1 and -shuffle (the external capabilities test cannot reach registry).
func TestMergesSnippetNameWithoutConfigMerger(t *testing.T) {
	const name = "test-agent-without-config-merger"
	Register(noMergerAgent{name: name})
	t.Cleanup(func() { delete(registry, name) })

	if !MergesSnippetName(name, "anything.json") {
		t.Error("a known agent without a ConfigMerger must report true")
	}
}
