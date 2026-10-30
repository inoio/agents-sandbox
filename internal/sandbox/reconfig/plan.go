// Package reconfig plans and resolves the reconfiguration of a project VM:
// which changes force a recreate, a daemon restart, or a live modification,
// and how to apply them given other attached clients.
package reconfig

import (
	"context"
	"errors"
	"strconv"
	"strings"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
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

// changeLabelList joins the change labels into a single comma-separated line.
func changeLabelList(changes []Change) string {
	labels := make([]string, 0, len(changes))
	for _, c := range changes {
		labels = append(labels, c.Label)
	}
	return strings.Join(labels, ", ")
}

// reportAloneChange logs the informational apply decision made without other
// attached clients and, at verbose level, the change labels that drove it.
func reportAloneChange(ui termio.UI, message, reasonLabel string, changes []Change) {
	ui.Infof("VM/config changed; %s (no other client attached)", message)
	if summary := changeLabelList(changes); summary != "" {
		ui.Verbosef("%s reasons: %s", reasonLabel, summary)
	}
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
// and the desired state. image is the structured identity of the runner image
// this run wants, so the comparison never parses a reference the launcher itself
// rendered; only the persisted cfg.Image is parsed, because it is read back from
// the VM. Returns a nil Plan when there is no existing config (first creation)
// or nothing needs to change.
func PlanReconfig(
	cfg *msbSdk.SandboxConfig,
	image naming.ImageIdentity,
	opts options.RunOptions,
	flags ChangeFlags,
	homeVol string,
) *Plan {
	d := &Plan{}
	if cfg == nil {
		return d
	}

	// Recreation triggers (cannot be changed live).
	if imageRecreate(cfg, image, flags.Image) {
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
func imageRecreate(cfg *msbSdk.SandboxConfig, image naming.ImageIdentity, flag bool) bool {
	if flag {
		return true
	}
	return image.Ref() != "" && cfg.Image != "" && !sameImageRef(cfg.Image, image)
}

// sameImageRef reports whether a persisted image reference names the same runner
// image as the given structured identity, tolerating the legacy prefixed,
// prefix-free and content-addressed spellings of one (slug, agent) so a VM
// created under one spelling is not recreated merely because the launcher now
// computes another. A digest-pinned or non-runner persisted reference names no
// (slug, agent) pair, so it is never the same image. Two content-addressed
// references are the same image only when their content ids match: the persisted
// image digest that normally gates a switch is recorded by the home action
// before the VM is replaced, so after a failed replacement the content ref is
// the remaining signal that the live VM is on the old content.
func sameImageRef(persisted string, image naming.ImageIdentity) bool {
	parsed := naming.ParseImageTag(persisted)
	if !isNameImage(parsed) {
		return false
	}
	if parsed.Slug != image.Slug || parsed.Agent != image.Agent {
		return false
	}
	// Both content-addressed: the content id decides.
	if parsed.ContentID != "" && image.ContentID != "" {
		return parsed.ContentID == image.ContentID
	}
	// At least one is a name/legacy spelling (the upgrade from a 0.6.0 or
	// pre-0.6 VM): same slug+agent is the same image.
	return true
}

// isNameImage reports whether info parsed to a recognizable project name
// reference: a slug and agent with no digest. A foreign or digest-pinned
// reference has no agent, while a content-addressed ref is still a name image.
func isNameImage(info naming.ArtifactInfo) bool {
	return info.Slug != "" && info.Agent != "" && info.Digest == ""
}

// tmpSizeChange reports the /tmp tmpfs size change, if any.
func tmpSizeChange(cfg *msbSdk.SandboxConfig, opts options.RunOptions) (Change, bool) {
	want, ok := options.ParseMemoryOK(opts.TmpSize)
	if !ok {
		return Change{}, false
	}
	tmp, ok := cfg.Volumes[mounts.TmpMountPath]
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
	ws, ok := cfg.Volumes[mounts.WorkspaceMountPath]
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
	home, ok := cfg.Volumes[mounts.VMHomeDir]
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
	// The display form normalises back to "<size>M", so passing no raw spec is
	// identical to re-formatting the current size.
	return SizeChange("root disk size", diskMiBOr0(cfg), want, "", opts.DiskSize), true
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
	// A zero mo.CPUs is the unset sentinel, and clamping to a zero boot max also
	// yields zero, so no mutation of this guard can change the staged non-zero
	// CPU change.
	// gomutants:disable-next-line EXPRESSION_REMOVE,INVERT_LOGICAL,CONDITIONALS_BOUNDARY reason="opts.CPUs==0 or cfg.MaxCPUs==0 makes the clamp 0, and a zero mo.CPUs is the unset sentinel, so every listed guard mutation leaves the staged CPU change unchanged"
	if opts.CPUs != 0 && cfg.MaxCPUs > 0 {
		if want := min(opts.CPUs, cfg.MaxCPUs); want != cfg.CPUs {
			mo.CPUs = want
		}
	}
	if opts.Memory != "" {
		want := options.ParseMemory(opts.Memory)
		if cfg.MaxMemoryMiB > 0 {
			want = min(want, cfg.MaxMemoryMiB)
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

// ApplyActions names what a resolved reconfiguration should apply: a VM
// recreate, a daemon restart, or neither.
type ApplyActions struct {
	Recreate bool
	Restart  bool
}

// ResolveReconfig resolves a Plan into concrete apply actions based on the
// number of other attached clients.
func ResolveReconfig(
	ctx context.Context,
	ui termio.UI,
	plan *Plan,
	otherClientCount int,
	changes []Change,
) (ApplyActions, error) {
	_ = ctx
	if plan.Recreate {
		if otherClientCount == 0 {
			reportAloneChange(ui, "rebuilding project VM", "rebuild", changes)
			return ApplyActions{Recreate: true, Restart: false}, nil
		}
		key, err := PromptA(ui, changes, otherClientCount)
		if err != nil {
			return ApplyActions{}, nil //nolint:nilerr // brief: swallow select errors
		}
		if key == quitKey {
			return ApplyActions{}, errors.New("config apply aborted by user")
		}
		return ApplyActions{}, nil // keep
	}
	if plan.RestartDaemons {
		if otherClientCount == 0 {
			reportAloneChange(ui, "restarting daemons", "restart", changes)
			return ApplyActions{Recreate: false, Restart: true}, nil
		}
		key, err := PromptB(ui, changes, otherClientCount)
		if err != nil {
			return ApplyActions{}, nil //nolint:nilerr // brief: swallow select errors
		}
		if key == quitKey {
			return ApplyActions{}, errors.New("config apply aborted by user")
		}
		return ApplyActions{Recreate: false, Restart: key == restartKey}, nil
	}
	return ApplyActions{}, nil
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
