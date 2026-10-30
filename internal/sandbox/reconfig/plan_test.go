package reconfig

import (
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
)

// planSlug and planContentID identify the runner image the plan tests compare
// their persisted VM image against.
const planSlug = "proj-1mjusbm3wikhb0"
const planContentID = "9f2c1a7b4d6e0f31"

// planImage is the launcher-side structured identity most tests compare a
// persisted VM image against: the fixture project and agent, pinned to one
// content id (the spelling a rebuilt image resolves to).
func planImage() naming.ImageIdentity {
	return naming.ImageIdentity{Slug: planSlug, Agent: "opencode", ContentID: planContentID}
}

// planNameImage is the same project and agent with no content id: the spelling
// for an image whose content id the launcher does not know.
func planNameImage() naming.ImageIdentity {
	return naming.ImageIdentity{Slug: planSlug, Agent: "opencode"}
}

func TestPlanReconfigServeOnly(t *testing.T) {
	desired := options.ServeOnlyBindings(options.ServeOnlyBasePort)
	tests := []struct {
		name         string
		serveOnly    bool
		cfgPorts     []msbSdk.PortBinding
		wantRecreate bool
		wantLabel    string
	}{
		{"serve on, vm not published", true, nil, true, "published port(s)"},
		{"serve on, vm already published", true, desired, false, ""},
		{"serve off, vm published", false, desired, true, "published port(s)"},
		{"serve off, vm not published", false, nil, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg *msbSdk.SandboxConfig
			if tt.cfgPorts == nil {
				// A running VM always has a non-nil SandboxConfig; PortBindings
				// is simply nil/empty when nothing is published.
				cfg = &msbSdk.SandboxConfig{PortBindings: nil}
			} else {
				cfg = &msbSdk.SandboxConfig{PortBindings: tt.cfgPorts}
			}
			plan := PlanReconfig(
				cfg,
				planImage(),
				options.RunOptions{ServeOnly: tt.serveOnly},
				ChangeFlags{},
				"",
			)
			if plan.Recreate != tt.wantRecreate {
				t.Errorf("Recreate = %v, want %v", plan.Recreate, tt.wantRecreate)
			}
			if tt.wantLabel != "" {
				found := false
				for _, c := range plan.Changes {
					if c.Label == tt.wantLabel {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected change label %q, got %+v", tt.wantLabel, plan.Changes)
				}
			}
		})
	}
}

// TestPlanReconfigTriggersRecreateOnImageChange pins the genuine image switch: a
// persisted content reference whose content id differs from the desired identity
// recreates the VM, while the same content id does not.
func TestPlanReconfigTriggersRecreateOnImageChange(t *testing.T) {
	const persistedContentID = "4b8d0e6f2a1c3b57"
	cfg := &msbSdk.SandboxConfig{Image: naming.ContentImageRef(planSlug, "opencode", persistedContentID)}

	planSame := PlanReconfig(
		cfg,
		naming.ImageIdentity{Slug: planSlug, Agent: "opencode", ContentID: persistedContentID},
		options.RunOptions{},
		ChangeFlags{},
		"",
	)
	if planSame.Recreate {
		t.Error("expected no recreate when the persisted image is the desired content")
	}

	planNew := PlanReconfig(
		cfg,
		planImage(),
		options.RunOptions{},
		ChangeFlags{},
		"",
	)
	if !planNew.Recreate {
		t.Error("expected recreate when the desired content id differs from the persisted one")
	}
	if len(planNew.Changes) == 0 || planNew.Changes[0].Label != "image" {
		t.Errorf("expected an 'image' change label, got %+v", planNew.Changes)
	}
}

