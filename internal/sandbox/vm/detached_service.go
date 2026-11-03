package vm

import (
	"context"
	"fmt"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// detachedService is the ingredient table for ensureDetachedService: the helper
// identity (kill rediscovery and the launch marker), the health probe, how the
// fresh launch is prepared, and the verbose notes each service words
// differently. A call site fills only the fields its service needs.
//
//exhaustruct:ignore
type detachedService struct {
	// helper identifies the service, for killing a stale instance and for the
	// marked launch command.
	helper detachedHelper

	// probe reports the service's health; it drives both the idempotency
	// pre-check and every readiness poll.
	probe func(context.Context) (bool, string)

	// onUnhealthy logs an unhealthy readiness poll; it may be nil.
	onUnhealthy func(detail string)

	// label names the service in a readiness failure.
	label string

	// launchArgs builds the fresh launch's arguments and the starting note. It
	// runs only after the pre-check has decided a restart is due, so a healthy
	// service pays for no launch preparation (dockerd's storage-driver probe).
	launchArgs func(context.Context) (args []string, startingNote string, err error)

	// startLabel names the service in a launch failure ("start <label>: …").
	startLabel string

	// execUser runs the detached job as that user; empty uses the sandbox
	// default (the daemon runs as the dev user, dockerd as root).
	execUser string

	// timeout and pollInterval bound the readiness await.
	timeout      time.Duration
	pollInterval time.Duration

	// skipPreCheck forces a restart even when the pre-check reports healthy
	// (the daemon's force path).
	skipPreCheck bool

	// cleanup, when non-nil, runs after a stale instance is killed and before
	// the launch (dockerd's containerd/pid/socket residue); its failure is
	// logged with cleanupNote and never fatal.
	cleanup func(context.Context) error

	// alreadyHealthyNote is logged when the pre-check reports healthy and the
	// service is left running.
	alreadyHealthyNote string
	// preCheckUnhealthyNote, when non-empty, is a printf template logged with
	// the pre-check's detail.
	preCheckUnhealthyNote string
	// readyNote is logged once the service reports ready.
	readyNote string
	// closeNote is a printf template logged with the error when releasing the
	// job handle fails.
	closeNote string
	// cleanupNote is a printf template logged with the error when cleanup fails.
	cleanupNote string
}

// ensureDetachedService guarantees a detached helper service is healthy in the
// VM. It probes the service and leaves it running when healthy (unless the
// caller forces a restart); otherwise it kills a stale instance, optionally
// clears its residue, launches a fresh one as a runtime-owned detached job, and
// waits until it reports ready. It is the one home of the launch/readiness
// recipe: ensureDaemon and startDockerdIfPresent supply only their ingredients.
func ensureDetachedService(
	ctx context.Context,
	sb msb.Sandbox,
	ui termio.Logger,
	service detachedService,
) error {
	if !service.skipPreCheck {
		healthy, detail := service.probe(ctx)
		if healthy {
			ui.Verbose(service.alreadyHealthyNote)
			return nil
		}
		if detail != "" && service.preCheckUnhealthyNote != "" {
			ui.Verbosef(service.preCheckUnhealthyNote, detail)
		}
	}

	args, startingNote, err := service.launchArgs(ctx)
	if err != nil {
		return err
	}
	ui.Verbose(startingNote)

	killJobs(ctx, sb, service.helper, ui)
	if service.cleanup != nil {
		if cleanupErr := service.cleanup(ctx); cleanupErr != nil {
			ui.Verbosef(service.cleanupNote, cleanupErr)
		}
	}

	binary, commandArgs := service.helper.markedCommand(args)
	job, startErr := sb.ExecDetached(ctx, binary, commandArgs, execUserOptions(service.execUser)...)
	if startErr != nil {
		return fmt.Errorf("start %s: %w", service.startLabel, startErr)
	}
	defer func() {
		if closeErr := job.Close(); closeErr != nil {
			ui.Verbosef(service.closeNote, closeErr)
		}
	}()

	if readyErr := awaitJobReady(
		ctx,
		job,
		service.label,
		service.probe,
		service.onUnhealthy,
		service.timeout,
		service.pollInterval,
	); readyErr != nil {
		return readyErr
	}
	ui.Verbose(service.readyNote)
	return nil
}

// execUserOptions returns the exec-user option for a detached launch, or nothing
// when user is empty so the sandbox default applies.
func execUserOptions(user string) []msbSdk.JobExecOption {
	if user == "" {
		return nil
	}
	return []msbSdk.JobExecOption{msbSdk.WithExecUser(user)}
}
