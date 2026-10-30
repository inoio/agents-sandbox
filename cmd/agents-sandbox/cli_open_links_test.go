package main

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/runoptions"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// resolveOpenLinks parses args on the named command (run or shell) with a real
// resolver and returns the resolved OpenLinks option.
func resolveOpenLinks(t *testing.T, commandName string, args ...string) bool {
	t.Helper()
	ui := &termio.Mock{}
	root := buildRootCmd(ui)
	command, _, err := root.Find([]string{commandName})
	if err != nil {
		t.Fatalf("Find %q: %v", commandName, err)
	}
	if err := command.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	command.SetContext(context.WithValue(context.Background(), (*launcherConfigKey)(nil), mustResolver(t, command)))
	opts, err := runoptions.Build(resolverFromContext(command.Context()).Config(), command, ui)
	if err != nil {
		t.Fatalf("runoptions.Build: %v", err)
	}
	return opts.OpenLinks
}

func TestOpenLinksDefaultsOnForRunAndShell(t *testing.T) {
	for _, commandName := range []string{cmdRun, cmdShell} {
		t.Run(commandName, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			if !resolveOpenLinks(t, commandName) {
				t.Fatal("OpenLinks = false, want on by default")
			}
		})
	}
}

func TestOpenLinksFlagDisables(t *testing.T) {
	for _, commandName := range []string{cmdRun, cmdShell} {
		t.Run(commandName, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			if resolveOpenLinks(t, commandName, "--open-links=false") {
				t.Fatal("OpenLinks = true, want --open-links=false to disable")
			}
		})
	}
}

func TestOpenLinksEnvDisables(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_OPEN_LINKS", "false")
	if resolveOpenLinks(t, cmdRun) {
		t.Fatal("OpenLinks = true, want AGENTS_SANDBOX_OPEN_LINKS=false to disable")
	}
}

func TestOpenLinksConfigDisablesAndFlagOverrides(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{"open-links": false})
	if resolveOpenLinks(t, cmdRun) {
		t.Fatal("OpenLinks = true, want the config file to disable")
	}
	if !resolveOpenLinks(t, cmdRun, "--open-links") {
		t.Fatal("OpenLinks = false, want --open-links to override the config file")
	}
}
