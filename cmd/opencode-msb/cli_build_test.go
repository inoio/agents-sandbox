package main

import (
	"os/exec"
	"slices"
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
)

func TestBuildBuildCmd(t *testing.T) {
	t.Run("B1_dry_run", func(t *testing.T) {
		// build --dry-run → info "dry-run: Would build runner image"
		ui := &stdio.Mock{}

		root := buildRootCmd(ui)
		root.SetArgs([]string{cmdBuild, "--dry-run"})

		if err := root.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !slices.Contains(ui.InfoCalls, "dry-run: Would build runner image") {
			t.Errorf("expected info 'dry-run: Would build runner image'; got: %v", ui.InfoCalls)
		}

		// Dry-run must not invoke docker at all
		if len(ui.SpinnerCalls) > 0 {
			t.Errorf("dry-run should not spawn spinner; got: %v", ui.SpinnerCalls)
		}
	})

	t.Run("B2_dry_run_with_rebuild", func(t *testing.T) {
		// build --dry-run --rebuild → same info (force ignored in dry-run)
		ui := &stdio.Mock{}

		root := buildRootCmd(ui)
		root.SetArgs([]string{cmdBuild, "--dry-run", "--rebuild"})

		if err := root.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !slices.Contains(ui.InfoCalls, "dry-run: Would build runner image") {
			t.Errorf("expected info 'dry-run: Would build runner image'; got: %v", ui.InfoCalls)
		}

		// --rebuild is a no-op in dry-run; no spinner should be started
		if len(ui.SpinnerCalls) > 0 {
			t.Errorf("dry-run should not spawn spinner even with --rebuild; got: %v", ui.SpinnerCalls)
		}
	})

	t.Run("B3_no_dry_run_spinner", func(t *testing.T) {
		// build (no flags, docker available) → spinner "Building runner image"
		if _, err := exec.LookPath("docker"); err != nil {
			t.Skip("docker not in PATH; cannot test build path")
		}

		ui := &stdio.Mock{}

		root := buildRootCmd(ui)
		root.SetArgs([]string{cmdBuild})

		_ = root.Execute() // build may fail (e.g. image build error); we only check spinner

		if !slices.Contains(ui.SpinnerCalls, "Building runner image") {
			t.Errorf("expected spinner 'Building runner image'; got: %v", ui.SpinnerCalls)
		}
	})

	t.Run("B4_image_build_dry_run", func(t *testing.T) {
		// image build --dry-run → same behavior as build --dry-run (shared buildBuildCmd)
		ui := &stdio.Mock{}

		root := buildRootCmd(ui)
		root.SetArgs([]string{cmdImage, cmdBuild, "--dry-run"})

		if err := root.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !slices.Contains(ui.InfoCalls, "dry-run: Would build runner image") {
			t.Errorf("expected info 'dry-run: Would build runner image' via image build; got: %v", ui.InfoCalls)
		}
	})

	t.Run("B5_image_build_with_dry_run_and_rebuild", func(t *testing.T) {
		// image build --dry-run --rebuild → same dry-run info (shared buildBuildCmd)
		ui := &stdio.Mock{}

		root := buildRootCmd(ui)
		root.SetArgs([]string{cmdImage, cmdBuild, "--dry-run", "-r"})

		if err := root.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !slices.Contains(ui.InfoCalls, "dry-run: Would build runner image") {
			t.Errorf("expected info 'dry-run: Would build runner image' via image build -r; got: %v", ui.InfoCalls)
		}

		// Verify short flags also work for rebuild + dry-run combination
		if len(ui.SpinnerCalls) > 0 {
			t.Errorf("dry-run should not spawn spinner; got: %v", ui.SpinnerCalls)
		}
	})
}
