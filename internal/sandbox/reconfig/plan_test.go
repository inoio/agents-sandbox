package reconfig

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/ports"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestFormatSizeSpecKeepsTheRawSpellingOnlyWhenItMatches pins that the user's
// spelling is shown only when it parses to the value; otherwise, and without
// one, the value is shown in MiB.
func TestFormatSizeSpecKeepsTheRawSpellingOnlyWhenItMatches(t *testing.T) {
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
		{3500, "2G", "3500M"},
	}
	for _, tc := range cases {
		if got := FormatSizeSpec(tc.valueMiB, tc.raw); got != tc.want {
			t.Errorf("FormatSizeSpec(%d, %q) = %q, want %q", tc.valueMiB, tc.raw, got, tc.want)
		}
	}
}

func TestSizeChangeFormatting(t *testing.T) {
	// user-provided "2G" parses to 2048 MiB
	c := SizeChange("/tmp tmpfs size", 2048, 4096, "2G", "4G")
	if c.Label != "/tmp tmpfs size" || c.Old != "2G" || c.New != "4G" {
		t.Errorf("unexpected SizeChange: %+v", c)
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
		{Label: "host bind mounts", Details: []string{"- /a ← /x (read-write)", "+ /host ← / (read-write)"}},
	})
	want := "Project VM config changed:\n" +
		"  - image: a → b\n" +
		"  - new-only\n" +
		"  - old-only\n" +
		"  - label-only\n" +
		"  - host bind mounts:\n" +
		"      - /a ← /x (read-write)\n" +
		"      + /host ← / (read-write)"
	if got != want {
		t.Errorf("configChangeList() = %q, want %q", got, want)
	}
	if got := configChangeList(nil); got != "Project VM config changed:" {
		t.Errorf("configChangeList(nil) = %q", got)
	}
}

// TestPlanReconfigRecreatesOnDiskSizeWithoutARecordedRootDisk pins that a VM
// whose config records no root disk counts as 0M, so a requested disk size
// recreates it.
func TestPlanReconfigRecreatesOnDiskSizeWithoutARecordedRootDisk(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: naming.RunnerImageRef(planSlug, "opencode")}
	plan := PlanReconfig(cfg, planImage(), options.RunOptions{DiskSize: "8G"}, ChangeFlags{}, "")
	if !plan.Recreate {
		t.Fatal("expected recreate on disk size change with nil RootDisk")
	}
	found := false
	for _, c := range plan.Changes {
		if c.Label == "root disk size" && c.Old == "0M" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected root disk size change with Old=0M, got %+v", plan.Changes)
	}
}

