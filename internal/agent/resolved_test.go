package agent_test

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// capabilityPresent adapts an AsX helper's (value, ok) result to just its ok.
func capabilityPresent(_ any, ok bool) bool { return ok }

// Resolve materializes exactly the capabilities the profile provides among the
// ones the run consumes, so a run boundary discovers them once and consumers
// read the fields instead of re-asserting the interfaces.
func TestResolveMaterializesEveryConsumedCapability(t *testing.T) {
	for _, name := range agent.Names() {
		t.Run(name, func(t *testing.T) {
			a, ok := agent.Lookup(name)
			if !ok {
				t.Fatalf("Lookup(%q) failed", name)
			}
			r := agent.Resolve(a)
			if r.Profile != a {
				t.Errorf("Resolved.Profile is not the selected profile")
			}
			cases := []struct {
				capability string
				resolved   bool
				asserted   bool
			}{
				{"DaemonProvider", r.Daemon != nil, capabilityPresent(agent.AsDaemonProvider(a))},
				{"WorktreeProvider", r.Worktree != nil, capabilityPresent(agent.AsWorktreeProvider(a))},
				{"VersionProvider", r.Version != nil, capabilityPresent(agent.AsVersionProvider(a))},
				{"UpgradeChecker", r.Upgrade != nil, capabilityPresent(agent.AsUpgradeChecker(a))},
				{"GuestToolRequirer", r.GuestTools != nil, capabilityPresent(agent.AsGuestToolRequirer(a))},
				{
					"DetachedWorktreeReporter",
					r.DetachedWorktree != nil,
					capabilityPresent(agent.AsDetachedWorktreeReporter(a)),
				},
			}
			for _, c := range cases {
				if c.resolved != c.asserted {
					t.Errorf("Resolved.%s present = %v, want %v (the type assertion's result)",
						c.capability, c.resolved, c.asserted)
				}
			}
		})
	}
}

// A profile with no optional capabilities resolves to a value whose capability
// fields are all nil, so a consumer can treat nil as "absent" without
// re-asserting.
func TestResolveOfPlainProfileHasNoCapabilities(t *testing.T) {
	r := agent.Resolve(stubPlainAgent{})
	absent := []struct {
		capability string
		present    bool
	}{
		{"DaemonProvider", r.Daemon != nil},
		{"WorktreeProvider", r.Worktree != nil},
		{"VersionProvider", r.Version != nil},
		{"UpgradeChecker", r.Upgrade != nil},
		{"GuestToolRequirer", r.GuestTools != nil},
		{"DetachedWorktreeReporter", r.DetachedWorktree != nil},
	}
	for _, c := range absent {
		if c.present {
			t.Errorf("Resolved.%s present for a plain profile, want nil", c.capability)
		}
	}
}
