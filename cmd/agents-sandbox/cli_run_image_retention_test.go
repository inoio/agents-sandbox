package main

import (
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// TestCurrentRunImageKeysRetainsCheckoutSlug pins the keys a run protects from its
// own startup auto-prune: the checkout slug carries the runner image, so the run
// passed to the prune must name it (plus any pre-migration legacy repo slugs).
func TestCurrentRunImageKeysRetainsCheckoutSlug(t *testing.T) {
	clone := newOriginClone(t)
	identity := mustIdentity(t, clone)
	t.Chdir(clone)

	want := []state.Key{{Slug: identity.CheckoutSlug, Agent: "opencode"}}
	for _, legacy := range identity.LegacyRepoSlugs {
		want = append(want, state.Key{Slug: legacy, Agent: "opencode"})
	}
	if got := currentRunImageKeys("opencode"); !slices.Equal(got, want) {
		t.Errorf("currentRunImageKeys = %v, want %v", got, want)
	}
}

// TestCurrentRunImageKeysEmptyOutsideARepo pins the best-effort contract: an
// unresolvable identity retains nothing rather than failing the prune.
func TestCurrentRunImageKeysEmptyOutsideARepo(t *testing.T) {
	t.Chdir(unresolvableIdentityDir(t))
	if got := currentRunImageKeys("opencode"); len(got) != 0 {
		t.Errorf("currentRunImageKeys = %v, want none outside an identity", got)
	}
}
