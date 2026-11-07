package viperconfig

import (
	"testing"
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
