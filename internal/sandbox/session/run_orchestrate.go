package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/git"
	"github.com/inoio/opencode-sandbox/internal/sandbox/doctor"
	"github.com/inoio/opencode-sandbox/internal/sandbox/image"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/sandbox/volume"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

func buildAttachCommand(target string, args []string) string {
	parts := []string{"opencode", "attach", "http://127.0.0.1:4096", "--dir", target}
	parts = append(parts, args...)

	return strings.Join(parts, " ")
}

// serveOnlyMessage builds the message printed when serving opencode for
// external clients such as Opencode Desktop.
func serveOnlyMessage(host, port string) string {
	return fmt.Sprintf("Connect Opencode Desktop to: http://%s:%s\n\n"+
		"Optional: set OPENCODE_SERVER_PASSWORD (and OPENCODE_SERVER_USERNAME) to protect the server with basic auth.\n"+
		"Press Ctrl-D to stop serving.", host, port)
}

// runServeOnly keeps the VM alive and blocks until ctx is done (CTRL-D or
// SIGINT), without attaching an in-VM TUI. It holds the VM via a keeper exec so
// the msb idle timeout does not stop it while serving.
func runServeOnly(ctx context.Context, sb msb.Sandbox, ui termio.UI) error {
	host := options.ServeOnlyBindAddr
	port := options.ServeOnlyPort
	ui.Infof("%s", serveOnlyMessage(host, port))
	keeperCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	keeperDone := keepVMAlive(keeperCtx, sb)
	defer func() { _ = keeperDone() }()
	<-ctx.Done()
	return ctx.Err()
}

type sandboxSession struct {
	sb     msb.Sandbox
	name   string
	target string
	cwd    string
}

func (s *sandboxSession) cleanup() {
	if s.sb != nil {
		_ = s.sb.Detach(context.Background())
	}
	// Run git worktree prune on the host repo to clean up stale entries.
	/*if s.cwd != "" {
		_ = git.PruneWorktrees(context.Background(), s.cwd)
	}*/
}

func prepareSandbox(
	ctx context.Context,
	opts options.RunOptions,
	ui termio.UI,
) (*sandboxSession, error) {
	projectSlug := git.ProjectSlug(ui)

	// Without an explicit pin, reuse the version already baked into the runner
	// image instead of re-resolving "latest" from the network on every run.
	// Re-resolving would change the version build arg (and thus the image
	// identity), causing sporadic rebuilds and fresh loads into microsandbox.
	openCodeVersion := opts.OpenCodeVersion
	if openCodeVersion == "" {
		openCodeVersion = currentUpgradeVersion()
	}

	imageInfo, err := image.EnsureImage(
		ctx,
		projectSlug,
		image.BuildOptions{Force: opts.Rebuild, OpenCodeVersion: openCodeVersion},
		ui,
	)
	if err != nil {
		return nil, fmt.Errorf("image setup failed: %w", err)
	}
	ui.Verbosef("Using image '%s' (digest=%s, opencode %s)", imageInfo.Tag, imageInfo.Digest, imageInfo.OpenCodeVersion)

	imageInfo, action, err := maybePromptOpenCodeUpgrade(ctx, ui, opts, imageInfo)
	if err != nil {
		return nil, err
	}
	if action == upgradeActionRebuild {
		ui.Verbosef("runner image rebuilt with a newer opencode version")
	}

	// Persist the version actually baked so later runs reuse it as a stable
	// build arg. Recording after the upgrade prompt captures any rebuild.
	if recordErr := recordUpgradeVersion(imageInfo.OpenCodeVersion); recordErr != nil {
		ui.Warnf("could not record opencode version in updater state: %v (continuing)", recordErr)
	}

	vm := volume.NewManager(ui)
	client := msb.Get()
	homeVol, vs, err := vm.ResolveHomeVolume(ctx, client, projectSlug, imageInfo.Digest, imageInfo.Tag, opts, ui)
	if err != nil {
		return nil, fmt.Errorf("volume setup failed: %w", err)
	}
	ui.Verbosef("home volume: %s", homeVol)

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get current directory: %w", err)
	}

	// Load the merged opencode config and home files exactly once per startup;
	// the result is shared by the reconfig decision and the provisioning step.
	cfs, err := reprovision.LoadConfigFiles(configpaths.Get().UserOpencodeConfigDir(), ui)
	if err != nil {
		return nil, err
	}

	recreate, restart, homeVol, err := decideReconfig(
		ctx,
		client,
		vm,
		opts,

		imageInfo.Tag,
		imageInfo.Digest,
		homeVol,
		vs,
		cfs,
		ui,
	)
	if err != nil {
		return nil, err
	}
	ui.Verbosef("recreate: %v, restart: %v", recreate, restart)
	opts.Recreate = recreate
	sb, created, err := ensureProjectVM(ctx, opts, imageInfo.Tag, homeVol, cwd, imageInfo.Env, ui)
	if err != nil {
		return nil, err
	}
	if created {
		persistCreatedEnvSecrets(projectSlug, ui)
	}
	name := projectVMName(projectSlug)

	var sandboxTarget string
	var sandboxErr error
	if sb == nil {
		ui.Infof("VM lifecycle skipped (--dry-run-vm)")
		sandboxTarget = resolveTargetNoBranch()
	} else {
		sandboxTarget, sandboxErr = setUpSandbox(ctx, sb, opts, cfs, ui, restart)
		if sandboxErr != nil {
			return nil, sandboxErr
		}
	}

	ui.Verbosef("attach target: %s", sandboxTarget)

	return &sandboxSession{
		sb:     sb,
		name:   name,
		target: sandboxTarget,
		cwd:    cwd,
	}, nil
}