// TestPlanReconfigToleratesLegacyImageRef covers Review Focus 1's "VM not
// recreated": a VM booted from the legacy prefixed microsandbox reference must
// not be recreated just because the launcher now computes the prefix-free one.
// The desired side is the name identity, the spelling an install that records no
// content id resolves; every spelling of the same project and agent is tolerated.
func TestPlanReconfigToleratesLegacyImageRef(t *testing.T) {
	const legacyRef = "agents-sandbox/runner-proj-1mjusbm3wikhb0:opencode-latest"

	cases := []struct {
		name         string
		persisted    string
		wantRecreate bool
	}{
		{"prefix-free name ref", naming.RunnerImageRef(planSlug, "opencode"), false},
		{"identical legacy ref", legacyRef, false},
		{"different agent", naming.RunnerImageRef(planSlug, "pi"), true},
		{"pinned digest ref", "agents-sandbox/runner-proj-1mjusbm3wikhb0:4e8oeqohbmalok", true},
		// content ref, same image
		{"content ref, same image", naming.ContentImageRef(planSlug, "opencode", planContentID), false},
		// prefixed content ref, same image
		{"prefixed content ref, same image", naming.DockerImagePrefix + planSlug + ":opencode." + planContentID, false},
		{"content ref, different agent", naming.ContentImageRef(planSlug, "pi", planContentID), true},
	}
	for _, tc := range cases {
		cfg := &msbSdk.SandboxConfig{Image: tc.persisted}
		plan := PlanReconfig(cfg, planNameImage(), options.RunOptions{}, ChangeFlags{}, "")
		if plan.Recreate != tc.wantRecreate {
			t.Errorf(
				"%s: PlanReconfig(persisted %q) recreate = %v, want %v",
				tc.name, tc.persisted, plan.Recreate, tc.wantRecreate,
			)
		}
	}
}

// TestPlanReconfigToleratesContentImageRef covers the upgrade from a VM that was
// booted from a content ref: it is not recreated when the persisted reference
// names the same slug+agent under the name or legacy prefixed spelling, but IS
// recreated for a different content id (a genuine image switch whose persisted
// digest may already match — the home action records the new digest before the
// VM is replaced), a different agent, or a digest-pinned ref.
func TestPlanReconfigToleratesContentImageRef(t *testing.T) {
	cases := []struct {
		name         string
		persisted    string
		wantRecreate bool
	}{
		{"identical content ref", naming.ContentImageRef(planSlug, "opencode", planContentID), false},
		{"its prefix-free name ref", naming.RunnerImageRef(planSlug, "opencode"), false},
		{"its legacy prefixed name ref", naming.DockerImageRef(planSlug, "opencode"), false},
		{"different agent", naming.ContentImageRef(planSlug, "pi", planContentID), true},
		{"pinned digest ref", planSlug + ":4e8oeqohbmalok", true},
		// different content id: a real image switch, not a spelling change
		{"different content id", naming.ContentImageRef(planSlug, "opencode", "4b8d0e6f2a1c3b57"), true},
		// prefixed spelling of a different content id
		{"prefixed different content id", naming.DockerImagePrefix + planSlug + ":opencode.4b8d0e6f2a1c3b57", true},
	}
	for _, tc := range cases {
		cfg := &msbSdk.SandboxConfig{Image: tc.persisted}
		plan := PlanReconfig(cfg, planImage(), options.RunOptions{}, ChangeFlags{}, "")
		if plan.Recreate != tc.wantRecreate {
			t.Errorf(
				"%s: PlanReconfig(persisted %q) recreate = %v, want %v",
				tc.name, tc.persisted, plan.Recreate, tc.wantRecreate,
			)
		}
	}
}

func TestDesiredPublishBindingsDelegatesToOptions(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{PortBindings: []msbSdk.PortBinding{
		{Bind: "127.0.0.1", HostPort: 4097, GuestPort: 4096, Protocol: msbSdk.PortProtocolTCP},
	}}
	optsBindings := options.ServeOnlyBindings(4097)
	planBindings := desiredPublishBindings(true, cfg)
	if len(planBindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(planBindings))
	}
	if planBindings[0].Bind != optsBindings[0].Bind {
		t.Errorf("Bind = %q, want %q", planBindings[0].Bind, optsBindings[0].Bind)
	}
	if planBindings[0].HostPort != optsBindings[0].HostPort {
		t.Errorf("HostPort = %d, want %d", planBindings[0].HostPort, optsBindings[0].HostPort)
	}
	if planBindings[0].GuestPort != optsBindings[0].GuestPort {
		t.Errorf("GuestPort = %d, want %d", planBindings[0].GuestPort, optsBindings[0].GuestPort)
	}
	if planBindings[0].Protocol != optsBindings[0].Protocol {
		t.Errorf("Protocol = %v, want %v", planBindings[0].Protocol, optsBindings[0].Protocol)
	}
}

func TestDesiredPublishBindingsNilWhenNotServeOnly(t *testing.T) {
	got := desiredPublishBindings(false, nil)
	if got != nil {
		t.Errorf("expected nil when serveOnly=false, got %+v", got)
	}
}

func TestPlanReconfigNetworkChangeTriggersRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{}
	opts := options.RunOptions{}
	plan := PlanReconfig(cfg, planImage(), opts, ChangeFlags{Network: true}, "")
	if !plan.Recreate {
		t.Fatal("expected Recreate when network policy changes")
	}
}

func TestPlanReconfigNetworkSameNoRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{}
	opts := options.RunOptions{}
	plan := PlanReconfig(cfg, planImage(), opts, ChangeFlags{}, "")
	if plan.Recreate {
		t.Fatal("expected no Recreate when network policy is unchanged")
	}
}

func TestPlanReconfigHomeVolumeChangeTriggersRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Image: naming.RunnerImageRef(planSlug, "opencode"),
		Volumes: map[string]msbSdk.MountConfig{
			mounts.VMHomeDir: {Named: "agents-sandbox-home-proj-old"},
		},
	}
	plan := PlanReconfig(cfg, planImage(), options.RunOptions{}, ChangeFlags{},
		"agents-sandbox-home-proj-new")
	if !plan.Recreate {
		t.Fatal("expected Recreate when the desired home volume differs from the mounted one")
	}
	found := false
	for _, c := range plan.Changes {
		if c.Label == "home volume" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a 'home volume' change label, got %+v", plan.Changes)
	}
}

func TestPlanReconfigHomeVolumeSameNoRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Image: naming.RunnerImageRef(planSlug, "opencode"),
		Volumes: map[string]msbSdk.MountConfig{
			mounts.VMHomeDir: {Named: "agents-sandbox-home-proj-vol"},
		},
	}
	plan := PlanReconfig(cfg, planImage(), options.RunOptions{}, ChangeFlags{},
		"agents-sandbox-home-proj-vol")
	if plan.Recreate {
		t.Fatal("expected no Recreate when the mounted home volume matches the desired one")
	}
}

func TestPlanReconfigNilCfgHomeVolumeSafe(t *testing.T) {
	plan := PlanReconfig(nil, planImage(), options.RunOptions{}, ChangeFlags{},
		"agents-sandbox-home-proj-vol")
	if plan.Recreate {
		t.Fatal("expected no Recreate for a nil config (fresh VM creation)")
	}
}

func TestPlanReconfigMountsChangeTriggersRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{}
	plan := PlanReconfig(cfg, planImage(), options.RunOptions{}, ChangeFlags{Mounts: true}, "")
	if !plan.Recreate {
		t.Fatal("expected Recreate when host bind mounts change")
	}
	found := false
	for _, c := range plan.Changes {
		if c.Label == changeLabelBindMounts {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a %q change label, got %+v", changeLabelBindMounts, plan.Changes)
	}
}

// TestPlanReconfigMountsUnchangedNoRecreate guards the regression where the
// plan was derived from cfg.Volumes, which the SDK never populates when
// reading an existing VM back: every run would then rebuild the VM.
func TestPlanReconfigMountsUnchangedNoRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{}
	opts := options.RunOptions{Mounts: mounts.Mounts{
		"/home/dev/.m2": {Source: "/host/.m2"},
	}}
	plan := PlanReconfig(cfg, planImage(), opts, ChangeFlags{}, "")
	if plan.Recreate {
		t.Fatalf("expected no Recreate for unchanged mounts, got %+v", plan.Changes)
	}
}

func TestFormatSizeSpecEmptyRaw(t *testing.T) {
	cases := []struct {
		valueMiB uint32
		raw      string
		want     string
	}{
		{0, "", "0M"},
		{1024, "", "1024M"},
		{4096, "", "4096M"},
		{2048, "2G", "2G"},
		{2048, "3G", "2048M"},
	}
	for _, tc := range cases {
		if got := FormatSizeSpec(tc.valueMiB, tc.raw); got != tc.want {
			t.Errorf("FormatSizeSpec(%d, %q) = %q, want %q", tc.valueMiB, tc.raw, got, tc.want)
		}
	}
}

// TestConfigChangeList pins both display forms: the "label: old → new" line for
// a change carrying both values, and the bare label for anything else. A
// one-sided change must take the bare form.
func TestConfigChangeList(t *testing.T) {
	got := configChangeList([]Change{
		{Label: "image", Old: "a", New: "b"},
		{Label: "new-only", New: "b"},
		{Label: "old-only", Old: "a"},
		{Label: "label-only"},
	})
	want := "Project VM config changed:\n" +
		"  - image: a → b\n" +
		"  - new-only\n" +
		"  - old-only\n" +
		"  - label-only"
	if got != want {
		t.Errorf("configChangeList() = %q, want %q", got, want)
	}
	if got := configChangeList(nil); got != "Project VM config changed:" {
		t.Errorf("configChangeList(nil) = %q", got)
	}
}

