package main

import (
	"context"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/runoptions"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// resolveNoRebuild parses args on the named command (run or shell) with a real
// resolver and returns the resolved NoRebuild option.
func resolveNoRebuild(t *testing.T, commandName string, args ...string) bool {
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
	return opts.NoRebuild
}

func TestNoRebuildFlagIsOffByDefaultForRunAndShell(t *testing.T) {
	for _, commandName := range []string{cmdRun, cmdShell} {
		t.Run(commandName, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			if resolveNoRebuild(t, commandName) {
				t.Fatal("NoRebuild = true, want off by default")
			}
		})
	}
}

func TestNoRebuildFlagSetsOptionForRunAndShell(t *testing.T) {
	for _, commandName := range []string{cmdRun, cmdShell} {
		t.Run(commandName, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			if !resolveNoRebuild(t, commandName, "--"+flagNoRebuild) {
				t.Fatal("NoRebuild = false, want --no-rebuild to set it")
			}
		})
	}
}

func TestNoRebuildFlagHasNoShorthand(t *testing.T) {
	for _, commandName := range []string{cmdRun, cmdShell} {
		cmd, _ := setupCommandFixtures(t, commandName, "--help")
		foundCmd, _, err := cmd.Find([]string{commandName})
		if err != nil {
			t.Fatalf("Find %q: %v", commandName, err)
		}
		flag := foundCmd.Flags().Lookup(flagNoRebuild)
		if flag == nil {
			t.Fatalf("%s command must have --%s", commandName, flagNoRebuild)
		}
		if flag.Shorthand != "" {
			t.Errorf("%s --%s shorthand = %q, want none", commandName, flagNoRebuild, flag.Shorthand)
		}
	}
}

// TestNoRebuildConflictsWithRebuildFlags pins that --no-rebuild is rejected with
// the flags that only take effect through a rebuild, before anything starts.
func TestNoRebuildConflictsWithRebuildFlags(t *testing.T) {
	for _, commandName := range []string{cmdRun, cmdShell} {
		for _, conflicting := range [][]string{{"--" + flagRebuild}, {"--" + flagAgentVersion, "1.2.3"}} {
			t.Run(commandName+" "+conflicting[0], func(t *testing.T) {
				args := append([]string{commandName, "--" + flagNoRebuild, "--" + flagDryRun}, conflicting...)
				cmd, _ := setupCommandFixtures(t, args...)

				err := cmd.Execute()

				if err == nil {
					t.Fatalf("Execute(%v) = nil, want a mutually-exclusive flag error", args)
				}
				if !strings.Contains(err.Error(), flagNoRebuild) {
					t.Errorf("error = %q, want it to name --%s", err, flagNoRebuild)
				}
			})
		}
	}
}