// persistCreatedEnvSecrets records the desired env/secret fingerprints when a
// project VM is freshly created, so subsequent runs can detect changes.
func persistCreatedEnvSecrets(projectSlug string, ui termio.UI) {
	desiredEnv := reprovision.MergeEnvMaps(
		reprovision.BuildEnvMap(configpaths.Get().UserEnvFile()),
		reprovision.BuildEnvMap(configpaths.Get().ProjectEnvFile()),
	)
	desiredSecrets := reprovision.BuildSecretsFromSpecs(reprovision.MergeSecretSpecs(
		reprovision.ParseSecretSpecLegacy(configpaths.Get().UserEnvSecretFile(), ui),
		reprovision.ParseSecretSpecLegacy(configpaths.Get().ProjectEnvSecretFile(), ui),
		reprovision.ParseSecretSpecYAML(configpaths.Get().UserEnvSecretYAMLFile(), ui),
		reprovision.ParseSecretSpecYAML(configpaths.Get().ProjectEnvSecretYAMLFile(), ui),
	), ui)
	if err := persistEnvSecrets(
		projectSlug,
		reprovision.BuildEnvState(desiredEnv),
		reprovision.BuildSecretState(desiredSecrets),
	); err != nil {
		ui.Warnf("persisting env/secret fingerprints on VM creation: %v (continuing)", err)
	}
}

// Run creates (or reuses) the project VM, provisions config, starts opencode
// serve, and attaches a TUI client.
//
// Note: Run is called from cli.go after all flags are resolved.
func Run(ctx context.Context, opts options.RunOptions, ui termio.UI) error {
	session, err := prepareSandbox(ctx, opts, ui)
	if err != nil {
		return err
	}
	defer session.cleanup()

	if opts.DryRun {
		ui.Infof("dry-run: Would run opencode")
		return nil
	}
	if opts.DryRunVM && session.sb == nil {
		ui.Infof("dry-run: Would start opencode in VM")
		return nil
	}

	projectSlug := git.ProjectSlug(ui)

	if opts.ServeOnly { //nolint:nestif // lease acquire/serve/release/reap sequence requires this structure
		release, acquireErr := state.AcquireClientLease(projectSlug)
		if acquireErr != nil {
			ui.Warnf("client lease failed: %v", acquireErr)
		}
		defer func() {
			if acquireErr == nil && release != nil {
				release()
			}
		}()
		if err := runServeOnly(ctx, session.sb, ui); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		if acquireErr == nil {
			release()
			release = nil
		}
		if err := reapOnLastClient(ctx, projectSlug, session.sb, opts.ReapPolicy, ui); err != nil {
			ui.Warnf("reap failed: %v", err)
		}
		return &ExitError{Code: 0}
	}

	setup := buildAttachCommand(session.target, opts.Args)
	ui.Verbosef("%s", setup)
	// Run as a login shell so /etc/profile and ~/.profile are sourced,
	// putting tools installed under /usr/local/go/bin, ~/go/bin and
	// ~/.microsandbox/bin on PATH for opencode and its child shells.
	return runAttach(ctx, session, projectSlug, ui, opts, "-l", "-c", setup)
}

