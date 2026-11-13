package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/runoptions"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// resolveOpenLinks parses args on the named command (run or shell) with a real
// resolver and returns the resolved OpenLinks option.
func resolveOpenLinks(t *testing.T, commandName string, args ...string) bool {
	t.Helper()
	ui := &termiotest.Mock{}
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
			configpathstest.WithMockConfigPaths(t)
			if !resolveOpenLinks(t, commandName) {
				t.Fatal("OpenLinks = false, want on by default")
			}
		})
	}
}

func TestOpenLinksFlagDisables(t *testing.T) {
	for _, commandName := range []string{cmdRun, cmdShell} {
		t.Run(commandName, func(t *testing.T) {
			configpathstest.WithMockConfigPaths(t)
			if resolveOpenLinks(t, commandName, "--open-links=false") {
				t.Fatal("OpenLinks = true, want --open-links=false to disable")
			}
		})
	}
}

func TestOpenLinksEnvDisables(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_OPEN_LINKS", "false")
	if resolveOpenLinks(t, cmdRun) {
		t.Fatal("OpenLinks = true, want AGENTS_SANDBOX_OPEN_LINKS=false to disable")
	}
}

func TestOpenLinksConfigDisablesAndFlagOverrides(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{"open-links": false})
	if resolveOpenLinks(t, cmdRun) {
		t.Fatal("OpenLinks = true, want the config file to disable")
	}
	if !resolveOpenLinks(t, cmdRun, "--open-links") {
		t.Fatal("OpenLinks = false, want --open-links to override the config file")
	}
}

// TestOpenLinksSpaceFormDisablesAndLeaksNothing pins F19 and its inverse end to
// end: the space form `--open-links <falsey>` must disable the host opener
// exactly like `--open-links=true|false` and must not leak the word into the
// agent argv. It runs through execute, the only path that normalizes the space
// form before pflag parses (pflag's NoOptDefVal would otherwise leave
// open-links on and forward the falsey token as a positional argument). "0" is
// the spelling the literal true/false vocabulary used to miss.
func TestOpenLinksSpaceFormDisablesAndLeaksNothing(t *testing.T) {
	for _, value := range []string{"false", "0"} {
		t.Run(value, func(t *testing.T) {
			initTestProjectDir(t)

			mock := &msbtest.MockMsbClient{}
			sb := &msbtest.MockSandbox{}
			_, ui := setupRunMocks(t, mock, sb, cmdRun)

			if err := execute([]string{cmdRun, "--open-links", value}, ui); err != nil {
				t.Fatalf("execute run --open-links %s: %v", value, err)
			}

			openerStayedOff := slices.ContainsFunc(ui.VerboseCalls, func(line string) bool {
				return strings.Contains(line, "not starting host opener") && strings.Contains(line, "enabled false")
			})
			if !openerStayedOff {
				t.Errorf("expected the host opener to stay off; verbose calls: %q", ui.VerboseCalls)
			}
			if strings.Contains(strings.Join(sb.AttachArgs, " "), "'"+value+"'") {
				t.Errorf("AttachArgs = %q, want the space-form value not to reach the agent argv", sb.AttachArgs)
			}
		})
	}
}

// TestOpenLinksSpaceFormInvalidValueRejected pins that a value spelling the flag
// accepts from users is consumed and rejected by pflag's bool parser exactly
// like --open-links=<invalid>, instead of pflag taking the implicit true (host
// opener stays on) and leaking the word into the agent argv. "off"/"on" match
// the natural-language spelling case-insensitively; pflag's Bool then rejects
// them.
func TestOpenLinksSpaceFormInvalidValueRejected(t *testing.T) {
	for _, value := range []string{"off", "ON"} {
		t.Run(value, func(t *testing.T) {
			initTestProjectDir(t)

			mock := &msbtest.MockMsbClient{}
			sb := &msbtest.MockSandbox{}
			_, ui := setupRunMocks(t, mock, sb, cmdRun)

			err := execute([]string{cmdRun, "--open-links", value}, ui)
			if err == nil || !strings.Contains(err.Error(), `for "--open-links" flag`) {
				t.Fatalf("execute run --open-links %s error = %v, want a pflag parse error", value, err)
			}
		})
	}
}
