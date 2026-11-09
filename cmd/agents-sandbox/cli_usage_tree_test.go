package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// renderUsage runs runUsageFunc for cmd into a buffer and returns the output.
func renderUsage(t *testing.T, cmd *cobra.Command) string {
	t.Helper()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := runUsageFunc(cmd); err != nil {
		t.Fatalf("runUsageFunc: %v", err)
	}
	return buf.String()
}

// TestRunUsageFuncExactOutput pins the byte-exact usage rendering for the run
// command: the header, the single forwarded-args entry, and the flag block. The
// output is compared in full so every WriteString, the alignment arithmetic
// (maxLen-len(name)+2) and the trailing-newline guard are load-bearing.
func TestRunUsageFuncExactOutput(t *testing.T) {
	cmd := buildRunCmd(&termiotest.Mock{})
	cmd.Annotations[annotationArgs] = `[{"name":"[-- AGENT_ARG...]","help":"Arguments forwarded to agent"}]`

	out := renderUsage(t, cmd)

	want := "Usage:\n" +
		"  run [flags] [-- AGENT_ARG...]\n" +
		"\n" +
		"  [-- AGENT_ARG...]          Arguments forwarded to agent\n" +
		"\n" +
		"\n" +
		"Flags:\n" +
		cmd.Flags().FlagUsages() +
		"\n"
	if out != want {
		t.Errorf("runUsageFunc output mismatch:\ngot:\n%q\nwant:\n%q", out, want)
	}
}

// TestRunUsageFuncExactOutputNoArgs pins the rendering for a command with no
// forwarded-args annotation and no subcommands: no arg block, no trailing
// newline from the args guard, and the flag section only.
func TestRunUsageFuncExactOutputNoArgs(t *testing.T) {
	cmd := &cobra.Command{Use: "widget"}
	cmd.Flags().Bool("verbose", false, "be verbose")

	out := renderUsage(t, cmd)

	want := "Usage:\n" +
		"  widget [flags]\n" +
		"\n" +
		"\n" +
		"Flags:\n" +
		cmd.Flags().FlagUsages() +
		"\n"
	if out != want {
		t.Errorf("runUsageFunc output mismatch:\ngot:\n%q\nwant:\n%q", out, want)
	}
}

// TestRunUsageFuncExactOutputSubcommandsAndTopics pins the byte-exact rendering
// of the argument block (two args, the second longer than minUsagePadding so the
// maxLen-growth branch and the alignment arithmetic are load-bearing), the
// available-commands section (rpad padding), the flags section, the additional
// help topics section, and the trailing "Use ..." line.
func TestRunUsageFuncExactOutputSubcommandsAndTopics(t *testing.T) {
	cmd := &cobra.Command{Use: "widget"}
	cmd.Annotations = map[string]string{
		annotationArgs: `[{"name":"[ARG]","help":"a short arg"},{"name":"[A_VERY_LONG_ARGUMENT_NAME]","help":"a long arg help"}]`,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "sub",
		Short: "a real subcommand",
		Run:   func(*cobra.Command, []string) {},
	})
	cmd.AddCommand(&cobra.Command{Use: "topic", Short: "a help topic"})
	cmd.Flags().Bool("verbose", false, "be verbose")

	out := renderUsage(t, cmd)

	want := "Usage:\n" +
		"  widget [flags] [ARG] [A_VERY_LONG_ARGUMENT_NAME]\n" +
		"\n" +
		"  [ARG]                        a short arg\n" +
		"  [A_VERY_LONG_ARGUMENT_NAME]  a long arg help\n" +
		"\n" +
		"Available Commands:\n" +
		"  sub                      a real subcommand\n" +
		"  topic                    a help topic\n" +
		"\n" +
		"Flags:\n" +
		cmd.Flags().FlagUsages() +
		"\n" +
		"\n" +
		"Additional help topics:\n" +
		"  widget topic              a help topic\n" +
		"\n" +
		"Use \"widget [command] --help\" for more information about a command.\n"
	if out != want {
		t.Errorf("runUsageFunc output mismatch:\ngot:\n%q\nwant:\n%q", out, want)
	}
}

