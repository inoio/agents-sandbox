// Package reconfig plans and resolves the reconfiguration of a project VM:
// which changes force a recreate, a daemon restart, or a live modification,
// and how to apply them given other attached clients.
package reconfig

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/ports"
	"github.com/inoio/agents-sandbox/internal/termio"
)

const (
	changeLabelPublishedPorts     = "published port(s)"
	changeLabelNetworkPolicy      = "network policy"
	changeLabelBindMounts         = "host bind mounts"
	changeLabelImage              = "image"
	changeLabelHomeVolume         = "home volume"
	changeLabelEnv                = "environment variables"
	changeLabelSecrets            = "secrets"
	changeLabelAgentConfig        = "agent config"
	changeLabelMemoryAboveBootMax = "memory above boot maximum"
)

// Notes shown for a fingerprinted setting whose entries are unchanged although
// its fingerprint differs: the change lies in what the entries leave out.
const (
	valuesChangedNote       = "values changed"
	secretValuesChangedNote = "values or other secret settings changed"
	networkRuleChangedNote  = "emitted rules changed (no configured entry differs)"
)

// Change describes one changed setting for display. A single value shows as
// Old → New; a set of values (mounts, network entries, ports, changed files)
// shows as Details lines. Env and secret values are never part of a Change.
//
//exhaustruct:ignore
type Change struct {
	Label   string
	Old     string
	New     string
	Details []string
}

// SettingDiff is a fingerprinted setting's change detection with the display
// entries recorded when the VM was created (Applied) and those of this run
// (Desired). AppliedRecorded is false when the VM's state predates recorded
// entries, so the previous value cannot be shown.
type SettingDiff struct {
	Changed         bool
	AppliedRecorded bool
	Applied         []string
	Desired         []string
}

// settingChange renders a changed setting: the removed and added entries, the
// note when the entries match, or every desired entry when the applied ones
// were never recorded.
func settingChange(label, sameEntriesNote string, diff SettingDiff) Change {
	if !diff.AppliedRecorded {
		details := []string{"(previous value not recorded)"}
		for _, entry := range diff.Desired {
			details = append(details, "+ "+entry)
		}
		return Change{Label: label, Details: details}
	}
	details := entryDiff(diff.Applied, diff.Desired)
	if len(details) == 0 {
		details = []string{sameEntriesNote}
	}
	return Change{Label: label, Details: details}
}

// entryDiff lists the entries only in applied as "- entry", then the entries
// only in desired as "+ entry", each in its input order.
func entryDiff(applied, desired []string) []string {
	var lines []string
	for _, entry := range applied {
		if !slices.Contains(desired, entry) {
			lines = append(lines, "- "+entry)
		}
	}
	for _, entry := range desired {
		if !slices.Contains(applied, entry) {
			lines = append(lines, "+ "+entry)
		}
	}
	return lines
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
	Changes        []Change
	// ServeHostPort is the resolved published host port for serve-only mode (0
	// when not serving).
	ServeHostPort int
	// PortsChanged reports whether the desired published port bindings differ
	// from the ones the existing VM carries.
	PortsChanged bool
}

func configChangeList(changes []Change) string {
	return changeListUnder("Project VM config changed:", changes)
}

// changeListUnder renders the changes as an indented list below heading.
func changeListUnder(heading string, changes []Change) string {
	lines := []string{heading}
	for _, c := range changes {
		switch {
		case c.Old != "" && c.New != "":
			lines = append(
				lines,
				"  - "+termio.EscapeControl(c.Label)+": "+termio.EscapeControl(c.Old)+" → "+termio.EscapeControl(c.New),
			)
		case len(c.Details) > 0:
			lines = append(lines, "  - "+termio.EscapeControl(c.Label)+":")
			for _, detail := range c.Details {
				lines = append(lines, "      "+termio.EscapeControl(detail))
			}
		default:
			lines = append(lines, "  - "+termio.EscapeControl(c.Label))
		}
	}
	return strings.Join(lines, "\n")
}

