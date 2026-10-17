package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// This file is the #202 regression check for the deprecation register in
// .operator-shared/specs/deprecation-policy.md: every deprecated alias must
// still resolve (proved by the resolution tests named in each comment) while
// emitting exactly one warning. A change that drops an alias or its one-time
// warning breaks one of these tests.
//
// The image-reference aliases (`runner-base-dind` and the `# agents-sandbox:dind`
// Dockerfile marker) are intentionally silent and have no warning to assert;
// their resolution is covered by the image-package tests (render_test.go,
// identity_test.go).

// countFlagDeprecationWarnings counts the pflag "Flag --<name> has been
// deprecated" lines in s. pflag writes this line to the command's flag output
// once per Set call, which cobra forwards to the root command's stdout.
func countFlagDeprecationWarnings(s, flagName string) int {
	return strings.Count(s, "Flag --"+flagName+" has been deprecated")
}

// TestDeprecatedFlagAliasesWarnExactlyOnce exercises each CLI flag alias and
// asserts its deprecation warning fires exactly once.
//
// Resolution for each alias is covered separately: --dind by
// TestResolveBuildDeprecatedDindFlag and TestResolveBuildDockerFlagWinsOverDind,
// --opencode-version by TestBuildCommandOpenCodeVersionFallback, --age/-a by
// TestPruneOlderThanAndAgeConflict and TestPruneAgeDeprecated, and --names by
// TestListSandboxesQuietNames.
func TestDeprecatedFlagAliasesWarnExactlyOnce(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantFlag string
	}{
		{"dind", []string{cmdBuild, "--" + flagDind, "--dry-run"}, flagDind},
		{"dockerfile-dind", []string{cmdBuild, cmdDockerfile, "--" + flagDind}, flagDind},
		{"opencode-version", []string{cmdBuild, "--" + flagOpenCodeVersion, "1.2.3", "--dry-run"}, flagOpenCodeVersion},
		{"age", []string{cmdPrune, "--" + flagAge, "7d", "--yes"}, flagAge},
		{"age-shorthand", []string{cmdPrune, "-" + flagAge[:1], "7d", "--yes"}, flagAge},
		{"sandbox-list-names", []string{cmdList, "--" + pFlagNames}, pFlagNames},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, ui := setupCommandFixtures(t, tt.args...)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("execute %v: %v", tt.args, err)
			}
			if got := countFlagDeprecationWarnings(ui.StdOutBuffer.String(), tt.wantFlag); got != 1 {
				t.Errorf("--%s deprecation warning emitted %d times, want exactly 1; stdout:\n%s",
					tt.wantFlag, got, ui.StdOutBuffer.String())
			}
		})
	}
}

// TestDeprecatedConfigAliasesWarnExactlyOnce covers the deprecated sources that
// resolve through the launcher config rather than a CLI flag: the legacy
// OPENCODE_SANDBOX_ env prefix, the AGENTS_SANDBOX_DIND env var, and the dind
// config key. Each must surface its one-time warning exactly once.
//
// Resolution is covered by TestResolverLegacyEnvPrefixStillWorks,
// TestResolverAgentsEnvTakesPrecedenceOverLegacy, TestDindFromEnvAlias,
// TestDindConfigAlias, and TestDockerConfigWinsOverDindAlias.
func TestDeprecatedConfigAliasesWarnExactlyOnce(t *testing.T) {
	tests := []struct {
		name      string
		env       [][2]string
		configYML string
		wantWarn  string
	}{
		{
			name:     "legacy env prefix",
			env:      [][2]string{{"OPENCODE_SANDBOX_CPUS", "4"}},
			wantWarn: "OPENCODE_SANDBOX_CPUS",
		},
		{
			name:     "dind env",
			env:      [][2]string{{"AGENTS_SANDBOX_DIND", "true"}},
			wantWarn: "AGENTS_SANDBOX_DIND",
		},
		{
			name:      "dind config key",
			configYML: "dind: true\n",
			wantWarn:  "the dind config key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			for _, kv := range tt.env {
				t.Setenv(kv[0], kv[1])
			}
			if tt.configYML != "" {
				dir := configpaths.Get().ProjectConfigDir()
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(
					filepath.Join(dir, "config.yaml"), []byte(tt.configYML), 0o600,
				); err != nil {
					t.Fatal(err)
				}
			}

			ui := &termio.Mock{}
			cmd := buildRootCmd(ui)
			cmd.SetArgs([]string{cmdVersion})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("execute version: %v", err)
			}

			got := 0
			for _, warning := range ui.WarnCalls {
				if strings.Contains(warning, tt.wantWarn) {
					got++
				}
			}
			if got != 1 {
				t.Errorf("deprecation warning mentioning %q emitted %d times, want exactly 1; warnings: %v",
					tt.wantWarn, got, ui.WarnCalls)
			}
		})
	}
}
