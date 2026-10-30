package viperconfig

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// The deprecation policy's "replacement wins" rule, realized once over the
// source enum: every canonical docker source outranks every deprecated dind
// alias, and within one kind the higher layer wins. A present source wins even
// when its value is false.
func TestEffectiveDockerSourcePrecedence(t *testing.T) {
	tests := []struct {
		name    string
		sources []DockerSourceValue
		want    DockerSourceValue
	}{
		{
			name: "no source",
			want: DockerSourceValue{},
		},
		{
			name:    "a none source contributes nothing",
			sources: []DockerSourceValue{{Source: DockerSourceNone, Value: true}},
			want:    DockerSourceValue{},
		},
		{
			name: "a none source does not shadow a real one",
			sources: []DockerSourceValue{
				{Source: DockerSourceNone, Value: true},
				{Source: DockerSourceDindConfig, Value: true},
			},
			want: DockerSourceValue{Source: DockerSourceDindConfig, Value: true},
		},
		{
			name: "canonical config beats the deprecated dind flag",
			sources: []DockerSourceValue{
				{Source: DockerSourceDindFlag, Value: true},
				{Source: DockerSourceDockerConfig, Value: false},
			},
			want: DockerSourceValue{Source: DockerSourceDockerConfig, Value: false},
		},
		{
			name: "canonical env beats the deprecated dind flag",
			sources: []DockerSourceValue{
				{Source: DockerSourceDindFlag, Value: false},
				{Source: DockerSourceDockerEnv, Value: true},
			},
			want: DockerSourceValue{Source: DockerSourceDockerEnv, Value: true},
		},
		{
			name: "the docker flag beats the canonical env",
			sources: []DockerSourceValue{
				{Source: DockerSourceDockerEnv, Value: true},
				{Source: DockerSourceDockerFlag, Value: false},
			},
			want: DockerSourceValue{Source: DockerSourceDockerFlag, Value: false},
		},
		{
			name: "the dind flag beats the dind env",
			sources: []DockerSourceValue{
				{Source: DockerSourceDindConfig, Value: true},
				{Source: DockerSourceDindEnv, Value: false},
				{Source: DockerSourceDindFlag, Value: true},
			},
			want: DockerSourceValue{Source: DockerSourceDindFlag, Value: true},
		},
		{
			name: "precedence does not depend on the argument order",
			sources: []DockerSourceValue{
				{Source: DockerSourceDockerFlag, Value: false},
				{Source: DockerSourceDindConfig, Value: true},
				{Source: DockerSourceDockerEnv, Value: true},
			},
			want: DockerSourceValue{Source: DockerSourceDockerFlag, Value: false},
		},
		{
			name: "the first of two equal-precedence sources wins",
			sources: []DockerSourceValue{
				{Source: DockerSourceDockerEnv, Value: true},
				{Source: DockerSourceDockerEnv, Value: false},
			},
			want: DockerSourceValue{Source: DockerSourceDockerEnv, Value: true},
		},
		{
			name: "a later equal-precedence source does not displace an earlier one",
			sources: []DockerSourceValue{
				{Source: DockerSourceDindConfig, Value: false},
				{Source: DockerSourceDindConfig, Value: true},
			},
			want: DockerSourceValue{Source: DockerSourceDindConfig, Value: false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EffectiveDockerSource(tt.sources...); got != tt.want {
				t.Errorf("EffectiveDockerSource(%v) = %+v, want %+v", tt.sources, got, tt.want)
			}
		})
	}
}

func TestDockerSourceDeprecated(t *testing.T) {
	for _, tc := range []struct {
		source DockerSource
		want   bool
	}{
		{DockerSourceNone, false},
		{DockerSourceDockerFlag, false},
		{DockerSourceDockerEnv, false},
		{DockerSourceDockerConfig, false},
		{DockerSourceDindFlag, true},
		{DockerSourceDindEnv, true},
		{DockerSourceDindConfig, true},
	} {
		if got := tc.source.Deprecated(); got != tc.want {
			t.Errorf("DockerSource(%d).Deprecated() = %v, want %v", tc.source, got, tc.want)
		}
	}
}

// Config.DockerSource reports the layer the resolved docker value came from, so
// the run and build paths can rank their flags against it.
func TestConfigDockerSource(t *testing.T) {
	tests := []struct {
		name      string
		env       [][2]string
		configYML string
		flag      string
		want      DockerSource
	}{
		{name: "nothing set", want: DockerSourceNone},
		{
			name:      "canonical config key",
			configYML: "docker: false\n",
			want:      DockerSourceDockerConfig,
		},
		{
			name: "canonical env",
			env:  [][2]string{{"AGENTS_SANDBOX_DOCKER", "true"}},
			want: DockerSourceDockerEnv,
		},
		{
			name: "deprecated canonical env prefix",
			env:  [][2]string{{"OPENCODE_SANDBOX_DOCKER", "true"}},
			want: DockerSourceDockerEnv,
		},
		{
			name: "canonical flag",
			flag: KeyDocker,
			want: DockerSourceDockerFlag,
		},
		{
			name:      "deprecated dind config key",
			configYML: "dind: true\n",
			want:      DockerSourceDindConfig,
		},
		{
			name: "deprecated dind env",
			env:  [][2]string{{"AGENTS_SANDBOX_DIND", "true"}},
			want: DockerSourceDindEnv,
		},
		{
			name: "deprecated dind env prefix",
			env:  [][2]string{{"OPENCODE_SANDBOX_DIND", "true"}},
			want: DockerSourceDindEnv,
		},
		{
			name:      "canonical config beats a dind env",
			env:       [][2]string{{"AGENTS_SANDBOX_DIND", "true"}},
			configYML: "docker: false\n",
			want:      DockerSourceDockerConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			for _, kv := range tt.env {
				t.Setenv(kv[0], kv[1])
			}
			if tt.configYML != "" {
				testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.yaml", tt.configYML)
			}
			var cmd *cobra.Command
			if tt.flag != "" {
				cmd = &cobra.Command{Use: "root"}
				cmd.Flags().Bool(tt.flag, false, "")
				if err := cmd.Flags().Set(tt.flag, "true"); err != nil {
					t.Fatalf("set %s: %v", tt.flag, err)
				}
			}

			r, err := NewResolver(cmd, "")
			if err != nil {
				t.Fatalf("NewResolver: %v", err)
			}
			if got := r.Config().DockerSource(); got != tt.want {
				t.Errorf("Config().DockerSource() = %d, want %d", got, tt.want)
			}
		})
	}
}

// A command with no canonical --docker flag registered is not a docker source,
// so the resolution falls through to the config layers.
func TestConfigDockerSourceWithoutDockerFlag(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	root := &cobra.Command{Use: "root"}

	r, err := NewResolver(root, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if got := r.Config().DockerSource(); got != DockerSourceNone {
		t.Errorf("Config().DockerSource() = %d, want none", got)
	}
}