// TestRunUsageFuncReturnsWriteError pins that runUsageFunc propagates the write
// error from the command's output writer instead of returning nil.
func TestRunUsageFuncReturnsWriteError(t *testing.T) {
	writeErr := errors.New("write failed")
	cmd := &cobra.Command{Use: "widget"}
	cmd.SetOut(errWriter{err: writeErr})

	err := runUsageFunc(cmd)
	if !errors.Is(err, writeErr) {
		t.Fatalf("runUsageFunc error = %v, want %v", err, writeErr)
	}
}

// TestExtendRunCmdAcceptsArbitraryArgs pins that the run command's Args validator
// is cobra.ArbitraryArgs: forwarding an agent argument with no flag prefix must
// not be rejected as an unknown flag or an arity error.
func TestExtendRunCmdAcceptsArbitraryArgs(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "run", "--dry-run", "--", "--some-agent-flag", "value")
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run -- --some-agent-flag value: %v", err)
	}
}

// TestExtendRunCmdInstallsUsageFunc pins that extendRunCmd wires the custom usage
// renderer: the command's usage (not cobra's default) must be produced.
func TestExtendRunCmdInstallsUsageFunc(t *testing.T) {
	cmd := &cobra.Command{Use: "widget"}
	extendRunCmd(&termiotest.Mock{}, cmd)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Usage(); err != nil {
		t.Fatalf("Usage: %v", err)
	}
	// Compare against runUsageFunc's own output so cobra's default usage (which
	// also starts with "Usage:\n  widget") does not pass.
	if out, want := buf.String(), renderUsage(t, cmd); out != want {
		t.Errorf("Usage() output = %q; want the custom runUsageFunc rendering %q", out, want)
	}
}

// TestRootImpliedRunAcceptsArbitraryArgs pins that the root command's Args
// validator accepts a forwarded positional. cobra's Find falls back to
// legacyArgs (which rejects a bare arg on a root with subcommands) when Args is
// nil, so the explicit ArbitraryArgs is what makes the implied-run path work.
func TestRootImpliedRunAcceptsArbitraryArgs(t *testing.T) {
	cmd, _ := setupCommandFixtures(t)
	if _, _, err := cmd.Find([]string{"forwarded-arg"}); err != nil {
		t.Fatalf("root Find = %v, want a forwarded positional resolved to the implied run", err)
	}
}

// TestRunNotifyFlagBareValueIsOn pins the bare-form default for --notify: the
// key must be registered and carry "on" as its no-option default so `--notify`
// with no value is accepted.
func TestRunNotifyFlagBareValueIsOn(t *testing.T) {
	cmd := buildRunCmd(&termiotest.Mock{})
	notify := cmd.Flags().Lookup(flagNotify)
	if notify == nil {
		t.Fatalf("--%s flag not registered", flagNotify)
	}
	if notify.NoOptDefVal != "on" {
		t.Errorf("--%s NoOptDefVal = %q, want %q", flagNotify, notify.NoOptDefVal, "on")
	}
}

// TestRunFlagDefaults pins the defaults of shared run/shell flags whose values
// are contract: --cpus defaults to 0 ("all") and --workspace-quota to 16G.
// --tmp-size must default to the empty string, not a fixed literal: an unset
// value is what lets options.ResolveTmpSizeMiB apply its memory-relative default
// (min(2 GiB, memory/2)); a fixed default would collide with a smaller --memory
// and be rejected by the tmp < memory check.
func TestRunFlagDefaults(t *testing.T) {
	cmd := buildRunCmd(&termiotest.Mock{})
	cpus := cmd.Flags().Lookup(flagCpus)
	if cpus == nil || cpus.DefValue != "0" {
		t.Errorf("--%s default = %v, want 0", flagCpus, cpus)
	}
	quota := cmd.Flags().Lookup(flagWorkspaceQuota)
	if quota == nil || quota.DefValue != "16G" {
		t.Errorf("--%s default = %v, want 16G", flagWorkspaceQuota, quota)
	}
	tmpSize := cmd.Flags().Lookup(flagTmpSize)
	if tmpSize == nil || tmpSize.DefValue != "" {
		t.Errorf("--%s default = %v, want \"\" (unset, memory-relative)", flagTmpSize, tmpSize)
	}
}

func TestArgsFromAnnotationsInvalidJSON(t *testing.T) {
	cmd := &cobra.Command{
		Annotations: map[string]string{annotationArgs: "{not valid json"},
	}
	if got := argsFromAnnotations(cmd); got != nil {
		t.Errorf("expected nil for invalid JSON, got %v", got)
	}
}
