package configmerge

import (
	"errors"
	"strings"
	"testing"
)

func TestReconcile(t *testing.T) {
	cases := map[string]struct {
		base, live, launcher string
		liveAbsent           bool
		want                 ReconcileAction
		wantContent          string
	}{
		"fresh home gets the launcher content": {
			launcher: `{"model":"opus"}`, liveAbsent: true,
			want: ReconcileWrite, wantContent: `{"model":"opus"}`,
		},
		"nothing to write and no file": {liveAbsent: true, want: ReconcileKeep},
		"an unchanged launcher leaves the agent's own writes alone": {
			base: `{"model":"opus"}`, live: `{"model":"opus","theme":"dark"}`, launcher: `{"model":"opus"}`,
			want: ReconcileKeep,
		},
		"a launcher change and an agent change both survive": {
			base: `{"model":"opus"}`, live: `{"model":"opus","theme":"dark"}`, launcher: `{"model":"haiku"}`,
			want: ReconcileWrite, wantContent: `{"model":"haiku","theme":"dark"}`,
		},
		"the launcher wins a key both changed": {
			base: `{"model":"opus"}`, live: `{"model":"sonnet"}`, launcher: `{"model":"haiku"}`,
			want: ReconcileWrite, wantContent: `{"model":"haiku"}`,
		},
		"a key the launcher dropped is removed, the agent's key stays": {
			base: `{"model":"opus","effort":"high"}`, live: `{"model":"opus","effort":"high","theme":"dark"}`,
			launcher: `{"model":"opus"}`,
			want:     ReconcileWrite, wantContent: `{"model":"opus","theme":"dark"}`,
		},
		"nested tables merge per key": {
			base:     `{"permissions":{"allow":["Bash(ls:*)"]}}`,
			live:     `{"permissions":{"allow":["Bash(ls:*)"],"deny":["Read(.env)"]}}`,
			launcher: `{"permissions":{"allow":["Bash(git:*)"]}}`,
			want:     ReconcileWrite, wantContent: `{"permissions":{"allow":["Bash(git:*)"],"deny":["Read(.env)"]}}`,
		},
		"a table the launcher dropped keeps the agent's keys in it": {
			base:     `{"permissions":{"allow":["Bash(ls:*)"]}}`,
			live:     `{"permissions":{"allow":["Bash(ls:*)"],"deny":["Read(.env)"]}}`,
			launcher: `{}`,
			want:     ReconcileWrite, wantContent: `{"permissions":{"deny":["Read(.env)"]}}`,
		},
		"a file holding only dropped launcher keys is removed": {
			base: `{"model":"opus"}`, live: `{"model":"opus"}`,
			want: ReconcileRemove,
		},
		"without a base the launcher's keys win and the agent's stay": {
			live: `{"model":"opus","theme":"dark"}`, launcher: `{"model":"haiku"}`,
			want: ReconcileWrite, wantContent: `{"model":"haiku","theme":"dark"}`,
		},
		"an unreadable base counts as none": {
			base: `{not json`, live: `{"theme":"dark"}`, launcher: `{"model":"haiku"}`,
			want: ReconcileWrite, wantContent: `{"model":"haiku","theme":"dark"}`,
		},
		"a launcher change the file already has rewrites nothing": {
			base: `{"model":"opus"}`, live: `{"model":"haiku"}`, launcher: `{"model":"haiku"}`,
			want: ReconcileKeep,
		},
		"a launcher key the agent removed is set again": {
			base:        `{"model":"opus","effort":"high"}`,
			live:        `{"model":"opus"}`,
			launcher:    `{"model":"opus","effort":"high"}`,
			want:        ReconcileWrite,
			wantContent: `{"model":"opus","effort":"high"}`,
		},
		"a launcher key the agent changed is reset": {
			base: `{"permissions":{"deny":["Read(.env)"]}}`, live: `{"permissions":{"deny":[]},"theme":"dark"}`,
			launcher: `{"permissions":{"deny":["Read(.env)"]}}`,
			want:     ReconcileWrite, wantContent: `{"permissions":{"deny":["Read(.env)"]},"theme":"dark"}`,
		},
		"a fresh home gets the launcher content even when the base matches": {
			base: `{"model":"opus"}`, launcher: `{"model":"opus"}`, liveAbsent: true,
			want: ReconcileWrite, wantContent: `{"model":"opus"}`,
		},
		"a launcher key set to null is applied": {
			live: `{"theme":"dark"}`, launcher: `{"proxy":null}`,
			want: ReconcileWrite, wantContent: `{"theme":"dark","proxy":null}`,
		},
		"a dropped launcher key that was null is removed": {
			base: `{"proxy":null}`, live: `{"proxy":null,"theme":"dark"}`, launcher: `{}`,
			want: ReconcileWrite, wantContent: `{"theme":"dark"}`,
		},
		"a launcher scalar replaces the agent's table": {
			base: `{"sandbox":{"mode":"a"}}`, live: `{"sandbox":{"mode":"a","x":1}}`, launcher: `{"sandbox":"off"}`,
			want: ReconcileWrite, wantContent: `{"sandbox":"off"}`,
		},
		"a launcher table merges into the agent's table": {
			base: `{"sandbox":"off"}`, live: `{"sandbox":{"x":1}}`, launcher: `{"sandbox":{"mode":"a"}}`,
			want: ReconcileWrite, wantContent: `{"sandbox":{"mode":"a","x":1}}`,
		},
		"a launcher table replaces the agent's scalar": {
			live: `{"sandbox":"off"}`, launcher: `{"sandbox":{"mode":"a"}}`,
			want: ReconcileWrite, wantContent: `{"sandbox":{"mode":"a"}}`,
		},
		"a dropped table left without keys is removed": {
			base: `{"p":{"a":1},"m":"x"}`, live: `{"p":{"a":1},"m":"x","t":"dark"}`, launcher: `{"m":"x"}`,
			want: ReconcileWrite, wantContent: `{"m":"x","t":"dark"}`,
		},
		"a dropped scalar the agent turned into a table is removed": {
			base: `{"p":"x"}`, live: `{"p":{"a":1},"t":"dark"}`, launcher: `{"t":"dark"}`,
			want: ReconcileWrite, wantContent: `{"t":"dark"}`,
		},
		"an emptied launcher table stays empty": {
			base: `{"p":{"a":1}}`, live: `{"p":{"a":1},"t":"dark"}`, launcher: `{"p":{}}`,
			want: ReconcileWrite, wantContent: `{"p":{},"t":"dark"}`,
		},
		"every changed table is merged": {
			base:     `{"a":{"x":1},"b":{"x":1},"c":{"x":1}}`,
			live:     `{"a":{"x":1,"u":1},"b":{"x":1,"u":1},"c":{"x":1,"u":1}}`,
			launcher: `{"a":{"x":2},"b":{"x":2},"c":{"x":2}}`,
			want:     ReconcileWrite, wantContent: `{"a":{"x":2,"u":1},"b":{"x":2,"u":1},"c":{"x":2,"u":1}}`,
		},
		"a fresh file gets a launcher document without keys": {
			liveAbsent: true, launcher: `{}`,
			want: ReconcileWrite, wantContent: `{}`,
		},
		"an empty live file is a file without keys": {
			live: "", launcher: `{"model":"haiku"}`,
			want: ReconcileWrite, wantContent: `{"model":"haiku"}`,
		},
	}
	for name, tc := range cases {
		live := []byte(tc.live)
		if tc.liveAbsent {
			live = nil
		}
		got, err := Reconcile("settings.json", bytesOrNil(tc.base), live, bytesOrNil(tc.launcher))
		if err != nil {
			t.Errorf("%s: Reconcile error %v", name, err)
			continue
		}
		if got.Action != tc.want {
			t.Errorf("%s: action = %v, want %v", name, got.Action, tc.want)
			continue
		}
		if tc.want == ReconcileWrite && !DocumentsEqual("settings.json", got.Content, []byte(tc.wantContent)) {
			t.Errorf("%s: content = %s, want %s", name, got.Content, tc.wantContent)
		}
	}
}