func TestPlanReconfigDecidesRecreate(t *testing.T) {
	mkConfig := func(cpus uint8, mem uint32, diskMiB uint32, tmpMiB uint32) *msbSdk.SandboxConfig {
		var rootDisk *msbSdk.RootDiskConfig
		if diskMiB > 0 {
			d := msbSdk.RootDisk.Managed(diskMiB)
			rootDisk = &d
		}
		return &msbSdk.SandboxConfig{
			CPUs:      cpus,
			MemoryMiB: mem,
			RootDisk:  rootDisk,
			Image:     naming.RunnerImageRef(planSlug, "opencode"),
			Volumes: map[string]msbSdk.MountConfig{
				mounts.TmpMountPath:       {SizeMiB: tmpMiB},
				mounts.WorkspaceMountPath: {QuotaMiB: options.DefaultWorkspaceQuotaMiB},
				mounts.VMHomeDir:          {Named: "agents-sandbox-home-proj-vol"},
			},
		}
	}

	cases := []struct {
		name    string
		cfg     *msbSdk.SandboxConfig
		image   naming.ImageIdentity
		opts    options.RunOptions
		homeVol string
		want    bool
	}{
		{
			name:    "image mismatch",
			cfg:     mkConfig(4, 4096, 0, 2048),
			image:   naming.ImageIdentity{Slug: planSlug, Agent: "pi"},
			opts:    options.RunOptions{},
			homeVol: "agents-sandbox-home-proj-vol",
			want:    true,
		},
		{
			name:    "tmpfs mismatch",
			cfg:     mkConfig(4, 4096, 0, 2048),
			image:   planImage(),
			opts:    options.RunOptions{TmpSize: "1G"},
			homeVol: "agents-sandbox-home-proj-vol",
			want:    true,
		},
		{
			name:    "workspace quota mismatch",
			cfg:     mkConfig(4, 4096, 0, 2048),
			image:   planImage(),
			opts:    options.RunOptions{WorkspaceQuota: "32G"},
			homeVol: "agents-sandbox-home-proj-vol",
			want:    true,
		},
		{
			name:    "workspace quota unset ignores quota",
			cfg:     mkConfig(4, 4096, 0, 2048),
			image:   planImage(),
			opts:    options.RunOptions{},
			homeVol: "agents-sandbox-home-proj-vol",
			want:    false,
		},
		{
			name:    "larger disk grows live",
			cfg:     mkConfig(4, 4096, 8192, 2048),
			image:   planImage(),
			opts:    options.RunOptions{DiskSize: "16G"},
			homeVol: "agents-sandbox-home-proj-vol",
			want:    false,
		},
		{
			name:    "smaller disk recreates",
			cfg:     mkConfig(4, 4096, 16384, 2048),
			image:   planImage(),
			opts:    options.RunOptions{DiskSize: "8G"},
			homeVol: "agents-sandbox-home-proj-vol",
			want:    true,
		},
		{
			name:    "disk unset ignores disk",
			cfg:     mkConfig(4, 4096, 8192, 2048),
			image:   planImage(),
			opts:    options.RunOptions{},
			homeVol: "agents-sandbox-home-proj-vol",
			want:    false,
		},
		{
			name:    "home volume mismatch",
			cfg:     mkConfig(4, 4096, 0, 2048),
			image:   planImage(),
			opts:    options.RunOptions{},
			homeVol: "agents-sandbox-home-proj-new",
			want:    true,
		},
		{
			name:    "no change",
			cfg:     mkConfig(4, 4096, 16384, 2048),
			image:   planImage(),
			opts:    options.RunOptions{TmpSize: "2G", DiskSize: "16G"},
			homeVol: "agents-sandbox-home-proj-vol",
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PlanReconfig(tc.cfg, tc.image, tc.opts, ChangeFlags{}, tc.homeVol).Recreate
			if got != tc.want {
				t.Errorf("PlanReconfig().Recreate = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPlanReconfigServeHostPortReuse(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Image: naming.RunnerImageRef(planSlug, "opencode"),
		PortBindings: []msbSdk.PortBinding{
			{Bind: "127.0.0.1", HostPort: 4096, GuestPort: 4096, Protocol: msbSdk.PortProtocolTCP},
		},
	}
	opts := options.RunOptions{ServeOnly: true}
	plan := PlanReconfig(cfg, planImage(), opts, ChangeFlags{}, "")
	if plan.Recreate {
		t.Error("expected no recreate when reusing the existing binding")
	}
	if plan.ServeHostPort != 4096 {
		t.Errorf("ServeHostPort = %d, want 4096", plan.ServeHostPort)
	}
}

func TestPlanReconfigServeOnly(t *testing.T) {
	desired := ports.ServeOnlyBindings(ports.ServeOnlyBasePort)
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

func TestPlanReconfigUserPorts(t *testing.T) {
	userBinding := msbSdk.PortBinding{
		Bind: "127.0.0.1", HostPort: 3000, GuestPort: 3000, Protocol: msbSdk.PortProtocolTCP,
	}
	tests := []struct {
		name         string
		ports        []ports.PortMapping
		cfgPorts     []msbSdk.PortBinding
		wantRecreate bool
	}{
		{"user port added", nil, []msbSdk.PortBinding{userBinding}, true},
		{
			"user port unchanged",
			[]ports.PortMapping{{HostPort: 3000, GuestPort: 3000}},
			[]msbSdk.PortBinding{userBinding},
			false,
		},
		{"user port removed", []ports.PortMapping{{HostPort: 3000, GuestPort: 3000}}, nil, true},
		{
			"user port changed",
			[]ports.PortMapping{{HostPort: 3000, GuestPort: 5173}},
			[]msbSdk.PortBinding{userBinding},
			true,
		},
		{
			"binding order is irrelevant",
			[]ports.PortMapping{{HostPort: 3000, GuestPort: 3000}, {HostPort: 8080, GuestPort: 5173}},
			[]msbSdk.PortBinding{
				{Bind: "127.0.0.1", HostPort: 8080, GuestPort: 5173, Protocol: msbSdk.PortProtocolTCP},
				userBinding,
			},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &msbSdk.SandboxConfig{PortBindings: tt.cfgPorts}
			plan := PlanReconfig(
				cfg,
				planImage(),
				options.RunOptions{Ports: tt.ports},
				ChangeFlags{},
				"",
			)
			if plan.Recreate != tt.wantRecreate {
				t.Errorf("Recreate = %v, want %v", plan.Recreate, tt.wantRecreate)
			}
		})
	}
}

func TestPlanReconfigUserPortsWithServeOnly(t *testing.T) {
	serveBinding := msbSdk.PortBinding{
		Bind: "127.0.0.1", HostPort: 4096, GuestPort: 4096, Protocol: msbSdk.PortProtocolTCP,
	}
	userBinding := msbSdk.PortBinding{
		Bind: "127.0.0.1", HostPort: 3000, GuestPort: 3000, Protocol: msbSdk.PortProtocolTCP,
	}
	cfg := &msbSdk.SandboxConfig{PortBindings: []msbSdk.PortBinding{userBinding, serveBinding}}
	opts := options.RunOptions{
		ServeOnly: true,
		Ports:     []ports.PortMapping{{HostPort: 3000, GuestPort: 3000}},
	}
	plan := PlanReconfig(cfg, planImage(), opts, ChangeFlags{}, "")
	if plan.Recreate {
		t.Error("expected no recreate when serve-only and user ports both match")
	}
	if plan.ServeHostPort != 4096 {
		t.Errorf("ServeHostPort = %d, want 4096 (the agent guest port binding)", plan.ServeHostPort)
	}
}

// TestPlanReconfigChangeFlagsDecideRecreateOrRestart pins what each change the
// launcher detects from a fingerprint does: env, secrets, network and host bind
// mounts cannot be applied live and recreate the VM, while an agent config
// change only restarts the daemon. With no flag set nothing happens.
func TestPlanReconfigChangeFlagsDecideRecreateOrRestart(t *testing.T) {
	for _, tc := range []struct {
		name         string
		flags        ChangeFlags
		wantRecreate bool
		wantRestart  bool
	}{
		{name: "nothing changed"},
		{name: "env", flags: ChangeFlags{Env: changedSetting()}, wantRecreate: true},
		{name: "secrets", flags: ChangeFlags{Secrets: changedSetting()}, wantRecreate: true},
		{name: "network", flags: ChangeFlags{Network: changedSetting()}, wantRecreate: true},
		{name: "agent config", flags: ChangeFlags{AgentConfig: []string{agentConfigPath}}, wantRestart: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &msbSdk.SandboxConfig{Image: naming.RunnerImageRef(planSlug, "opencode"), CPUs: 4, MemoryMiB: 4096}
			plan := PlanReconfig(cfg, planImage(), options.RunOptions{}, tc.flags, "")
			if plan.Recreate != tc.wantRecreate || plan.RestartDaemons != tc.wantRestart {
				t.Errorf("PlanReconfig: recreate=%v restart=%v, want recreate=%v restart=%v",
					plan.Recreate, plan.RestartDaemons, tc.wantRecreate, tc.wantRestart)
			}
		})
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
	plan := PlanReconfig(cfg, planImage(), options.RunOptions{}, ChangeFlags{Mounts: changedSetting()}, "")
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

func TestPlanReconfigAgentConfigSuppressedByRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Image: naming.ContentImageRef(planSlug, "opencode", "4b8d0e6f2a1c3b57"), CPUs: 4, MemoryMiB: 4096,
	}
	plan := PlanReconfig(
		cfg,
		planImage(),
		options.RunOptions{},
		ChangeFlags{AgentConfig: []string{agentConfigPath}},
		"",
	)
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

// TestPlanReconfigRecreateSuppressesEnvChange pins that env/secret changes fold
// into an already-decided recreate: they must not be appended as extra change
// lines, which would misreport why the VM is rebuilt.
func TestPlanReconfigRecreateSuppressesEnvChange(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Image: naming.ContentImageRef(planSlug, "opencode", "4b8d0e6f2a1c3b57"), CPUs: 4, MemoryMiB: 4096,
	}
	plan := PlanReconfig(
		cfg,
		planImage(),
		options.RunOptions{},
		ChangeFlags{Env: changedSetting(), Secrets: changedSetting()},
		"",
	)
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
	plan := PlanReconfig(
		cfg,
		planNameImage(),
		options.RunOptions{},
		ChangeFlags{AgentConfig: []string{agentConfigPath}},
		"",
	)
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

func TestPlanReconfigRecreateOnTmpMismatch(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Volumes: map[string]msbSdk.MountConfig{mounts.TmpMountPath: {SizeMiB: 2048}},
	}
	d := PlanReconfig(cfg, planImage(), options.RunOptions{TmpSize: "4G"},
		ChangeFlags{}, "")
	if !d.Recreate {
		t.Error("expected recreate on /tmp size mismatch")
	}
	if len(d.Changes) != 1 || d.Changes[0].Label != "/tmp tmpfs size" {
		t.Errorf("expected one /tmp change, got %+v", d.Changes)
	}
}

func TestPlanReconfigRecreateOnImageMismatch(t *testing.T) {
	d := PlanReconfig(nil, planImage(), options.RunOptions{}, ChangeFlags{}, "")
	if d.Recreate {
		t.Error("image comparison requires cfg.Image; see resolver, planner.cpp nil-cfg safe")
	}
	cfg := &msbSdk.SandboxConfig{Image: naming.ContentImageRef(planSlug, "opencode", "4b8d0e6f2a1c3b57")}
	d = PlanReconfig(cfg, planImage(), options.RunOptions{}, ChangeFlags{}, "")
	if !d.Recreate {
		t.Error("expected recreate on image mismatch")
	}
}

func TestPlanReconfigRecreateOnImageDigestChange(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: naming.DockerImageRef(planSlug, "opencode")}

	d := PlanReconfig(cfg, planImage(), options.RunOptions{}, ChangeFlags{Image: true}, "")
	if !d.Recreate {
		t.Fatal("expected recreate when the image digest changes")
	}
	if d.RestartDaemons {
		t.Error("expected image changes to recreate the VM instead of restarting daemons")
	}
	if len(d.Changes) != 1 || d.Changes[0].Label != changeLabelImage {
		t.Errorf("expected one image change, got %+v", d.Changes)
	}
}

func TestPlanReconfigNoActionsWhenUnchanged(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{CPUs: 4, MemoryMiB: 4096}
	d := PlanReconfig(cfg, planImage(), options.RunOptions{CPUs: 4, Memory: "4G"},
		ChangeFlags{}, "")
	if d.Recreate || d.RestartDaemons {
		t.Errorf("expected no actions, got %+v", d)
	}
}

func TestPlanReconfigEnvWithRecreateNoRestartFlag(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Image: naming.ContentImageRef(planSlug, "opencode", "4b8d0e6f2a1c3b57"), CPUs: 4, MemoryMiB: 4096,
		Volumes: map[string]msbSdk.MountConfig{
			mounts.TmpMountPath: {SizeMiB: 2048},
		},
	}
	// image mismatch triggers recreate, env change would add restartDaemons
	d := PlanReconfig(cfg, planImage(), options.RunOptions{TmpSize: "4G"}, ChangeFlags{Env: changedSetting()}, "")
	if !d.Recreate {
		t.Error("expected recreate on image mismatch")
	}
	if d.RestartDaemons {
		t.Error("expected restartDaemons=false when recreate is set (folded into recreate)")
	}
}

