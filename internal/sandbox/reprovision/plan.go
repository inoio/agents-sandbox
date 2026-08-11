package reprovision

import (
	"context"
	"errors"
	"strconv"
	"strings"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/options"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// Change describes one changed setting for prompt display. Values are
// shown for simple sizes/counts; env/secrets/config carry labels only (Old/New empty).
type Change struct {
	Label string
	Old   string
	New   string
}

// ParseSizeSpec parses a memory spec ("" means runtime default -> not parsed).
func ParseSizeSpec(spec string) (uint32, bool) {
	if spec == "" {
		return 0, false
	}
	spec = strings.TrimSpace(spec)
	multiplier := uint32(1)
	last := spec[len(spec)-1]
	rest := spec
	switch last {
	case 'g', 'G':
		multiplier = 1024
		rest = spec[:len(spec)-1]
	case 'm', 'M':
		multiplier = 1
		rest = spec[:len(spec)-1]
	}
	n, err := strconv.Atoi(rest)
	if err != nil {
		return 0, false
	}
	return uint32(n) * multiplier, true //nolint:gosec // G115: bounded spec size
}

// FormatSizeSpec returns the raw user spec verbatim when it matches valueMiB,
// else the normalized "<valueMiB>M" form.
func FormatSizeSpec(valueMiB uint32, raw string) string {
	if v, ok := ParseSizeSpec(raw); ok && v == valueMiB {
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
type Plan struct {
	Recreate       bool
	RestartDaemons bool
	Resources      *msbSdk.ModifyOptions
	Changes        []Change
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

// PlanReconfig computes the reconfiguration plan given the current VM config
// and the desired state. Returns a nil Plan when there is no existing config
// (first creation) or nothing needs to change.
func PlanReconfig( //nolint:gocognit // core planner, cognitive complexity acceptable for now
	cfg *msbSdk.SandboxConfig,
	imageRef string,
	opts options.RunOptions,
	envChanged, secretsChanged, opencodeConfigChanged bool,
) *Plan {
	d := &Plan{} //nolint:exhaustruct // fields zeroed intentionally
	if cfg == nil {
		return d
	}

	// Recreation triggers (cannot be changed live).
	if imageRef != "" && cfg.Image != "" && cfg.Image != imageRef {
		d.Recreate = true
		d.Changes = append(
			d.Changes,
			Change{Label: "image"}, //nolint:exhaustruct // label-only for change reporting
		)
	}
	if wantTmp, ok := ParseSizeSpec(opts.TmpSize); ok {
		if tmp, ok := cfg.Volumes[tmpMountPath]; ok && tmp.SizeMiB != wantTmp {
			d.Recreate = true
			oldRaw := FormatSizeSpec(tmp.SizeMiB, "")
			d.Changes = append(d.Changes, SizeChange("/tmp tmpfs size", tmp.SizeMiB, wantTmp, oldRaw, opts.TmpSize))
		}
	}
	if wantDisk, ok := ParseSizeSpec(opts.DiskSize); ok {
		if cfg.RootDisk == nil || cfg.RootDisk.SizeMiB != wantDisk {
			d.Recreate = true
			oldRaw := ""
			if cfg.RootDisk != nil {
				oldRaw = FormatSizeSpec(cfg.RootDisk.SizeMiB, "")
			}
			d.Changes = append(
				d.Changes,
				SizeChange("root disk size", diskMiBOr0(cfg), wantDisk, oldRaw, opts.DiskSize),
			)
		}
	}

	// Env/secret changes cannot be applied live or on a daemon restart:
	// microsandbox requires a VM (re)start for them, so they are folded into the
	// rebuild tier (they are baked into the VM at creation, see createProjectVM).
	if !d.Recreate && (envChanged || secretsChanged) {
		d.Recreate = true
		if envChanged {
			d.Changes = append(
				d.Changes,
				Change{Label: "environment variables"}, //nolint:exhaustruct // label-only for change reporting
			)
		}
		if secretsChanged {
			d.Changes = append(
				d.Changes,
				Change{Label: "secrets"}, //nolint:exhaustruct // label-only for change reporting
			)
		}
	}

	// opencode config changes are picked up by restarting the opencode daemon;
	// a full VM rebuild is not required.
	if !d.Recreate && opencodeConfigChanged {
		d.RestartDaemons = true
		d.Changes = append(
			d.Changes,
			Change{Label: "opencode config"}, //nolint:exhaustruct // label-only for change reporting
		)
	}

	// cpu/memory always staged for live Modify (clamped to boot max).
	var mo msbSdk.ModifyOptions
	if opts.CPUs != 0 && cfg.MaxCPUs > 0 {
		want := min(opts.CPUs, cfg.MaxCPUs)
		if want != cfg.CPUs {
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
	if mo.CPUs != 0 || mo.MemoryMiB != 0 {
		mo.Policy = msbSdk.ModificationPolicyNoRestart
		d.Resources = &mo
	}
	return d
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