func TestReconcileRefusesAnUndecodableLiveFile(t *testing.T) {
	_, err := Reconcile("settings.json", nil, []byte("{not json"), []byte(`{"model":"opus"}`))
	if err == nil || errors.Unwrap(err) == nil {
		t.Errorf("Reconcile = %v, want the wrapped decode error", err)
	}
}

func TestReconcileRefusesAnUndecodableLauncherDocument(t *testing.T) {
	_, err := Reconcile("settings.json", nil, []byte(`{}`), []byte("{not json"))
	if err == nil || errors.Unwrap(err) == nil {
		t.Errorf("Reconcile = %v, want the wrapped decode error", err)
	}
}

// TestReconcileLeavesAnUndecodableFileTheLauncherOwnsNothingIn: the live file is
// only decoded when the launcher wrote or writes keys into it.
func TestReconcileLeavesAnUndecodableFileTheLauncherOwnsNothingIn(t *testing.T) {
	got, err := Reconcile("settings.json", nil, []byte("{not json"), nil)
	if err != nil || got.Action != ReconcileKeep {
		t.Errorf("Reconcile = %+v, %v; want keep", got, err)
	}
	for name, tc := range map[string]struct{ base, launcher string }{
		"the same content":     {`{"model":"opus"}`, `{"model":"opus"}`},
		"a base, no launcher":  {`{"model":"opus"}`, ""},
		"a launcher, no base":  {"", `{"model":"opus"}`},
		"a different launcher": {`{"model":"opus"}`, `{"model":"haiku"}`},
	} {
		if _, err := Reconcile(
			"settings.json",
			bytesOrNil(tc.base),
			[]byte("{not json"),
			bytesOrNil(tc.launcher),
		); err == nil {
			t.Errorf("%s: an undecodable live file was merged", name)
		}
	}
}

// TestReconcileKeepsCodexTrustBesideASnippet pins the TOML case #768 started
// from: codex's own trust entry survives a snippet's model change.
func TestReconcileKeepsCodexTrustBesideASnippet(t *testing.T) {
	live := "model = \"gpt-5\"\n\n[projects.\"/workspace\"]\ntrust_level = \"trusted\"\n"
	got, err := Reconcile("config.toml", []byte("model = \"gpt-5\"\n"), []byte(live), []byte("model = \"gpt-5.5\"\n"))
	if err != nil || got.Action != ReconcileWrite {
		t.Fatalf("Reconcile = %+v, %v; want a write", got, err)
	}
	for _, want := range []string{`model = 'gpt-5.5'`, `trust_level = 'trusted'`} {
		if !strings.Contains(string(got.Content), want) {
			t.Errorf("content %s lacks %s", got.Content, want)
		}
	}
}

func bytesOrNil(text string) []byte {
	if text == "" {
		return nil
	}
	return []byte(text)
}