// warnAloneChange announces a change applied without asking because no other
// client is attached. It is a warning that lists every change with its resolved
// values, so a rebuild that e.g. adds a host mount is visible before the agent
// attaches.
func warnAloneChange(ui termio.Logger, action string, changes []Change) {
	heading := "Project VM config changed; " + action + " (no other client attached):"
	ui.Warn(changeListUnder(heading, changes))
}

// ChangeFlags carries the change detections that PlanReconfig cannot derive
// from the live VM config, because the microsandbox SDK does not round-trip
// these settings when reading an existing VM back. Each flag is computed by
// comparing a persisted fingerprint against the desired state.
type ChangeFlags struct {
	Image   bool
	Env     SettingDiff
	Secrets SettingDiff
	Network SettingDiff
	Mounts  SettingDiff
	// AgentConfig lists the guest paths of agent config files that differ from
	// the running VM; empty means unchanged.
	AgentConfig []string
	// BootMaxMemoryGrew reports that a freshly created VM would boot with a
	// higher memory maximum than the live one (the host gained RAM), so a
	// recreate can apply memory above the live maximum.
	BootMaxMemoryGrew bool
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
		d.addRecreate(imageChange(cfg, image))
	}
	if change, ok := tmpSizeChange(cfg, opts); ok {
		d.addRecreate(change)
	}
	if change, ok := workspaceQuotaChange(cfg, opts); ok {
		d.addRecreate(change)
	}
	if homeVolumeChange(cfg, homeVol) {
		d.addRecreate(Change{Label: changeLabelHomeVolume, Old: cfg.Volumes[mounts.VMHomeDir].Named, New: homeVol})
	}
	if flags.Mounts.Changed {
		d.addRecreate(settingChange(changeLabelBindMounts, valuesChangedNote, flags.Mounts))
	}
	if change, ok := rootDiskChange(cfg, opts); ok {
		d.addRecreate(change)
	}
	// Memory above the boot maximum cannot be applied live. A recreate applies
	// it only when a fresh VM boots with a higher maximum; otherwise the memory
	// is clamped live, since a recreate would hit the same maximum and repeat on
	// every launch.
	if flags.BootMaxMemoryGrew && memoryAboveBootMax(cfg, opts) {
		d.addRecreate(Change{Label: changeLabelMemoryAboveBootMax})
	}

	// Port publish state (serve-only) must match; a mismatch requires recreate
	// since microsandbox applies a published-port change only with a restart.
	// Network policy is creation-only too; its comparison is fingerprint-based
	// (NetworkChanged) because the SDK does not round-trip network config.
	var portsChange Change
	d.ServeHostPort, portsChange, d.PortsChanged = publishReconfig(opts, cfg)
	if d.PortsChanged {
		d.addRecreate(portsChange)
	}
	if flags.Network.Changed {
		d.addRecreate(settingChange(changeLabelNetworkPolicy, networkRuleChangedNote, flags.Network))
	}

	// Env/secret changes are folded into the rebuild tier (baked into the VM at
	// creation); agent config changes only need a daemon restart.
	if !d.Recreate {
		d.applyEnvSecretRecreate(flags)
	}
	if !d.Recreate && len(flags.AgentConfig) > 0 {
		d.RestartDaemons = true
		d.Changes = append(d.Changes, Change{Label: changeLabelAgentConfig, Details: flags.AgentConfig})
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
	if flags.Env.Changed {
		d.addRecreate(settingChange(changeLabelEnv, valuesChangedNote, flags.Env))
	}
	if flags.Secrets.Changed {
		d.addRecreate(settingChange(changeLabelSecrets, secretValuesChangedNote, flags.Secrets))
	}
}