// TestPlanReconfigMemoryAboveBootMaxRecreates pins the memory-above-boot-max
// contract end to end: 16G against a 8192 MiB boot maximum recreates with a
// descriptive change line and stages no clamped live memory change.
func TestPlanReconfigMemoryAboveBootMaxRecreates(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Image:        naming.RunnerImageRef(planSlug, "opencode"),
		MemoryMiB:    4096,
		MaxMemoryMiB: 8192,
	}
	d := PlanReconfig(cfg, planImage(), options.RunOptions{Memory: "16G"}, ChangeFlags{BootMaxMemoryGrew: true}, "")
	if !d.Recreate {
		t.Fatal("expected a recreate for a memory raise above the boot maximum")
	}
	found := false
	for _, c := range d.Changes {
		if c.Label == changeLabelMemoryAboveBootMax {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a %q change line, got %+v", changeLabelMemoryAboveBootMax, d.Changes)
	}
}

// TestPlanReconfigMemoryAboveBootMaxSuppressesAgentConfig pins that the
// above-boot-max recreate folds an agent-config change the same way every other
// recreate does: no daemon restart alongside it.
func TestPlanReconfigMemoryAboveBootMaxSuppressesAgentConfig(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Image:        naming.RunnerImageRef(planSlug, "opencode"),
		MemoryMiB:    4096,
		MaxMemoryMiB: 8192,
	}
	d := PlanReconfig(
		cfg,
		planImage(),
		options.RunOptions{Memory: "16G"},
		ChangeFlags{AgentConfig: []string{agentConfigPath}, BootMaxMemoryGrew: true},
		"",
	)
	if !d.Recreate {
		t.Fatal("expected a recreate for a memory raise above the boot maximum")
	}
	if d.RestartDaemons {
		t.Error("agent config must not restart daemons when the VM is recreated anyway")
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

// TestTmpSizeChangeClampedAtBootMax pins that a tmp above the live VM's boot
// maximum is compared capped, mirroring the creation-side clamp
// (vm.effectiveTmpSizeMiB), so a host-RAM clamp cannot recreate the VM on every
// run.
func TestTmpSizeChangeClampedAtBootMax(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		MaxMemoryMiB: 32 * 1024,
		Volumes:      map[string]msbSdk.MountConfig{mounts.TmpMountPath: {SizeMiB: 32 * 1024}},
	}
	if _, changed := tmpSizeChange(cfg, options.RunOptions{TmpSize: "48G"}); changed {
		t.Error("a tmp above the boot maximum was created clamped to it; it must not be a change")
	}
}

