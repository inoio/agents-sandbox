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
// same footgun (F19). A following token is taken as the value only when the
// flag's matcher accepts it, so a value spelling (case-insensitively) is
// rewritten and rejected by the flag's own validation with the same error the =
// form surfaces. A token the matcher rejects is left as a positional argument
// the agent receives: pflag does not reject it either, because the flag's
// NoOptDefVal supplies the value and the token is never inspected, so
// `--notify bogus` keeps the watcher at its implicit "on" rather than erroring.
// A dash-less token spelled like the flag is never rewritten.
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
			name: "a single-dash token after --notify stays split",
			args: []string{"run", "--notify", "-x"},
			want: []string{"run", "--notify", "-x"},
		},
		{
			name: "attached --notify value is not re-consumed",
			args: []string{"run", "--notify=off", "on"},
			want: []string{"run", "--notify=off", "on"},
		},
		{
			name: "tokens after a bare -- are untouched",
			args: []string{"run", "--", "--notify", "off"},
			want: []string{"run", "--", "--notify", "off"},
		},
		{
			name: "--notify OFF is consumed as a value (case-insensitively)",
			args: []string{"run", "--notify", "OFF"},
			want: []string{"run", "--notify=OFF"},
		},
		{
			name: "--notify false is consumed as a value",
			args: []string{"run", "--notify", "false"},
			want: []string{"run", "--notify=false"},
		},
		{
			name: "--notify with an out-of-vocabulary token is left as a positional",
			args: []string{"run", "--notify", "bogus"},
			want: []string{"run", "--notify", "bogus"},
		},
		{
			name: "a dash-less token spelled like the flag is left forwarded",
			args: []string{"run", "notify", "me"},
			want: []string{"run", "notify", "me"},
		},
		{
			name: "a captured flag value spelled like the flag is left forwarded",
			args: []string{"run", "-w", "notify", "fix", "the", "tests"},
			want: []string{"run", "-w", "notify", "fix", "the", "tests"},
		},
		{
			name: "a dash-less notify followed by a value spelling is left forwarded",
			args: []string{"run", "notify", "off"},
			want: []string{"run", "notify", "off"},
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
			name: "attached --open-links value is not re-consumed",
			args: []string{"run", "--open-links=true", "false"},
			want: []string{"run", "--open-links=true", "false"},
		},
		{
			name: "bare --open-links stays bare",
			args: []string{"run", "--open-links"},
			want: []string{"run", "--open-links"},
		},
		{
			name: "--open-links off is consumed as a value (natural-language)",
			args: []string{"run", "--open-links", "off"},
			want: []string{"run", "--open-links=off"},
		},
		{
			name: "--open-links with an out-of-vocabulary token is left as a positional",
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
		{
			name: "an unrelated flag keeps its space-form value",
			args: []string{"run", "--workspace", "true"},
			want: []string{"run", "--workspace", "true"},
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
