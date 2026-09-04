package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestRunUsageFuncLongArgNameAligns(t *testing.T) {
	cmd := buildRunCmd(&termio.Mock{})
	// A positional arg name longer than the minimum padding forces the
	// maxLen-growing branch in runUsageFunc.
	cmd.Annotations[annotationArgs] = `[{"name":"[-- VERY_LONG_OPENCODE_ARGUMENT]","help":"some help"}]`

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := runUsageFunc(cmd); err != nil {
		t.Fatalf("runUsageFunc: %v", err)
	}
	if !strings.Contains(buf.String(), "VERY_LONG_OPENCODE_ARGUMENT") {
		t.Errorf("expected the long arg name in usage output:\n%s", buf.String())
	}
}

func TestRunUsageFuncListsHelpTopics(t *testing.T) {
	cmd := buildRunCmd(&termio.Mock{})
	// A subcommand with no Run function is treated as an additional help
	// topic, exercising that section of runUsageFunc.
	cmd.AddCommand(&cobra.Command{
		Use:   "topic",
		Short: "A help topic",
	})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := runUsageFunc(cmd); err != nil {
		t.Fatalf("runUsageFunc: %v", err)
	}
	if !strings.Contains(buf.String(), "Additional help topics:") {
		t.Errorf("expected additional help topics section:\n%s", buf.String())
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