func TestWorkspaceQuotaChange(t *testing.T) {
	vol := func(quotaMiB uint32) map[string]msbSdk.MountConfig {
		return map[string]msbSdk.MountConfig{mounts.WorkspaceMountPath: {QuotaMiB: quotaMiB}}
	}
	tests := []struct {
		name    string
		volumes map[string]msbSdk.MountConfig
		spec    string
		want    bool
	}{
		{name: "missing volume", volumes: nil, spec: "32G", want: false},
		{name: "invalid spec", volumes: vol(1024), spec: "not-a-size", want: false},
		{
			name:    "equal quota",
			volumes: vol(options.DefaultWorkspaceQuotaMiB),
			spec:    FormatSizeSpec(options.DefaultWorkspaceQuotaMiB, ""),
			want:    false,
		},
		{name: "different quota", volumes: vol(1024), spec: "32G", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &msbSdk.SandboxConfig{Volumes: tc.volumes}
			if _, changed := workspaceQuotaChange(
				cfg,
				options.RunOptions{WorkspaceQuota: tc.spec},
			); changed != tc.want {
				t.Errorf("workspaceQuotaChange() changed = %v, want %v", changed, tc.want)
			}
		})
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

// TestRootDiskChangeOldValue pins which root-disk changes need a recreate: a
// decrease (a managed disk only grows) and an unrecorded disk, not an increase,
// which the live reconcile grows.
func TestRootDiskChangeOldValue(t *testing.T) {
	disk := msbSdk.RootDisk.Managed(16384)
	cfg := &msbSdk.SandboxConfig{RootDisk: &disk}
	change, changed := rootDiskChange(cfg, options.RunOptions{DiskSize: "8G"})
	if !changed {
		t.Fatal("expected a change for a smaller root disk")
	}
	if change.Label != "root disk size" || change.Old != "16384M" || change.New != "8G" {
		t.Errorf("rootDiskChange() = %+v", change)
	}
	if _, changed := rootDiskChange(cfg, options.RunOptions{DiskSize: "32G"}); changed {
		t.Error("a larger root disk must not be a recreate change")
	}
	if _, changed := rootDiskChange(&msbSdk.SandboxConfig{}, options.RunOptions{DiskSize: "16G"}); !changed {
		t.Error("a nil root disk with a requested size must be a change")
	}
}

func TestPublishReconfigHostPort(t *testing.T) {
	if hostPort, _, changed := publishReconfig(
		options.RunOptions{},
		&msbSdk.SandboxConfig{},
	); hostPort != 0 ||
		changed {
		t.Errorf("not serve-only: hostPort=%d changed=%v, want 0 false", hostPort, changed)
	}
	cfg := &msbSdk.SandboxConfig{PortBindings: ports.ServeOnlyBindings(4097)}
	if hostPort, _, changed := publishReconfig(options.RunOptions{ServeOnly: true}, cfg); hostPort != 4097 || changed {
		t.Errorf("serve-only reuse: hostPort=%d changed=%v, want 4097 false", hostPort, changed)
	}
}

// TestMemoryAboveBootMaxForcesRecreate pins that a desired memory above the VM's
// boot maximum can never be applied live: the plan recreates instead of staging a
// clamped Modify.
func TestMemoryAboveBootMaxForcesRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{CPUs: 4, MemoryMiB: 4096, MaxMemoryMiB: 4096}
	plan := PlanReconfig(cfg, planImage(), options.RunOptions{Memory: "8G"}, ChangeFlags{BootMaxMemoryGrew: true}, "")
	if !plan.Recreate {
		t.Fatal("expected recreate for a memory raise above the boot maximum")
	}
}