func TestImageRecreateEmptyImageRef(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "old:1"}
	if imageRecreate(cfg, naming.ImageIdentity{}, false) {
		t.Error("an empty desired image identity must not trigger a recreate")
	}
	if !imageRecreate(cfg, planImage(), false) {
		t.Error("a differing desired image identity must trigger a recreate")
	}
	if !imageRecreate(cfg, naming.ImageIdentity{}, true) {
		t.Error("the Image flag alone must trigger a recreate")
	}
}

// TestImageRecreateNonRunnerRefs pins that a persisted reference the launcher
// never computes — a foreign repository reference, or a digest-pinned runner ref
// — is never the same image as the desired identity, so the VM is recreated
// rather than reused.
func TestImageRecreateNonRunnerRefs(t *testing.T) {
	for _, persisted := range []string{
		"docker.io/library/foreign:1",
		planSlug + ":4e8oeqohbmalok",
	} {
		cfg := &msbSdk.SandboxConfig{Image: persisted}
		if !imageRecreate(cfg, planImage(), false) {
			t.Errorf("persisted %q must trigger a recreate against the desired identity", persisted)
		}
	}
}

func TestIsNameImage(t *testing.T) {
	const slug = "proj-1mjusbm3wikhb0"
	cases := []struct {
		name string
		info naming.ArtifactInfo
		want bool
	}{
		{"slug and agent", naming.ArtifactInfo{Slug: slug, Agent: "opencode"}, true},
		{"content-addressed", naming.ArtifactInfo{Slug: slug, Agent: "opencode", ContentID: "abc"}, true},
		{"no slug", naming.ArtifactInfo{Agent: "opencode"}, false},
		{"no agent", naming.ArtifactInfo{Slug: slug}, false},
		{"digest-pinned", naming.ArtifactInfo{Slug: slug, Agent: "opencode", Digest: "abc"}, false},
	}
	for _, tc := range cases {
		if got := isNameImage(tc.info); got != tc.want {
			t.Errorf("%s: isNameImage(%+v) = %v, want %v", tc.name, tc.info, got, tc.want)
		}
	}
}

// TestSameImageRefNonNameRefs pins that the persisted ref must name a (slug,
// agent) pair before any comparison happens: a digest-pinned runner ref sharing
// the desired slug, and a foreign ref, are never the same image even when the
// desired identity is incomplete (a slug-only or empty one). The pair comparison
// alone would accept the digest-pinned row, whose parsed agent is empty like the
// identity's.
func TestSameImageRefNonNameRefs(t *testing.T) {
	cases := []struct {
		name      string
		persisted string
		image     naming.ImageIdentity
	}{
		{"digest-pinned ref, same slug", planSlug + ":4e8oeqohbmalok", naming.ImageIdentity{Slug: planSlug}},
		{"foreign ref, empty identity", "docker.io/library/foreign:1", naming.ImageIdentity{}},
	}
	for _, tc := range cases {
		if sameImageRef(tc.persisted, tc.image) {
			t.Errorf("%s: sameImageRef(%q, %+v) = true, want false", tc.name, tc.persisted, tc.image)
		}
	}
}

// TestSameImageRefSlugAndAgent pins that a persisted name ref is the same image
// as a desired name identity only when both their slug and agent match, and that
// a differing slug is not masked by an equal agent.
func TestSameImageRefSlugAndAgent(t *testing.T) {
	nameImage := naming.ImageIdentity{Slug: planSlug, Agent: "opencode"}
	if !sameImageRef(naming.RunnerImageRef(planSlug, "opencode"), nameImage) {
		t.Error("the prefix-free name ref must be the same image as its name identity")
	}
	if !sameImageRef(naming.DockerImageRef(planSlug, "opencode"), nameImage) {
		t.Error("the prefixed spelling of one slug+agent must be the same image")
	}
	if sameImageRef(naming.RunnerImageRef("other-1mjusbm3wikhb0", "opencode"), nameImage) {
		t.Error("a different slug with the same agent must not be the same image")
	}
	if sameImageRef(naming.RunnerImageRef(planSlug, "pi"), nameImage) {
		t.Error("a different agent must not be the same image")
	}
}