// Shell creates (or reuses) the project VM and drops the user into an
// interactive shell session, without starting opencode serve.
func Shell(ctx context.Context, opts options.RunOptions, ui termio.UI) error {
	session, err := prepareSandbox(ctx, opts, ui)
	if err != nil {
		return err
	}
	defer session.cleanup()

	if opts.DryRun {
		ui.Infof("dry-run: Would start interactive shell session")
		return nil
	}
	if opts.DryRunVM && session.sb == nil {
		ui.Infof("dry-run: Would start interactive shell session")
		return nil
	}

	projectSlug := git.ProjectSlug(ui)
	return runAttach(ctx, session, projectSlug, ui, opts, "-l")
}

// BuildImage builds (or updates) the runner image for Docker-in-Docker support.
func BuildImage(ctx context.Context, force, dryRun bool, openCodeVersion string, ui termio.UI) error {
	if dryRun {
		ui.Infof("dry-run: Would build runner image")
		return nil
	}

	if err := doctor.CheckDocker(ctx); err != nil {
		return fmt.Errorf("docker not available: %w", err)
	}
	projectSlug := git.ProjectSlug(ui)

	info, err := image.EnsureImageWithClient(ctx, image.ResolveDockerfile(), projectSlug,
		image.BuildOptions{Force: force, OpenCodeVersion: openCodeVersion}, ui)
	if err != nil {
		return err
	}
	return image.EnsureLoaded(ctx, msb.Get(), projectSlug, info.Tag, ui)
}

func finalizeRun(attachErr error, exitCode int) error {
	if attachErr != nil {
		return fmt.Errorf("opencode session failed: %w", attachErr)
	}
	if exitCode == 0 {
		return nil
	}
	return &ExitError{Code: exitCode}
}

// runAttach performs the shared lease-acquire, attach, explicit release,
// reap-on-last-client, and finalize sequence for Run and Shell.
func runAttach(
	ctx context.Context,
	session *sandboxSession,
	projectSlug string,
	ui termio.UI,
	opts options.RunOptions,
	bashArgs ...string,
) error {
	// Acquire a client lease so state tracks this session.
	release, acquireErr := state.AcquireClientLease(projectSlug)
	if acquireErr != nil {
		ui.Warnf("client lease failed: %v", acquireErr)
	}
	defer func() {
		if acquireErr == nil && release != nil {
			release()
		}
	}()

	// Attach to the sandbox and capture its exit code.
	var exitCode int
	var attachErr error
	if opts.Root {
		exitCode, attachErr = session.sb.AttachWith(ctx, "/bin/bash", bashArgs, msbSdk.WithAttachUser("root"))
	} else {
		exitCode, attachErr = session.sb.Attach(ctx, "/bin/bash", bashArgs...)
	}

	// Explicitly release the lease after attach returns, before reaping.
	// This ensures state.CountActiveClients reflects only OTHER live clients.
	// The deferred release above is a safety net.
	if acquireErr == nil {
		release()
		release = nil
	}

	if err := reapOnLastClient(ctx, projectSlug, session.sb, opts.ReapPolicy, ui); err != nil {
		ui.Warnf("reap failed: %v", err)
	}

	return finalizeRun(attachErr, exitCode)
}
