package vm

import (
	"context"
	"fmt"
	"strings"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// detachedLogTail is the number of retained log bytes requested for a failed
// detached job.
const detachedLogTail = 100

// jobExited reports whether a detached job's process has finished, returning
// its exit code. A nil exit code means the process is still running or its
// result is not yet known.
func jobExited(info *msbSdk.JobInfo) (int, bool) {
	if info == nil || info.ExitCode == nil {
		return 0, false
	}
	return *info.ExitCode, true
}

// jobTailLogs returns the retained log tail of a detached job for diagnostics,
// or an empty string when the logs are unavailable.
func jobTailLogs(ctx context.Context, job msb.Job) string {
	var options msbSdk.JobLogOptions
	options.Tail = new(uint)
	*options.Tail = detachedLogTail
	entries, err := job.Logs(ctx, options)
	if err != nil {
		return ""
	}
	var builder strings.Builder
	for _, entry := range entries {
		builder.Write(entry.Data)
	}
	return builder.String()
}

// jobMarkerEnv names the environment assignment that carries a detached job's
// identity. microsandbox's jobs ABI exposes no labels — JobInfo carries the id,
// the recorded command, and the process state, nothing else — so the launcher
// labels its helpers through the command line instead: a helper is launched as
// `env <marker> <binary> <args…>` and rediscovered by the marker. Because the
// marker does not depend on the helper's arguments, a helper left behind by an
// older launcher (a different storage-driver flag, serve hostname, or log
// format) is still targeted (#343).
const jobMarkerEnv = "AGENTS_SANDBOX_DETACHED_JOB"

// jobMarkerBinary is the launcher that takes the marker assignment and then
// execs the helper, so the job's process, exit code, and logs stay the
// helper's.
const jobMarkerBinary = "env"

// detachedHelper identifies one of the helper processes the launcher runs as a
// detached job. It is the launcher's stand-in for a job label.
type detachedHelper struct {
	// marker is the helper's identity value, carried in the job's command line.
	marker string
	// binary is the helper's executable.
	binary string
}

// dockerdHelper is the identity of the dockerd job.
func dockerdHelper() detachedHelper {
	return detachedHelper{marker: "dockerd", binary: dockerdBinary}
}

// agentDaemonHelper is the identity of the agent serve daemon job; binary is
// the agent's own serve executable (opencode, opencode2, …).
func agentDaemonHelper(binary string) detachedHelper {
	return detachedHelper{marker: "agent-daemon", binary: binary}
}

// markedCommand returns the command and arguments that launch the helper under
// its marker.
func (h detachedHelper) markedCommand(args []string) (string, []string) {
	command := []string{jobMarkerEnv + "=" + h.marker, h.binary}
	return jobMarkerBinary, append(command, args...)
}

// matches reports whether a job's recorded command belongs to this helper.
//
// A marked command names the helper outright, whatever arguments it was
// launched with. A marker-less command is a helper started by a launcher
// version that predates the marker: released launchers only ever ran these two
// helpers as detached jobs, so the binary still identifies it. Its arguments
// could not — they are exactly what changes between releases, which is the
// stale-helper race this closes.
func (h detachedHelper) matches(command []string) bool {
	if len(command) >= 2 && command[0] == jobMarkerBinary {
		return command[1] == jobMarkerEnv+"="+h.marker
	}
	return len(command) > 0 && command[0] == h.binary
}

// killJobs kills every detached job belonging to the helper, so a stale helper
// never keeps the socket, port, or pid file a fresh launch needs. Failures are
// reported as verbose diagnostics and never fatal: the readiness probe that
// follows decides.
func killJobs(ctx context.Context, sb msb.Sandbox, helper detachedHelper, ui termio.UI) {
	infos, err := sb.ListJobs(ctx, msbSdk.WithAllJobs())
	if err != nil {
		ui.Verbosef("listing jobs to stop %s failed: %v", helper.marker, err)
		return
	}
	for _, info := range infos {
		if !helper.matches(info.Command) {
			continue
		}
		job, getErr := sb.GetJob(ctx, info.ID)
		if getErr != nil {
			ui.Verbosef("resolving job %s failed: %v", info.ID, getErr)
			continue
		}
		// Kill terminates the process; Close (handle-only) releases the native
		// handle regardless of whether the kill succeeded.
		killErr := job.Kill(ctx)
		if closeErr := job.Close(); closeErr != nil {
			ui.Verbosef("closing job %s handle failed: %v", info.ID, closeErr)
		}
		if killErr != nil {
			ui.Verbosef("killing job %s failed: %v", info.ID, killErr)
			continue
		}
		ui.Verbosef("killed stale job %s", info.ID)
	}
}

// awaitJobReady polls probe until it reports healthy. It fails fast when the
// job's process exits (a structured failure) and times out with the probe's
// last diagnostic detail and the job's log tail when probe never succeeds.
// onUnhealthy, when non-nil, is called with each unhealthy probe's detail for
// progress logging.
func awaitJobReady(
	ctx context.Context,
	job msb.Job,
	label string,
	probe func(context.Context) (healthy bool, detail string),
	onUnhealthy func(detail string),
	timeout, pollInterval time.Duration,
) error {
	deadline := time.Now().Add(timeout)
	var lastDetail string
	for {
		if code, exited := jobExited(inspectJob(ctx, job)); exited {
			return fmt.Errorf(
				"%s exited before becoming ready (exit %d):\n%s",
				label,
				code,
				jobTailLogs(ctx, job),
			)
		}
		healthy, detail := probe(ctx)
		if healthy {
			return nil
		}
		lastDetail = detail
		if onUnhealthy != nil {
			onUnhealthy(detail)
		}
		if !time.Now().Before(deadline) {
			message := fmt.Sprintf("%s did not become ready within %s", label, timeout)
			if lastDetail != "" {
				message += ": " + lastDetail
			}
			return fmt.Errorf("%s:\n%s", message, jobTailLogs(ctx, job))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// inspectJob reads a job's info, returning nil when inspection fails so the
// readiness loop keeps polling rather than treating a transient error as death.
func inspectJob(ctx context.Context, job msb.Job) *msbSdk.JobInfo {
	info, err := job.Inspect(ctx)
	if err != nil {
		return nil
	}
	return info
}

// probeHealthy runs a health command inside the sandbox as the default user and
// reports whether it succeeded. When it fails it returns a diagnostic detail
// (the shell error, exit code, or output) so a readiness timeout is
// diagnosable instead of opaque.
func probeHealthy(ctx context.Context, sb msb.Sandbox, command string) (bool, string) {
	out, err := sb.Shell(ctx, command, msbSdk.WithExecUser(DefaultSandboxUser))
	if err != nil {
		return false, fmt.Sprintf("probe failed: %v", err)
	}
	if out == nil {
		return false, "probe returned no result"
	}
	if !out.Success() {
		body := strings.TrimSpace(out.Stderr())
		if body == "" {
			body = strings.TrimSpace(out.Stdout())
		}
		if body == "" {
			body = "(no output)"
		}
		return false, fmt.Sprintf("probe exit %d: %s", out.ExitCode(), body)
	}
	return true, ""
}
