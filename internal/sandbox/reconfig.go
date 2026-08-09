package sandbox

import msbSdk "github.com/superradcompany/microsandbox/sdk/go"

type reconfigDecision struct {
	recreate       bool
	restartDaemons bool
	restartDockerd bool
	resources      *msbSdk.ModifyOptions
	changes        []reconfigChange
}

func planReconfig( //nolint:gocognit,gocyclo,cyclop // core planner, complexity acceptable for now
	cfg *msbSdk.SandboxConfig,
	imageRef string,
	opts RunOptions,
	envChanged, secretsChanged, opencodeConfigChanged bool, //nolint:unparam // precomputed by resolver in production
) *reconfigDecision {
	d := &reconfigDecision{} //nolint:exhaustruct // fields zeroed intentionally
	if cfg == nil && imageRef == "" {
		return d
	}
	if cfg == nil {
		return d
	}

	// Recreation triggers (cannot be changed live).
	if imageRef != "" && cfg.Image != "" && cfg.Image != imageRef {
		d.recreate = true
		d.changes = append(
			d.changes,
			reconfigChange{label: "image"}, //nolint:exhaustruct // label-only for change reporting
		)
	}
	if wantTmp, ok := parseSizeSpec(opts.TmpSize); ok {
		if tmp, ok := cfg.Volumes["/tmp"]; ok && tmp.SizeMiB != wantTmp {
			d.recreate = true
			oldRaw := formatSizeSpec(tmp.SizeMiB, "")
			d.changes = append(d.changes, sizeChange("/tmp tmpfs size", tmp.SizeMiB, wantTmp, oldRaw, opts.TmpSize))
		}
	}
	if wantDisk, ok := parseSizeSpec(opts.DiskSize); ok {
		if cfg.RootDisk == nil || cfg.RootDisk.SizeMiB != wantDisk {
			d.recreate = true
			oldRaw := ""
			if cfg.RootDisk != nil {
				oldRaw = formatSizeSpec(cfg.RootDisk.SizeMiB, "")
			}
			d.changes = append(
				d.changes,
				sizeChange("root disk size", diskMiBOr0(cfg), wantDisk, oldRaw, opts.DiskSize),
			)
		}
	}

	// reuse-only daemon restart triggers (only when not recreating).
	if !d.recreate && (envChanged || secretsChanged || opencodeConfigChanged) {
		d.restartDaemons = true
		d.restartDockerd = envChanged || secretsChanged
		if envChanged {
			d.changes = append(
				d.changes,
				reconfigChange{label: "environment variables"}, //nolint:exhaustruct // label-only for change reporting
			)
		}
		if secretsChanged {
			d.changes = append(
				d.changes,
				reconfigChange{label: "secrets"}, //nolint:exhaustruct // label-only for change reporting
			)
		}
		if opencodeConfigChanged {
			d.changes = append(
				d.changes,
				reconfigChange{label: "opencode config"}, //nolint:exhaustruct // label-only for change reporting
			)
		}
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
		want := parseMemory(opts.Memory)
		if cfg.MaxMemoryMiB > 0 && want > cfg.MaxMemoryMiB {
			want = cfg.MaxMemoryMiB
		}
		if want != cfg.MemoryMiB {
			mo.MemoryMiB = want
		}
	}
	if mo.CPUs != 0 || mo.MemoryMiB != 0 {
		mo.Policy = msbSdk.ModificationPolicyNoRestart
		d.resources = &mo
	}
	return d
}

func diskMiBOr0(cfg *msbSdk.SandboxConfig) uint32 {
	if cfg.RootDisk == nil {
		return 0
	}
	return cfg.RootDisk.SizeMiB
}
