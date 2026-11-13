package main

import (
	"reflect"
	"testing"
)

// TestNormalizeOptionalFlagValues pins the argv rewriter that keeps the space
// form of the optional-value flags (--notify <mode>, --open-links <bool>)
// working alongside their = form. pflag's NoOptDefVal never consumes a
// following token, so without the rewrite --notify off leaves the watcher on
// and forwards the word "off" to the agent (F18); --open-links false has the
// same footgun (F19).
func TestNormalizeOptionalFlagValues(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "--notify off becomes --notify=off",
			args: []string{"run", "--notify", "off"},
			want: []string{"run", "--notify=off"},
		},
		{
			name: "--notify=off stays --notify=off",
			args: []string{"run", "--notify=off"},
			want: []string{"run", "--notify=off"},
		},
		{
			name: "every notify mode rewrites",
			args: []string{"--notify", "on", "--notify", "off", "--notify", "desktop", "--notify", "audio"},
			want: []string{"--notify=on", "--notify=off", "--notify=desktop", "--notify=audio"},
		},
		{
			name: "bare --notify stays bare",
			args: []string{"run", "--notify"},
			want: []string{"run", "--notify"},
		},
		{
			name: "--notify followed by another flag stays split",
			args: []string{"run", "--notify", "--workspace", "feat"},
			want: []string{"run", "--notify", "--workspace", "feat"},
		},
		{
			name: "tokens after a bare -- are untouched",
			args: []string{"run", "--", "--notify", "off"},
			want: []string{"run", "--", "--notify", "off"},
		},
		{
			name: "a value outside the notify vocabulary stays split",
			args: []string{"run", "--notify", "bogus"},
			want: []string{"run", "--notify", "bogus"},
		},
		{
			name: "--open-links false becomes --open-links=false",
			args: []string{"run", "--open-links", "false"},
			want: []string{"run", "--open-links=false"},
		},
		{
			name: "--open-links true becomes --open-links=true",
			args: []string{"run", "--open-links", "true"},
			want: []string{"run", "--open-links=true"},
		},
		{
			name: "--open-links=0 stays --open-links=0",
			args: []string{"run", "--open-links=0"},
			want: []string{"run", "--open-links=0"},
		},
		{
			name: "every strconv.ParseBool spelling rewrites",
			args: []string{
				"--open-links", "1", "--open-links", "0", "--open-links", "t", "--open-links", "f",
				"--open-links", "T", "--open-links", "F", "--open-links", "TRUE", "--open-links", "FALSE",
				"--open-links", "True", "--open-links", "False",
			},
			want: []string{
				"--open-links=1", "--open-links=0", "--open-links=t", "--open-links=f",
				"--open-links=T", "--open-links=F", "--open-links=TRUE", "--open-links=FALSE",
				"--open-links=True", "--open-links=False",
			},
		},
		{
			name: "--open-links=true stays --open-links=true",
			args: []string{"run", "--open-links=true"},
			want: []string{"run", "--open-links=true"},
		},
		{
			name: "bare --open-links stays bare",
			args: []string{"run", "--open-links"},
			want: []string{"run", "--open-links"},
		},
		{
			name: "open-links value outside the vocabulary stays split",
			args: []string{"--open-links", "maybe"},
			want: []string{"--open-links", "maybe"},
		},
		{
			name: "no args",
			args: []string{},
			want: []string{},
		},
		{
			name: "unrelated args pass through",
			args: []string{"run", "--workspace", "feat", "--notify=off", "positional"},
			want: []string{"run", "--workspace", "feat", "--notify=off", "positional"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeOptionalFlagValues(tt.args); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("normalizeOptionalFlagValues(%q) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}
