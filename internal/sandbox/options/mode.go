package options

// Mode is how far a run should go, separate from RunOptions (which describes the
// sandbox to build): at the boundary, whether to bring up the sandbox at all.
type Mode uint8

const (
	// ModeRun is a full run: build, create/attach, attach the agent.
	ModeRun Mode = iota
	// ModeSkipVM (--dry-run-vm) prepares everything and skips the VM lifecycle.
	ModeSkipVM
	// ModePreview (--dry-run) is a read-only preview that makes no changes.
	ModePreview
)

// SkipsVM reports whether the VM lifecycle is skipped (prepare-only or preview).
func (m Mode) SkipsVM() bool { return m != ModeRun }

// Previews reports whether the run makes no changes at all.
func (m Mode) Previews() bool { return m == ModePreview }