// TestMemoryAboveBootMaxRequiresARequestedMemory pins the opts.Memory != ""
// operand: with no memory requested the helper reports false even though the
// default parse would exceed a small boot maximum.
func TestMemoryAboveBootMaxRequiresARequestedMemory(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{MemoryMiB: 1024, MaxMemoryMiB: 2048}
	if memoryAboveBootMax(cfg, options.RunOptions{}) {
		t.Error("memoryAboveBootMax with no requested memory = true, want false")
	}
}

// TestMemoryAboveBootMaxNeedsAKnownBootMaximum pins that an unknown boot
// maximum (zero) never triggers the memory recreate, while any known one does.
func TestMemoryAboveBootMaxNeedsAKnownBootMaximum(t *testing.T) {
	tests := []struct {
		maxMemoryMiB uint32
		wantRecreate bool
	}{
		{maxMemoryMiB: 0, wantRecreate: false},
		{maxMemoryMiB: 1, wantRecreate: true},
	}
	for _, tc := range tests {
		cfg := &msbSdk.SandboxConfig{CPUs: 4, MemoryMiB: 1, MaxMemoryMiB: tc.maxMemoryMiB}
		plan := PlanReconfig(
			cfg,
			planImage(),
			options.RunOptions{Memory: "8G"},
			ChangeFlags{BootMaxMemoryGrew: true},
			"",
		)
		if plan.Recreate != tc.wantRecreate {
			t.Errorf("boot maximum %d MiB: recreate = %v, want %v", tc.maxMemoryMiB, plan.Recreate, tc.wantRecreate)
		}
	}
}

// TestMemoryAboveAnUnchangedBootMaxDoesNotRecreate pins that a recreate is
// planned only when it would raise the boot maximum: when a fresh VM would get
// the same maximum, a recreate could not apply the value either and would
// repeat on every launch; the live reconcile clamps it instead.
func TestMemoryAboveAnUnchangedBootMaxDoesNotRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{CPUs: 4, MemoryMiB: 4096, MaxMemoryMiB: 6144}
	plan := PlanReconfig(cfg, planImage(), options.RunOptions{Memory: "8G"}, ChangeFlags{}, "")
	if plan.Recreate {
		t.Fatal("expected no recreate when a fresh VM would get the same boot maximum")
	}
}