// TestSameImageRefContentID pins the content-addressed rule: a persisted content
// ref is the same image as a desired content identity only when their content
// ids match, across the prefix-free and prefixed spellings, while a name
// identity tolerates any content id (the upgrade spelling).
func TestSameImageRefContentID(t *testing.T) {
	contentImage := planImage()
	prefixFree := naming.ContentImageRef(planSlug, "opencode", planContentID)
	prefixed := naming.DockerImagePrefix + planSlug + ":opencode." + planContentID
	if !sameImageRef(prefixFree, contentImage) {
		t.Error("the prefix-free content ref must be the same image as its content identity")
	}
	if !sameImageRef(prefixed, contentImage) {
		t.Error("the prefixed content ref must be the same image as its content identity")
	}
	if !sameImageRef(naming.RunnerImageRef(planSlug, "opencode"), contentImage) {
		t.Error("the name spelling of one slug+agent must be the same image as its content identity")
	}
	if sameImageRef(naming.ContentImageRef(planSlug, "opencode", "0123456789abcdef"), contentImage) {
		t.Error("a different content id must not be the same image")
	}
	if !sameImageRef(naming.ContentImageRef(planSlug, "opencode", "0123456789abcdef"), planNameImage()) {
		t.Error("any content spelling must be the same image as its name identity")
	}
}

func TestTmpSizeChangeMissingVolume(t *testing.T) {
	if _, changed := tmpSizeChange(&msbSdk.SandboxConfig{}, options.RunOptions{TmpSize: "4G"}); changed {
		t.Error("a requested tmp size with no mounted /tmp volume must not be a change")
	}
}

func TestTmpSizeChangeInvalidSpec(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Volumes: map[string]msbSdk.MountConfig{mounts.TmpMountPath: {SizeMiB: 2048}},
	}
	if _, changed := tmpSizeChange(cfg, options.RunOptions{TmpSize: "not-a-size"}); changed {
		t.Error("an unparseable tmp size must not be a change")
	}
	if _, changed := tmpSizeChange(cfg, options.RunOptions{}); changed {
		t.Error("an unset tmp size must not be a change")
	}
}

func TestWorkspaceQuotaChangeReturnValue(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Volumes: map[string]msbSdk.MountConfig{mounts.WorkspaceMountPath: {QuotaMiB: 1024}},
	}
	change, changed := workspaceQuotaChange(cfg, options.RunOptions{WorkspaceQuota: "32G"})
	if !changed {
		t.Fatal("expected a change for a different quota")
	}
	if change.Label != "/workspace write quota" || change.Old != "1024M" || change.New != "32G" {
		t.Errorf("workspaceQuotaChange() = %+v", change)
	}
}

func TestHomeVolumeChangeEmptyRequested(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Volumes: map[string]msbSdk.MountConfig{mounts.VMHomeDir: {Named: "vol"}},
	}
	if homeVolumeChange(cfg, "") {
		t.Error("an empty desired home volume must not be a change")
	}
	if homeVolumeChange(&msbSdk.SandboxConfig{}, "vol") {
		t.Error("a missing mounted home volume must not be a change")
	}
	if !homeVolumeChange(cfg, "other") {
		t.Error("a differing home volume must be a change")
	}
}

func TestRootDiskChangeOldValue(t *testing.T) {
	disk := msbSdk.RootDisk.Managed(8192)
	cfg := &msbSdk.SandboxConfig{RootDisk: &disk}
	change, changed := rootDiskChange(cfg, options.RunOptions{DiskSize: "16G"})
	if !changed {
		t.Fatal("expected a change for a different root disk")
	}
	if change.Label != "root disk size" || change.Old != "8192M" || change.New != "16G" {
		t.Errorf("rootDiskChange() = %+v", change)
	}
	if _, changed := rootDiskChange(&msbSdk.SandboxConfig{}, options.RunOptions{DiskSize: "16G"}); !changed {
		t.Error("a nil root disk with a requested size must be a change")
	}
}

func TestPublishReconfigHostPort(t *testing.T) {
	if hostPort, changed := publishReconfig(options.RunOptions{}, &msbSdk.SandboxConfig{}); hostPort != 0 || changed {
		t.Errorf("not serve-only: hostPort=%d changed=%v, want 0 false", hostPort, changed)
	}
	cfg := &msbSdk.SandboxConfig{PortBindings: options.ServeOnlyBindings(4097)}
	if hostPort, changed := publishReconfig(options.RunOptions{ServeOnly: true}, cfg); hostPort != 4097 || changed {
		t.Errorf("serve-only reuse: hostPort=%d changed=%v, want 4097 false", hostPort, changed)
	}
}

