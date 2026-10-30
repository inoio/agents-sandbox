package vm

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// imageChangeLabel is the plan's image change label; assertRebuildReason matches
// on the label text reported at verbose level.
const imageChangeLabel = "image"

// TestResolveReconfigDecisionRecreatesOnContentIDSwitch pins the structured
// image identity end to end: a live VM whose persisted content reference differs
// from the desired identity recreates the VM even when the persisted image
// digest already matches. That is the state a failed replacement leaves behind —
// the image-change home action records the new digest before the VM is replaced
// — so the content id is the only remaining signal that the live VM is still on
// the old image (specs/image-ref-content-addressing.md).
func TestResolveReconfigDecisionRecreatesOnContentIDSwitch(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const slug = "testproj"
	const desiredContentID = "9f2c1a7b4d6e0f31"
	f := newReconfigFixtureForImage(
		t,
		naming.ContentImageRef(slug, "opencode", "0123456789abcdef"),
		launcherLiveMounts(1024, 16384, "vol"),
		nil,
	)

	// The persisted digest already equals the desired one, so imageChanged is
	// false and only the content id can drive the recreate.
	persisted := matchingHomeState(network.Policy{})
	info := image.ImageInfo{
		Ref:      naming.ContentImageRef(slug, "opencode", desiredContentID),
		Identity: naming.ImageIdentity{Slug: slug, Agent: "opencode", ContentID: desiredContentID},
		Digest:   persisted.ImageDigest,
	}

	decision, err := resolveReconfigDecision(
		context.Background(),
		f.client,
		f.vm,
		state.Key{Slug: slug, Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		info,
		f.homeVol,
		persisted,
		f.cfs,
		envsecret.EnvSecrets{},
		f.ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !decision.Recreate {
		t.Error("expected a recreate when the persisted content id differs from the desired one")
	}
	assertRebuildReason(t, f.ui, imageChangeLabel)
}

// TestResolveReconfigDecisionReusesVMOnSameContentIdentity pins the tolerance
// side: a live VM persisted under the name spelling of the desired identity is
// reused, so the content-addressing change alone never recreates an existing VM.
func TestResolveReconfigDecisionReusesVMOnSameContentIdentity(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const slug = "testproj"
	const contentID = "9f2c1a7b4d6e0f31"
	f := newReconfigFixtureForImage(
		t,
		naming.RunnerImageRef(slug, "opencode"),
		launcherLiveMounts(1024, 16384, "vol"),
		nil,
	)

	persisted := matchingHomeState(network.Policy{})
	info := image.ImageInfo{
		Ref:      naming.ContentImageRef(slug, "opencode", contentID),
		Identity: naming.ImageIdentity{Slug: slug, Agent: "opencode", ContentID: contentID},
		Digest:   persisted.ImageDigest,
	}

	decision, err := resolveReconfigDecision(
		context.Background(),
		f.client,
		f.vm,
		state.Key{Slug: slug, Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		info,
		f.homeVol,
		persisted,
		f.cfs,
		envsecret.EnvSecrets{},
		f.ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if decision.Recreate {
		t.Errorf("expected no recreate for the name spelling of the desired image, got %+v", f.ui.VerboseCalls)
	}
}