// TestMemoryAtOrBelowBootMaxStaysLive pins that a desired memory within (or equal
// to) the boot maximum is left to the live reconcile: only an above-max raise
// recreates.
func TestMemoryAtOrBelowBootMaxStaysLive(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{MemoryMiB: 4096, MaxMemoryMiB: 8192}
	plan := PlanReconfig(cfg, planImage(), options.RunOptions{Memory: "8G"}, ChangeFlags{BootMaxMemoryGrew: true}, "")
	if plan.Recreate {
		t.Fatal("expected no recreate for a memory raise within the boot maximum")
	}
}

// TestResolveReconfigAppliesAloneAndAsksWithAttachedClients pins the apply
// decision. Without other clients a recreate or restart applies with a
// warning listing the changes. With attached clients the prompt decides: keep defers,
// quit aborts with an error, restart applies a restart, and a failed prompt
// defers even when it also reports the quit key.
func TestResolveReconfigAppliesAloneAndAsksWithAttachedClients(t *testing.T) {
	recreate := &Plan{Recreate: true, Changes: []Change{{Label: "root disk size"}}}
	restart := &Plan{RestartDaemons: true, Changes: []Change{{Label: "agent config"}}}
	for _, tc := range []struct {
		name         string
		plan         *Plan
		otherClients int
		selectKey    string
		selectErr    error
		want         ApplyActions
		wantErr      bool
	}{
		{name: "nothing to apply", plan: &Plan{}, otherClients: 1},
		{name: "recreate alone", plan: recreate, want: ApplyActions{Recreate: true}},
		{name: "recreate, keep", plan: recreate, otherClients: 1, selectKey: keepKey},
		{name: "recreate, quit", plan: recreate, otherClients: 1, selectKey: quitKey, wantErr: true},
		{
			name: "recreate, prompt fails", plan: recreate, otherClients: 1,
			selectKey: quitKey, selectErr: errors.New("select failed"),
		},
		{name: "restart alone", plan: restart, want: ApplyActions{Restart: true}},
		{name: "restart, keep", plan: restart, otherClients: 1, selectKey: keepKey},
		{name: "restart, quit", plan: restart, otherClients: 1, selectKey: quitKey, wantErr: true},
		{
			name: "restart, restart", plan: restart, otherClients: 1, selectKey: restartKey,
			want: ApplyActions{Restart: true},
		},
		{
			name: "restart, prompt fails", plan: restart, otherClients: 1,
			selectKey: quitKey, selectErr: errors.New("select failed"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ui := &termio.Mock{}
			var prompt string
			ui.SelectFn = func(message string, _ []termio.Choice, _ string) (string, error) {
				prompt = message
				return tc.selectKey, tc.selectErr
			}
			actions, err := ResolveReconfig(context.Background(), ui, tc.plan, tc.otherClients, tc.plan.Changes)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ResolveReconfig error = %v, wantErr %v", err, tc.wantErr)
			}
			if actions != tc.want {
				t.Errorf("ResolveReconfig = %+v, want %+v", actions, tc.want)
			}
			prompted := tc.selectKey != ""
			if prompted && !strings.Contains(prompt, tc.plan.Changes[0].Label) {
				t.Errorf("prompt = %q, want it to list the change %q", prompt, tc.plan.Changes[0].Label)
			}
			if !prompted && prompt != "" {
				t.Errorf("prompted %q, want no prompt", prompt)
			}
			appliedAlone := tc.otherClients == 0 && (tc.want.Recreate || tc.want.Restart)
			if appliedAlone && !strings.Contains(strings.Join(ui.WarnCalls, "\n"), tc.plan.Changes[0].Label) {
				t.Errorf(
					"warnings = %q, want one naming the change %q when applying alone",
					ui.WarnCalls,
					tc.plan.Changes[0].Label,
				)
			}
		})
	}
}

// TestResolveReconfigWarnsWithTheResolvedChangesWhenAlone pins #230's
// visibility rule: applying without other clients is announced as a warning
// that carries every change with its resolved values, not as an info line with
// the reasons hidden behind --verbose.
func TestResolveReconfigWarnsWithTheResolvedChangesWhenAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		plan *Plan
		want string
	}{
		{
			name: "rebuild",
			plan: &Plan{Recreate: true, Changes: []Change{
				{Label: changeLabelBindMounts, Details: []string{"+ /host ← / (read-write)"}},
				{Label: "secrets"},
			}},
			want: "Project VM config changed; rebuilding the project VM (no other client attached):\n" +
				"  - host bind mounts:\n" +
				"      + /host ← / (read-write)\n" +
				"  - secrets",
		},
		{
			name: "restart",
			plan: &Plan{RestartDaemons: true, Changes: []Change{
				{Label: changeLabelAgentConfig, Details: []string{agentConfigPath}},
			}},
			want: "Project VM config changed; restarting the agent daemon (no other client attached):\n" +
				"  - agent config:\n" +
				"      " + agentConfigPath,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ui := &termio.Mock{}
			if _, err := ResolveReconfig(context.Background(), ui, tc.plan, 0, tc.plan.Changes); err != nil {
				t.Fatalf("ResolveReconfig: %v", err)
			}
			if len(ui.WarnCalls) != 1 || ui.WarnCalls[0] != tc.want {
				t.Errorf("warnings = %q, want [%q]", ui.WarnCalls, tc.want)
			}
			if len(ui.InfoCalls) != 0 {
				t.Errorf("info lines = %q, want none", ui.InfoCalls)
			}
		})
	}
}

