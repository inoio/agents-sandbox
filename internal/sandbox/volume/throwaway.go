package volume

import (
	"context"
	"fmt"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// throwawaySandbox describes a short-lived task sandbox that mounts home
// volumes for a single operation (prefill, copy, edit).
type throwawaySandbox struct {
	// purpose names the operation in error and warning messages.
	purpose string
	// mounts maps guest paths to the volumes mounted there.
	mounts map[string]msbSdk.MountConfig
	// spinnerLabel, when set, starts a spinner before the sandbox is created
	// and hands it to the payload, which must stop it.
	spinnerLabel string
}

// withThrowawaySandbox loads the runner image, creates a task sandbox for the
// given key with the spec's mounts, runs payload against it, and always
// destroys and closes the sandbox afterwards.
//
// Destroy goes through the live sandbox handle, so a same-name replacement is
// never removed by mistake; replaced or already-gone sandboxes are not
// reported.
func withThrowawaySandbox(
	ctx context.Context,
	client msb.Client,
	k state.Key,
	info image.ImageInfo,
	spec throwawaySandbox,
	ui termio.UI,
	payload func(sb msb.Sandbox, spin termio.Spinner) error,
) error {
	info, err := image.EnsureLoaded(ctx, client, info, ui)
	if err != nil {
		return fmt.Errorf("load runner image: %w", err)
	}
	var spin termio.Spinner
	if spec.spinnerLabel != "" {
		spin = ui.Spinner(spec.spinnerLabel)
	}
	sandboxName := naming.TaskSandboxName(k.Slug, k.Agent, time.Now().UnixNano())
	sb, err := client.CreateSandbox(ctx, sandboxName,
		msbSdk.WithImage(info.Ref),
		msbSdk.WithLabels(naming.TaskSandboxLabels(k.Slug, k.Agent)),
		msbSdk.WithMounts(spec.mounts),
		msbSdk.WithReplace(),
	)
	if err != nil {
		if spin != nil {
			spin.StopError(err)
		}
		return fmt.Errorf("create %s sandbox: %w", spec.purpose, err)
	}
	defer destroyThrowawaySandbox(sb, sandboxName, spec.purpose, ui)
	return payload(sb, spin)
}

func destroyThrowawaySandbox(sb msb.Sandbox, sandboxName, purpose string, ui termio.UI) {
	destroyCtx, cancel := context.WithTimeout(context.Background(), options.SandboxStopTimeout)
	defer cancel()
	if err := sb.Destroy(destroyCtx); err != nil && !msb.IsReplaced(err) && !msb.IsNotFound(err) {
		ui.Warnf("failed to destroy %s sandbox %s: %v", purpose, sandboxName, err)
	}
	_ = sb.Close()
}

// shellStep is a shell script run in a throwaway sandbox, with the prefixes
// that label its two failure modes.
type shellStep struct {
	execErrPrefix string // the command could not be run
	failurePrefix string // the command ran and exited non-zero
	script        string
}

// runShell runs the step's script via `sh -c` and stops spin with the outcome.
func runShell(ctx context.Context, sb msb.Sandbox, spin termio.Spinner, step shellStep) error {
	out, err := sb.Exec(ctx, "sh", []string{"-c", step.script})
	if err != nil {
		spin.StopError(err)
		return fmt.Errorf("%s: %w", step.execErrPrefix, err)
	}
	if !out.Success() {
		err := fmt.Errorf("%s (exit %d): %s", step.failurePrefix, out.ExitCode(), out.Stderr())
		spin.StopError(err)
		return err
	}
	spin.Stop()
	return nil
}