// imageChange describes an image recreate with the VM's persisted reference and
// the one this run wants, when they differ; a recreate driven only by the image
// digest keeps the bare label, as does a side that is empty when displayed.
func imageChange(cfg *msbSdk.SandboxConfig, image naming.ImageIdentity) Change {
	want := image.Ref()
	if cfg.Image == want {
		return Change{Label: changeLabelImage}
	}
	return Change{Label: changeLabelImage, Old: cfg.Image, New: want}
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

// tmpSizeChange reports the /tmp tmpfs size change, if any. It compares only an
// explicit tmp-size: an unset spec is the memory-derived default, and comparing
// that would recreate every existing VM whose memory is below the default.
func tmpSizeChange(cfg *msbSdk.SandboxConfig, opts options.RunOptions) (Change, bool) {
	want, ok := options.ParseMemoryOK(opts.TmpSize)
	if !ok {
		return Change{}, false
	}
	// Mirror the creation-side clamp (vm.effectiveTmpSizeMiB): a tmp above the
	// live VM's boot maximum was created clamped to it, so comparing the raw
	// value would recreate the VM on every run.
	// gomutants:disable-next-line INTEGER_INCREMENT reason="MaxMemoryMiB is a VM memory in MiB and is always at least options.MinMemoryMiB, so >0 and >1 agree"
	if cfg.MaxMemoryMiB > 0 {
		want = min(want, cfg.MaxMemoryMiB)
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
	// A managed root disk grows live (vm.reconcileResourceConfig); only a
	// decrease, or a disk whose size is not recorded, needs a recreate.
	if cfg.RootDisk != nil && cfg.RootDisk.SizeMiB <= want {
		return Change{}, false
	}
	// The display form normalises back to "<size>M", so passing no raw spec is
	// identical to re-formatting the current size.
	return SizeChange("root disk size", diskMiBOr0(cfg), want, "", opts.DiskSize), true
}

// publishReconfig returns the resolved serve-only host port, the published
// ports change, and whether the desired bindings differ from the VM's.
func publishReconfig(opts options.RunOptions, cfg *msbSdk.SandboxConfig) (int, Change, bool) {
	want := desiredPublishBindings(opts, cfg)
	hostPort, _ := ports.ServeHostBinding(want)
	if ports.BindingsEqual(want, ports.BindingsFromSDK(cfg.PortBindings)) {
		return hostPort, Change{}, false
	}
	change := Change{
		Label:   changeLabelPublishedPorts,
		Details: entryDiff(describeBindings(ports.BindingsFromSDK(cfg.PortBindings)), describeBindings(want)),
	}
	return hostPort, change, true
}

// describeBindings renders port bindings as sorted "<bind>:<host> → <guest>/<protocol>" lines.
func describeBindings(bindings []msb.PortBinding) []string {
	lines := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		lines = append(lines, fmt.Sprintf("%s:%d → %d/%s",
			binding.Bind, binding.HostPort, binding.GuestPort, binding.Protocol))
	}
	sort.Strings(lines)
	return lines
}

// memoryAboveBootMax reports whether the desired memory exceeds the VM's boot
// maximum (host RAM at creation): no live modification is possible above it.
func memoryAboveBootMax(cfg *msbSdk.SandboxConfig, opts options.RunOptions) bool {
	return opts.Memory != "" && cfg.MaxMemoryMiB > 0 &&
		options.ParseMemory(opts.Memory) > cfg.MaxMemoryMiB
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
			warnAloneChange(ui, "rebuilding the project VM", changes)
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
			warnAloneChange(ui, "restarting the agent daemon", changes)
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
// project VM, composed by the single ports authority: the serve-only agent
// binding (with its resolved host port) plus the user-published mappings.
func desiredPublishBindings(opts options.RunOptions, cfg *msbSdk.SandboxConfig) []msb.PortBinding {
	return ports.PublishBindings(
		opts.ServeOnly, ports.ResolveServeHostPort(cfg, opts.ServeOnly, opts.Ports), opts.Ports,
	)
}
