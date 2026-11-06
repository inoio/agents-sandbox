//go:build integration

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestConfigCommandsWorkOutsideWorktree pins that config resolution stays on the
// repo slug. The per-slug user config dir is shared by every clone of an origin,
// so an outside worktree — which has its own checkout slug — must still read it.
// The config commands are the reason the launcher-config resolver must not move
// to the checkout slug.
func TestConfigCommandsWorkOutsideWorktree(t *testing.T) {
	repo := testutil.InitRepo(t)
	testutil.RunGit(t, repo, "remote", "add", "origin", originCloneURL)
	outside := filepath.Join(t.TempDir(), "wt")
	testutil.RunGit(t, repo, "worktree", "add", outside)

	identity := mustIdentity(t, outside)
	if !identity.OutsideWorktree {
		t.Fatalf("%q must resolve as an outside worktree", outside)
	}
	if identity.RepoSlug == identity.CheckoutSlug {
		t.Fatalf("outside worktree must have its own checkout slug, got %q", identity.CheckoutSlug)
	}

	t.Chdir(outside)

	t.Run("config agent reads the repo-slug user config", func(t *testing.T) {
		cmd, ui := setupCommandFixtures(t, cmdConfig, cmdAgent, "opencode")
		// A value present only in the repo-slug layer proves the resolver used
		// the repo slug: the checkout-slug layer is empty, so a checkout-keyed
		// resolver would report the default (true) instead.
		perSlugDir := filepath.Join(configpaths.Get().UserConfigDir(), identity.RepoSlug)
		if err := os.MkdirAll(perSlugDir, 0o755); err != nil {
			t.Fatal(err)
		}
		testutil.WriteYAML(t, perSlugDir, "config.yaml", map[string]any{"provision-host-config": false})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("config agent: %v", err)
		}
		joined := strings.Join(ui.OutCalls, "\n")
		if !strings.Contains(joined, "provision-host-config=false") {
			t.Errorf("config agent must read the repo-slug user config, got:\n%s", joined)
		}
	})

	t.Run("config home works outside the checkout", func(t *testing.T) {
		cmd, ui := setupCommandFixtures(t, cmdConfig, cmdHome)
		testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.yaml", "home:\n  .gitconfig:\n")

		if err := cmd.Execute(); err != nil {
			t.Fatalf("config home: %v", err)
		}
		joined := strings.Join(ui.OutCalls, "\n")
		if !strings.Contains(joined, "/home/dev/.gitconfig") {
			t.Errorf("config home must list the shared user manifest, got:\n%s", joined)
		}
	})

	// The per-slug user config dir is keyed by the repo slug, so config paths
	// must report the repo slug (shared by every clone), not the outside
	// worktree's own checkout slug.
	t.Run("config paths reports the shared repo slug", func(t *testing.T) {
		cmd, ui := setupCommandFixtures(t, cmdConfig, cmdPaths)

		if err := cmd.Execute(); err != nil {
			t.Fatalf("config paths: %v", err)
		}
		out := strings.Join(ui.OutCalls, "\n")
		if !strings.Contains(out, "repo slug:  "+identity.RepoSlug) {
			t.Errorf("config paths must report the repo slug %q, got:\n%s", identity.RepoSlug, out)
		}
		perSlugDir := filepath.Join(configpaths.Get().UserConfigDir(), identity.RepoSlug)
		if !strings.Contains(out, perSlugDir) {
			t.Errorf("config paths must list the shared repo-slug dir %q, got:\n%s", perSlugDir, out)
		}
		if !slices.Contains(ui.InfoCalls, configPathsHint()) {
			t.Errorf("config paths must print the stderr hint; InfoCalls=%q", ui.InfoCalls)
		}
	})
}
