package homeconfig

import "testing"

// FuzzParseHomeSection drives arbitrary config-file bytes (YAML/JSON) through
// the user-controlled "home:" manifest parser. It must never panic on hostile
// or malformed input (a crash here breaks `run` for a user with a bad config),
// and it must never report an error alongside a manifest.
func FuzzParseHomeSection(f *testing.F) {
	f.Add([]byte("home:\n  ~/.gitconfig: /host/gitconfig\n"))
	f.Add([]byte("home:\n  ~/.ssh: {source: /host/ssh, hook: copy}\n"))
	f.Add([]byte("home: {}\n"))
	f.Add([]byte("home: ~/.x\n"))
	f.Add([]byte("home: []\n"))
	f.Add([]byte("home:\n  1: /host\n"))
	f.Add([]byte("{}"))
	f.Add([]byte(""))
	f.Add([]byte("not: valid: yaml: ["))
	f.Add([]byte("home:\n\tx: y\n"))
	f.Add([]byte("home:\n  a: !!binary aGk=\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		manifest, _, err := ParseHomeSection(data, "fuzz.yaml")
		if err != nil && manifest != nil {
			t.Fatalf("ParseHomeSection(%q) returned an error and a non-nil manifest", data)
		}
	})
}
