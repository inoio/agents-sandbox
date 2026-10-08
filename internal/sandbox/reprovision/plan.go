package reprovision

import (
	"context"
	"errors"
	"strconv"
	"strings"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

const changeLabelPublishedPorts = "published port(s)"
const changeLabelNetworkPolicy = "network policy"
const changeLabelBindMounts = "host bind mounts"
const changeLabelImage = "image"

// Change describes one changed setting for prompt display. Values are
// shown for simple sizes/counts; env/secrets/config carry labels only (Old/New empty).
//
//exhaustruct:ignore
type Change struct {
	Label string
	Old   string
	New   string
}

// FormatSizeSpec returns the raw user spec verbatim when it matches valueMiB,
// else the normalized "<valueMiB>M" form.
func FormatSizeSpec(valueMiB uint32, raw string) string {
	if raw != "" && options.ParseMemory(raw) == valueMiB {
		return raw
	}
	return strconv.FormatUint(uint64(valueMiB), 10) + "M"
}

// SizeChange is a helper to create a Change with formatted size values.
func SizeChange(label string, old, newSize uint32, oldRaw, newRaw string) Change {
	return Change{
		Label: label,
		Old:   FormatSizeSpec(old, oldRaw),
		New:   FormatSizeSpec(newSize, newRaw),
	}
}

// Plan captures the reconfiguration decision for a project VM.
//
//exhaustruct:ignore
type Plan struct {
	Recreate       bool
	RestartDaemons bool
	Resources      *msbSdk.ModifyOptions
	Changes        []Change
	// ServeHostPort is the resolved published host port for serve-only mode (0
	// when not serving).
	ServeHostPort int
}

func configChangeList(changes []Change) string {
	lines := []string{"Project VM config changed:"}
	for _, c := range changes {
		switch {
		case c.Old != "" && c.New != "":
			lines = append(lines, "  - "+c.Label+": "+c.Old+" → "+c.New)
		default:
			lines = append(lines, "  - "+c.Label)
		}
	}
	return strings.Join(lines, "\n")
}

// ChangeFlags carries the change detections that PlanReconfig cannot derive
// from the live VM config, because the microsandbox SDK does not round-trip
// these settings when reading an existing VM back. Each flag is computed by
// comparing a persisted fingerprint against the desired state.
type ChangeFlags struct {
	Image       bool
	Env         bool
	Secrets     bool
	Network     bool
	Mounts      bool
	AgentConfig bool
}

// PlanReconfig computes the reconfiguration plan given the current VM config
// and the desired state. Returns a nil Plan when there is no existing config
// (first creation) or nothing needs to change.
func PlanReconfig(
	cfg *msbSdk.SandboxConfig,
	imageRef string,
	opts options.RunOptions,
	flags ChangeFlags,
	homeVol string,
) *Plan {
	d := &Plan{}
	if cfg == nil {
		return d
	}

	// Recreation triggers (cannot be changed live).
	if imageRecreate(cfg, imageRef, flags.Image) {
		d.addRecreate(Change{Label: changeLabelImage})
	}
	if change, ok := tmpSizeChange(cfg, opts); ok {
		d.addRecreate(change)
	}
	if change, ok := workspaceQuotaChange(cfg, opts); ok {
		d.addRecreate(change)
	}
	if homeVolumeChange(cfg, homeVol) {
		d.addRecreate(Change{Label: "home volume"})
	}
	if flags.Mounts {
		d.addRecreate(Change{Label: changeLabelBindMounts})
	}
	if change, ok := rootDiskChange(cfg, opts); ok {
		d.addRecreate(change)
	}

	// Port publish state (serve-only) must match; a mismatch requires recreate
	// since microsandbox published ports can only be set at VM creation.
	// Network policy is creation-only too; its comparison is fingerprint-based
	// (NetworkChanged) because the SDK does not round-trip network config.
	hostPort, portsChanged := publishReconfig(opts, cfg)
	d.ServeHostPort = hostPort
	if portsChanged {
		d.addRecreate(Change{Label: changeLabelPublishedPorts})
	}
	if flags.Network {
		d.addRecreate(Change{Label: changeLabelNetworkPolicy})
	}

	// Env/secret changes are folded into the rebuild tier (baked into the VM at
	// creation); agent config changes only need a daemon restart.
	if !d.Recreate {
		d.applyEnvSecretRecreate(flags)
	}
	if !d.Recreate && flags.AgentConfig {
		d.RestartDaemons = true
		d.Changes = append(d.Changes, Change{Label: "agent config"})
	}

	// cpu/memory are always staged for live Modify (clamped to boot max).
	if mo := resourceModification(cfg, opts); mo != nil {
		d.Resources = mo
	}
	return d
}

// addRecreate records a change that can only be applied by recreating the VM.
func (d *Plan) addRecreate(change Change) {
	d.Recreate = true
	d.Changes = append(d.Changes, change)
}

// applyEnvSecretRecreate adds env/secret changes as recreation triggers.
func (d *Plan) applyEnvSecretRecreate(flags ChangeFlags) {
	if flags.Env {
		d.addRecreate(Change{Label: "environment variables"})
	}
	if flags.Secrets {
		d.addRecreate(Change{Label: "secrets"})
	}
}

// imageRecreate reports whether the runner image differs from the configured one.
func imageRecreate(cfg *msbSdk.SandboxConfig, imageRef string, flag bool) bool {
	if flag {
		return true
	}
	return imageRef != "" && cfg.Image != "" && cfg.Image != imageRef
}

// tmpSizeChange reports the /tmp tmpfs size change, if any.
func tmpSizeChange(cfg *msbSdk.SandboxConfig, opts options.RunOptions) (Change, bool) {
	want, ok := options.ParseMemoryOK(opts.TmpSize)
	if !ok {
		return Change{}, false
	}
	tmp, ok := cfg.Volumes[tmpMountPath]
	if !ok || tmp.SizeMiB == want {
		return Change{}, false
	}
	return SizeChange("/tmp tmpfs size", tmp.SizeMiB, want, FormatSizeSpec(tmp.SizeMiB, ""), opts.TmpSize), true
}

// workspaceQuotaChange reports the /workspace write-quota change, if any.
func workspaceQuotaChange(cfg *msbSdk.SandboxConfig, opts options.RunOptions) (Change, bool) {
	want, ok := options.ParseMemoryOK(opts.WorkspaceQuota)
	if !ok {
		return Change{}, false
	}
	ws, ok := cfg.Volumes[workspaceMountPath]
	if !ok || ws.QuotaMiB == want {
		return Change{}, false
	}
	return SizeChange(
		"/workspace write quota",
		ws.QuotaMiB,
		want,
		FormatSizeSpec(ws.QuotaMiB, ""),
		opts.WorkspaceQuota,
	), true
}

// homeVolumeChange reports whether the requested home volume differs from the
// one baked into the VM. A migration/reset points state at a new volume, which
// can only take effect by recreating the VM with the new volume mounted.
func homeVolumeChange(cfg *msbSdk.SandboxConfig, homeVol string) bool {
	if homeVol == "" {
		return false
	}
	home, ok := cfg.Volumes[VMHomeDir]
	return ok && home.Named != homeVol
}

// rootDiskChange reports the root-disk size change, if any.
func rootDiskChange(cfg *msbSdk.SandboxConfig, opts options.RunOptions) (Change, bool) {
	want, ok := options.ParseMemoryOK(opts.DiskSize)
	if !ok {
		return Change{}, false
	}
	if cfg.RootDisk != nil && cfg.RootDisk.SizeMiB == want {
		return Change{}, false
	}
	oldRaw := ""
	if cfg.RootDisk != nil {
		oldRaw = FormatSizeSpec(cfg.RootDisk.SizeMiB, "")
	}
	return SizeChange("root disk size", diskMiBOr0(cfg), want, oldRaw, opts.DiskSize), true
}

// publishReconfig returns the resolved serve-only host port and whether the
// published port bindings differ from the configured ones.
func publishReconfig(opts options.RunOptions, cfg *msbSdk.SandboxConfig) (int, bool) {
	wantPorts := desiredPublishBindings(opts.ServeOnly, cfg)
	hostPort := 0
	if len(wantPorts) > 0 {
		hostPort = int(wantPorts[0].HostPort)
	}
	return hostPort, !portBindingsEqual(wantPorts, cfg.PortBindings)
}

// resourceModification returns the live cpu/memory modification to stage, or nil
// when neither changes. Values are clamped to the boot maximum.
func resourceModification(cfg *msbSdk.SandboxConfig, opts options.RunOptions) *msbSdk.ModifyOptions {
	var mo msbSdk.ModifyOptions
	if opts.CPUs != 0 && cfg.MaxCPUs > 0 {
		if want := min(opts.CPUs, cfg.MaxCPUs); want != cfg.CPUs {
			mo.CPUs = want
		}
	}
	if opts.Memory != "" {
		want := options.ParseMemory(opts.Memory)
		if cfg.MaxMemoryMiB > 0 && want > cfg.MaxMemoryMiB {
			want = cfg.MaxMemoryMiB
		}
		if want != cfg.MemoryMiB {
			mo.MemoryMiB = want
		}
	}
	if mo.CPUs == 0 && mo.MemoryMiB == 0 {
		return nil
	}
	mo.Policy = msbSdk.ModificationPolicyNoRestart
	return &mo
}

// ResolveReconfig resolves a Plan into concrete apply actions based on the
// number of other attached clients. Returns (applyRecreate, applyRestart, error).
func ResolveReconfig(
	ctx context.Context,
	ui termio.UI,
	plan *Plan,
	otherClientCount int,
	changes []Change,
) (bool, bool, error) {
	_ = ctx
	if plan.Recreate {
		if otherClientCount == 0 {
			ui.Infof("VM/config changed; rebuilding project VM (no other client attached)")
			return true, false, nil
		}
		key, err := PromptA(ui, changes, otherClientCount)
		if err != nil {
			return false, false, nil //nolint:nilerr // brief: swallow select errors
		}
		if key == quitKey {
			return false, false, errors.New("config apply aborted by user")
		}
		return false, false, nil // keep
	}
	if plan.RestartDaemons {
		if otherClientCount == 0 {
			ui.Infof("VM/config changed; restarting daemons (no other client attached)")
			return false, true, nil
		}
		key, err := PromptB(ui, changes, otherClientCount)
		if err != nil {
			return false, false, nil //nolint:nilerr // brief: swallow select errors
		}
		if key == quitKey {
			return false, false, errors.New("config apply aborted by user")
		}
		return false, key == restartKey, nil
	}
	return false, false, nil
}

func diskMiBOr0(cfg *msbSdk.SandboxConfig) uint32 {
	if cfg.RootDisk == nil {
		return 0
	}
	return cfg.RootDisk.SizeMiB
}

// desiredPublishBindings returns the port bindings agents-sandbox wants on the
// project VM. Serve-only publishes the agent port on the host loopback;
// otherwise nothing is published.
func desiredPublishBindings(serveOnly bool, cfg *msbSdk.SandboxConfig) []msbSdk.PortBinding {
	if !serveOnly {
		return nil
	}
	return options.ServeOnlyBindings(options.ResolveServeHostPort(cfg, true))
}

func portBindingsEqual(a, b []msbSdk.PortBinding) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
