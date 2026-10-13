package main

import (
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestBuilderRegistryCommandIsHiddenAndRegistered(t *testing.T) {
	ui := &termio.Mock{}
	root := buildRootCmd(ui)

	cmd, _, err := root.Find([]string{cmdBuilderRegistry})
	if err != nil {
		t.Fatalf("find %s: %v", cmdBuilderRegistry, err)
	}
	if cmd.Name() != cmdBuilderRegistry {
		t.Fatalf("found command %q, want %q", cmd.Name(), cmdBuilderRegistry)
	}
	if !cmd.Hidden {
		t.Error("builder registry command should be hidden")
	}
}

func TestBuilderRegistryCommandServesConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	root := buildRootCmd(ui)

	missing := filepath.Join(t.TempDir(), "missing-registry-config.json")
	root.SetArgs([]string{cmdBuilderRegistry, "--config", missing})
	if err := root.Execute(); err == nil {
		t.Fatal("Execute() error = nil, want error for a missing config path")
	}
}
