package agent

import "testing"

func TestVersionProviderForBuiltIns(t *testing.T) {
	for name, wantCmd := range map[string]string{
		"opencode":    "opencode --version",
		"pi":          "pi --version",
		"claude-code": "claude --version",
	} {
		a, ok := Lookup(name)
		if !ok {
			t.Fatalf("agent %q not registered", name)
		}
		p, ok := AsVersionProvider(a)
		if !ok {
			t.Fatalf("agent %q must be a VersionProvider", name)
		}
		if got := p.VersionCmd(); got != wantCmd {
			t.Errorf("VersionCmd() = %q, want %q", got, wantCmd)
		}
	}
}

func TestExtractSemverFromOutput(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		{"bare version", "0.5.0\n", "0.5.0"},
		{"v prefix", "v0.5.0\n", "0.5.0"},
		{"pi style", "pi 0.3.0\n", "0.3.0"},
		{"claude style", "@anthropic-ai/claude-code/2.3.4 linux-x64 node-v22.14.0\n", "2.3.4"},
		{"no version", "help text\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractSemverFromOutput(tc.out)
			if tc.want == "" {
				if err == nil {
					t.Errorf("expected an error for %q, got %q", tc.out, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("extractSemverFromOutput(%q): %v", tc.out, err)
			}
			if got != tc.want {
				t.Errorf("extractSemverFromOutput(%q) = %q, want %q", tc.out, got, tc.want)
			}
		})
	}
}