// TestChangeListEscapesControlCharacters pins that a configured value cannot
// hide or fake lines of the change list: a mount path carrying a terminal escape
// sequence, a newline, or a bidi override is shown escaped.
func TestChangeListEscapesControlCharacters(t *testing.T) {
	got := configChangeList([]Change{
		{Label: changeLabelBindMounts, Details: []string{"+ /a\x1b[2J\n  - fake ← / (read-write)"}},
		{Label: changeLabelImage, Old: "old\u202eimage", New: "new"},
	})
	want := "Project VM config changed:\n" +
		"  - host bind mounts:\n" +
		`      + /a\x1b[2J\n  - fake ← / (read-write)` + "\n" +
		`  - image: old\u202eimage → new`
	if got != want {
		t.Errorf("configChangeList() = %q, want %q", got, want)
	}
}

// TestSettingChangeShowsTheEntriesThatChanged pins how a fingerprinted
// setting's change is shown: removed and added entries when the applied ones
// were recorded, the kind-specific note when only values outside the entries
// changed, and every desired entry when the previous ones were never recorded.
func TestSettingChangeShowsTheEntriesThatChanged(t *testing.T) {
	for _, tc := range []struct {
		name string
		diff SettingDiff
		want []string
	}{
		{
			name: "entries added and removed",
			diff: SettingDiff{Changed: true, AppliedRecorded: true, Applied: []string{"a", "b"}, Desired: []string{"b", "c"}},
			want: []string{"- a", "+ c"},
		},
		{
			name: "same entries",
			diff: SettingDiff{Changed: true, AppliedRecorded: true, Applied: []string{"a"}, Desired: []string{"a"}},
			want: []string{"values changed"},
		},
		{
			name: "previous entries not recorded",
			diff: SettingDiff{Changed: true, Desired: []string{"a", "b"}},
			want: []string{"(previous value not recorded)", "+ a", "+ b"},
		},
		{
			name: "everything removed",
			diff: SettingDiff{Changed: true, AppliedRecorded: true, Applied: []string{"a"}},
			want: []string{"- a"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := settingChange("label", "values changed", tc.diff)
			if got.Label != "label" || !slices.Equal(got.Details, tc.want) {
				t.Errorf("settingChange = %+v, want label with details %q", got, tc.want)
			}
		})
	}
}