func TestResourceModificationStagesSingleCPU(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{CPUs: 4, MaxCPUs: 8}
	mo := resourceModification(cfg, options.RunOptions{CPUs: 1})
	if mo == nil || mo.CPUs != 1 {
		t.Fatalf("expected a staged CPU=1 modification, got %+v", mo)
	}
}

func TestResourceModificationMaxCPUsOne(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{CPUs: 0, MaxCPUs: 1}
	mo := resourceModification(cfg, options.RunOptions{CPUs: 2})
	if mo == nil || mo.CPUs != 1 {
		t.Fatalf("expected CPU clamped to 1, got %+v", mo)
	}
}

func TestPlanReconfigAgentConfigSuppressedByRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Image: naming.ContentImageRef(planSlug, "opencode", "4b8d0e6f2a1c3b57"), CPUs: 4, MemoryMiB: 4096,
	}
	plan := PlanReconfig(cfg, planImage(), options.RunOptions{}, ChangeFlags{AgentConfig: true}, "")
	if !plan.Recreate {
		t.Fatal("expected a recreate on the image change")
	}
	if plan.RestartDaemons {
		t.Error("agent config must not restart daemons when the VM is recreated anyway")
	}
	for _, c := range plan.Changes {
		if c.Label == "agent config" {
			t.Errorf("agent config should not be a change line when recreating: %+v", plan.Changes)
		}
	}
}

func TestResourceModificationMemoryNoMax(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{MemoryMiB: 1024, MaxMemoryMiB: 0}
	mo := resourceModification(cfg, options.RunOptions{Memory: "2G"})
	if mo == nil {
		t.Fatal("expected a staged memory modification when the boot max is unknown")
	}
	if mo.MemoryMiB != 2048 {
		t.Errorf("MemoryMiB = %d, want 2048", mo.MemoryMiB)
	}
	if mo.Policy != msbSdk.ModificationPolicyNoRestart {
		t.Errorf("Policy = %v, want no-restart", mo.Policy)
	}
}

func TestResourceModificationMemoryBelowMax(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{MemoryMiB: 1024, MaxMemoryMiB: 8192}
	mo := resourceModification(cfg, options.RunOptions{Memory: "2G"})
	if mo == nil || mo.MemoryMiB != 2048 {
		t.Fatalf("expected memory 2048 (below the max), got %+v", mo)
	}
}

func TestResourceModificationMemoryClampAtOneMiB(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{MemoryMiB: 0, MaxMemoryMiB: 1}
	mo := resourceModification(cfg, options.RunOptions{Memory: "2G"})
	if mo == nil || mo.MemoryMiB != 1 {
		t.Fatalf("expected memory clamped to 1, got %+v", mo)
	}
}

func TestResourceModificationNilWhenUnchanged(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{CPUs: 4, MemoryMiB: 4096, MaxCPUs: 8, MaxMemoryMiB: 8192}
	if mo := resourceModification(cfg, options.RunOptions{CPUs: 4, Memory: "4G"}); mo != nil {
		t.Errorf("expected nil when nothing changes, got %+v", mo)
	}
}

// TestPlanReconfigRecreateSuppressesEnvChange pins that env/secret changes fold
// into an already-decided recreate: they must not be appended as extra change
// lines, which would misreport why the VM is rebuilt.
func TestPlanReconfigRecreateSuppressesEnvChange(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Image: naming.ContentImageRef(planSlug, "opencode", "4b8d0e6f2a1c3b57"), CPUs: 4, MemoryMiB: 4096,
	}
	plan := PlanReconfig(cfg, planImage(), options.RunOptions{}, ChangeFlags{Env: true, Secrets: true}, "")
	if !plan.Recreate {
		t.Fatal("expected a recreate on the image change")
	}
	for _, c := range plan.Changes {
		if c.Label == "environment variables" || c.Label == "secrets" {
			t.Errorf("env/secret change should not be listed when recreating anyway: %+v", plan.Changes)
		}
	}
}

func TestPlanReconfigAgentConfigAppendsChange(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Image: naming.RunnerImageRef(planSlug, "opencode"), CPUs: 4, MemoryMiB: 4096,
	}
	plan := PlanReconfig(cfg, planNameImage(), options.RunOptions{}, ChangeFlags{AgentConfig: true}, "")
	if !plan.RestartDaemons {
		t.Fatal("expected a daemon restart for an agent config change")
	}
	found := false
	for _, c := range plan.Changes {
		if c.Label == "agent config" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an 'agent config' change line, got %+v", plan.Changes)
	}
}
