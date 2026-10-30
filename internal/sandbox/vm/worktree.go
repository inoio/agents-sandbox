package vm

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

const defaultTargetDir = mounts.WorkspaceMountPath

// validateWorktreeBase confirms the base ref resolves among existing local refs
// inside the VM. It never fetches: the ref must already be present locally.
func validateWorktreeBase(ctx context.Context, sb msb.Sandbox, dir, base string) error {
	out, err := sb.Exec(ctx, "git", []string{"-C", dir, "rev-parse", "--verify", base + "^{commit}"})
	if err != nil {
		return fmt.Errorf("check base %q: %w", base, err)
	}
	if !out.Success() {
		return fmt.Errorf("base %q does not resolve locally (no fetch is performed)", base)
	}
	return nil
}

// findWorktreeDir returns the first directory whose base name matches slug.
func findWorktreeDir(dirs []string, slug string) (string, bool) {
	for _, dir := range dirs {
		if filepath.Base(dir) == slug {
			return dir, true
		}
	}
	return "", false
}

// resolveWorktreeProject resolves the launch directory to the daemon project the
// worktree routes are scoped to. An agent whose routes are global has no resolve
// command and returns the zero project without shelling out.
func resolveWorktreeProject(
	ctx context.Context, sb msb.Sandbox, provider agent.WorktreeProvider, target string,
) (agent.WorktreeProject, error) {
	command := provider.WorktreeResolveCmd(target)
	if command == "" {
		return agent.WorktreeProject{}, nil
	}
	out, err := sb.Shell(ctx, command)
	if err != nil {
		return agent.WorktreeProject{}, fmt.Errorf("resolve project for %q: %w", target, err)
	}
	if !out.Success() {
		return agent.WorktreeProject{}, fmt.Errorf(
			"resolve project for %q failed (exit %d): %s", target, out.ExitCode(), out.Stderr(),
		)
	}
	project, ok := provider.WorktreeParseProject(out.Stdout())
	if !ok {
		return agent.WorktreeProject{}, fmt.Errorf("resolve project for %q returned an unexpected payload", target)
	}
	return project, nil
}

// worktreeDetachedNote is the parenthetical appended to the create message for
// agents whose daemon leaves a new worktree on a detached HEAD (v2), naming the
// recovery; it is empty for agents that keep a branch (v1). It is deliberately
// not used on reuse: a reused worktree's checkout state is unknown, so claiming
// one there would be wrong as soon as the user runs the suggested `git switch`.
func worktreeDetachedNote(a agent.Agent) string {
	reporter, ok := agent.AsDetachedWorktreeReporter(a)
	if !ok || !reporter.WorktreeCreatesDetachedHead() {
		return ""
	}
	return " (detached HEAD; run `git switch -c <branch>` to keep your commits)"
}

// ResolveTarget returns the --dir target for attach. An empty spec → defaultDir.
// With a name → reuse an existing worktree via the provider's HTTP API when one
// already exists for that name, otherwise create a new one, and return its
// directory path. The daemon project is resolved first and threaded through the
// list and create calls. When a base is present on an existing (reused) worktree
// the base is silently ignored with a warning. When a base is present on a fresh
// create the base is validated against the launch directory (no fetch) before
// the create, so an unresolvable base aborts without creating a worktree. An
// agent without a worktree provider falls back to defaultDir. defaultDir is the
// launch directory (the guest CWD, e.g. `/workspace/<sub>`).
func ResolveTarget(
	ctx context.Context,
	a agent.Agent,
	sb msb.Sandbox,
	spec options.WorktreeSpec,
	defaultDir string,
	ui termio.UI,
) (string, error) {
	provider, hasWorktree := agent.AsWorktreeProvider(a)
	if !hasWorktree {
		return defaultDir, nil
	}

	if spec.Name == "" {
		return defaultDir, nil
	}

	project, err := resolveWorktreeProject(ctx, sb, provider, defaultDir)
	if err != nil {
		return "", fmt.Errorf("worktree %q: %w", spec.Name, err)
	}

	ui.Verbosef("checking for an existing worktree %q", spec.Name)
	listOut, err := sb.Shell(ctx, provider.WorktreeListCmd(project))
	if err != nil {
		return "", fmt.Errorf("list worktrees for %q: %w", spec.Name, err)
	}
	// A failed list must abort rather than fall through to create: silently
	// treating it as "no worktrees" would abandon an existing worktree for a new
	// server-suffixed one and hide a daemon route or field rename.
	if !listOut.Success() {
		return "", fmt.Errorf(
			"list worktrees for %q failed (exit %d): %s",
			spec.Name,
			listOut.ExitCode(),
			listOut.Stderr(),
		)
	}
	dirs, listOK := provider.WorktreeParseList(listOut.Stdout(), project)
	if !listOK {
		return "", fmt.Errorf("list worktrees for %q returned an unexpected payload", spec.Name)
	}
	if dir, ok := findWorktreeDir(dirs, spec.Name); ok {
		if spec.Base != "" {
			ui.Warnf("worktree %q already exists; ignoring base %q", spec.Name, spec.Base)
		}
		ui.Infof("reusing existing worktree %q: %s", spec.Name, dir)
		return dir, nil
	}

	if spec.Base != "" {
		if err = validateWorktreeBase(ctx, sb, defaultDir, spec.Base); err != nil {
			return "", fmt.Errorf("worktree %q: %w", spec.Name, err)
		}
	}

	ui.Verbosef("creating worktree %q", spec.Name)
	createSpec := agent.WorktreeSpec{Name: spec.Name, Base: spec.Base}
	out, err := sb.Shell(ctx, provider.WorktreeCreateCmd(createSpec, project))
	if err != nil {
		return "", fmt.Errorf("create worktree %q: %w", spec.Name, err)
	}
	if !out.Success() {
		return "", fmt.Errorf(
			"create worktree %q failed (exit %d): %s (a timed-out request may still have created it)",
			spec.Name,
			out.ExitCode(),
			out.Stderr(),
		)
	}

	dir, ok := provider.WorktreeParseDir(out.Stdout())
	if !ok {
		return "", fmt.Errorf("parse worktree response for %q", spec.Name)
	}

	ui.Infof("created worktree %q: %s%s", spec.Name, dir, worktreeDetachedNote(a))
	if filepath.Base(dir) != spec.Name {
		ui.Warnf("requested worktree name %q was already taken; the daemon created %s", spec.Name, dir)
	}
	return dir, nil
}