// TestPlanReconfigShowsResolvedValues pins that each change the plan reports
// carries what changed, so a prompt or warning shows e.g. the host path a new
// mount exposes rather than a bare label.
func TestPlanReconfigShowsResolvedValues(t *testing.T) {
	newMount := SettingDiff{
		Changed: true, AppliedRecorded: true, Applied: []string{},
		Desired: []string{"/host ← / (read-write)"},
	}
	t.Run("mounts", func(t *testing.T) {
		plan := PlanReconfig(
			&msbSdk.SandboxConfig{},
			planImage(),
			options.RunOptions{},
			ChangeFlags{Mounts: newMount},
			"",
		)
		assertChange(t, plan, Change{Label: changeLabelBindMounts, Details: []string{"+ /host ← / (read-write)"}})
	})
	t.Run("network", func(t *testing.T) {
		widened := SettingDiff{
			Changed: true, AppliedRecorded: true,
			Applied: []string{"profile: none"}, Desired: []string{"profile: public"},
		}
		plan := PlanReconfig(
			&msbSdk.SandboxConfig{},
			planImage(),
			options.RunOptions{},
			ChangeFlags{Network: widened},
			"",
		)
		assertChange(t, plan, Change{
			Label: changeLabelNetworkPolicy, Details: []string{"- profile: none", "+ profile: public"},
		})
	})
	t.Run("env and secrets", func(t *testing.T) {
		rotated := SettingDiff{Changed: true, AppliedRecorded: true, Applied: []string{"A"}, Desired: []string{"A"}}
		plan := PlanReconfig(&msbSdk.SandboxConfig{}, planImage(), options.RunOptions{},
			ChangeFlags{Env: rotated, Secrets: rotated}, "")
		assertChange(t, plan, Change{Label: changeLabelEnv, Details: []string{"values changed"}})
		assertChange(
			t,
			plan,
			Change{Label: changeLabelSecrets, Details: []string{"values or other secret settings changed"}},
		)
	})
	t.Run("agent config", func(t *testing.T) {
		plan := PlanReconfig(&msbSdk.SandboxConfig{}, planImage(), options.RunOptions{},
			ChangeFlags{AgentConfig: []string{agentConfigPath}}, "")
		assertChange(t, plan, Change{Label: changeLabelAgentConfig, Details: []string{agentConfigPath}})
	})
	t.Run("image", func(t *testing.T) {
		cfg := &msbSdk.SandboxConfig{Image: "other-1mjusbm3wikhb0:opencode"}
		plan := PlanReconfig(cfg, planImage(), options.RunOptions{}, ChangeFlags{}, "")
		assertChange(t, plan, Change{Label: changeLabelImage, Old: cfg.Image, New: planImage().Ref()})
	})
	t.Run("image digest only", func(t *testing.T) {
		cfg := &msbSdk.SandboxConfig{Image: planImage().Ref()}
		plan := PlanReconfig(cfg, planImage(), options.RunOptions{}, ChangeFlags{Image: true}, "")
		assertChange(t, plan, Change{Label: changeLabelImage})
	})
	t.Run("home volume", func(t *testing.T) {
		cfg := &msbSdk.SandboxConfig{Volumes: map[string]msbSdk.MountConfig{
			mounts.VMHomeDir: {Named: "old-home"},
		}}
		plan := PlanReconfig(cfg, planImage(), options.RunOptions{}, ChangeFlags{}, "new-home")
		assertChange(t, plan, Change{Label: changeLabelHomeVolume, Old: "old-home", New: "new-home"})
	})
	t.Run("published ports", func(t *testing.T) {
		cfg := &msbSdk.SandboxConfig{PortBindings: []msbSdk.PortBinding{
			{Bind: "127.0.0.1", HostPort: 9000, GuestPort: 9000, Protocol: msbSdk.PortProtocolTCP},
			{Bind: "127.0.0.1", HostPort: 3000, GuestPort: 3000, Protocol: msbSdk.PortProtocolTCP},
		}}
		opts := options.RunOptions{Ports: []ports.PortMapping{{HostPort: 8080, GuestPort: 5173}}}
		plan := PlanReconfig(cfg, planImage(), opts, ChangeFlags{}, "")
		assertChange(t, plan, Change{Label: changeLabelPublishedPorts, Details: []string{
			"- 127.0.0.1:3000 → 3000/tcp", "- 127.0.0.1:9000 → 9000/tcp", "+ 127.0.0.1:8080 → 5173/tcp",
		}})
	})
	t.Run("published ports keep the serve host port", func(t *testing.T) {
		cfg := &msbSdk.SandboxConfig{PortBindings: ports.ServeOnlyBindings(4555)}
		opts := options.RunOptions{ServeOnly: true, Ports: []ports.PortMapping{{HostPort: 8080, GuestPort: 5173}}}
		plan := PlanReconfig(cfg, planImage(), opts, ChangeFlags{}, "")
		if !plan.PortsChanged || plan.ServeHostPort != 4555 {
			t.Errorf("PortsChanged = %v, ServeHostPort = %d, want a change that keeps serving on 4555",
				plan.PortsChanged, plan.ServeHostPort)
		}
	})
}

func assertChange(t *testing.T, plan *Plan, want Change) {
	t.Helper()
	for _, change := range plan.Changes {
		if change.Label == want.Label {
			if change.Old != want.Old || change.New != want.New || !slices.Equal(change.Details, want.Details) {
				t.Errorf("change %q = %+v, want %+v", want.Label, change, want)
			}
			return
		}
	}
	t.Errorf("plan changes %+v carry no %q change", plan.Changes, want.Label)
}

func TestDesiredPublishBindingsDelegatesToOptions(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{PortBindings: []msbSdk.PortBinding{
		{Bind: "127.0.0.1", HostPort: 4097, GuestPort: 4096, Protocol: msbSdk.PortProtocolTCP},
	}}
	optsBindings := ports.ServeOnlyBindings(4097)
	planBindings := desiredPublishBindings(options.RunOptions{ServeOnly: true}, cfg)
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

func TestDesiredPublishBindingsNilWhenNothingPublished(t *testing.T) {
	got := desiredPublishBindings(options.RunOptions{}, nil)
	if got != nil {
		t.Errorf("expected nil when nothing is published, got %+v", got)
	}
}

func TestDesiredPublishBindingsIncludeUserPorts(t *testing.T) {
	opts := options.RunOptions{Ports: []ports.PortMapping{{HostPort: 8080, GuestPort: 5173}}}
	got := desiredPublishBindings(opts, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 binding, got %+v", got)
	}
	if got[0].HostPort != 8080 || got[0].GuestPort != 5173 {
		t.Errorf("binding = %+v, want host 8080 guest 5173", got[0])
	}
}

// agentConfigPath is a guest agent config file the plan tests report as changed.
const agentConfigPath = "/home/dev/.config/opencode/opencode.json"

// changedSetting is a fingerprinted setting reported as changed, with nothing
// recorded to show.
func changedSetting() SettingDiff {
	return SettingDiff{Changed: true}
}

// planSlug and planContentID identify the runner image the plan tests compare
// their persisted VM image against.
const (
	planSlug      = "proj-1mjusbm3wikhb0"
	planContentID = "9f2c1a7b4d6e0f31"
)

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
