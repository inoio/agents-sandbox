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
		"a launcher document without keys owns nothing": {
			liveAbsent: true, launcher: `{}`,
			want: ReconcileKeep,
		},
		"a launcher document without keys leaves the agent's file alone": {
			live: `{"theme":"dark"}`, launcher: `{}`,
			want: ReconcileKeep,
		},
		"a null launcher document owns nothing": {
			live: `{"theme":"dark"}`, launcher: `null`,
			want: ReconcileKeep,
		},
		"a null live document is a file without keys": {
			live: `null`, launcher: `{"model":"haiku"}`,
			want: ReconcileWrite, wantContent: `{"model":"haiku"}`,
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

// TestReconcileHandlesATopLevelNullDocument pins that a live main config that is
// a top-level null (YAML "null", "~", "---"; JSON/JSONC/JSON5 "null") decodes to
// a document with no keys instead of a nil map the merge would panic on.
func TestReconcileHandlesATopLevelNullDocument(t *testing.T) {
	cases := map[string]struct {
		path, live, launcher, want string
	}{
		"yaml null":              {"settings.yaml", "null", "model: x\n", "model: x"},
		"yaml tilde":             {"settings.yaml", "~", "model: x\n", "model: x"},
		"yaml document marker":   {"settings.yaml", "---", "model: x\n", "model: x"},
		"json null":              {"settings.json", "null", `{"model":"x"}`, `{"model":"x"}`},
		"json null with comment": {"settings.json", "null // c", `{"model":"x"}`, `{"model":"x"}`},
	}
	for name, tc := range cases {
		got, err := Reconcile(tc.path, nil, []byte(tc.live), []byte(tc.launcher))
		if err != nil {
			t.Errorf("%s: Reconcile error %v", name, err)
			continue
		}
		if got.Action != ReconcileWrite {
			t.Errorf("%s: action = %v, want a write", name, got.Action)
			continue
		}
		if !DocumentsEqual(tc.path, got.Content, []byte(tc.want)) {
			t.Errorf("%s: content = %s, want %s", name, got.Content, tc.want)
		}
	}
	// A null live document with no launcher has nothing to merge.
	for _, path := range []string{"settings.yaml", "settings.json"} {
		got, err := Reconcile(path, nil, []byte("null"), nil)
		if err != nil || got.Action != ReconcileKeep {
			t.Errorf("%s: Reconcile = %+v, %v; want keep", path, got, err)
		}
	}
}

// TestReconcileReachesAFixedPoint applies each outcome the way the launcher does
// — writing ReconcileWrite.Content, treating ReconcileRemove as an absent file,
// and recording the launcher's content (via LauncherContent, as
// reconcileMainConfig does) as the next base — and pins that a keyless launcher
// document cannot make the main config alternate between created and deleted
// across runs. The live cases cover a present-but-keyless agent file, which a
// raw-bytes base would delete on the second run.
func TestReconcileReachesAFixedPoint(t *testing.T) {
	cases := map[string]struct {
		base, live, launcher string
	}{
		"keyless launcher":                              {launcher: `{}`},
		"keyless launcher over the agent's file":        {live: `{"theme":"dark"}`, launcher: `{}`},
		"keyless launcher over a keyless agent file":    {live: `{}`, launcher: `{}`},
		"keyless launcher over a null agent file":       {live: `null`, launcher: `{}`},
		"keyless launcher over a whitespace agent file": {live: "  \n", launcher: `{}`},
		"empty launcher":                                {},
		"null launcher":                                 {launcher: `null`},
		"null launcher over a keyless agent file":       {live: `{}`, launcher: `null`},
		"launcher with keys":                            {live: `{"a":1}`, launcher: `{"a":2}`},
		"launcher that drops its last key":              {base: `{"a":1}`, live: `{"a":1}`, launcher: `{}`},
	}
	for name, tc := range cases {
		live := bytesOrNil(tc.live)
		base := bytesOrNil(tc.base)
		launcher := bytesOrNil(tc.launcher)
		var settled ReconcileAction
		for run := range 4 {
			got, err := Reconcile("settings.json", base, live, launcher)
			if err != nil {
				t.Fatalf("%s: run %d: Reconcile error %v", name, run, err)
			}
			// The first run settles the file and the base; every run after it
			// must find nothing to do, or the main config would be rewritten (or
			// deleted) on one run and recreated on the next.
			if run > 0 {
				if run == 1 {
					settled = got.Action
				}
				if got.Action != settled {
					t.Errorf("%s: run %d action = %v, want %v as on run 1", name, run, got.Action, settled)
				}
				if got.Action != ReconcileKeep {
					t.Errorf("%s: run %d action = %v, want a fixed point (keep)", name, run, got.Action)
				}
			}
			switch got.Action {
			case ReconcileWrite:
				live = got.Content
			case ReconcileRemove:
				live = nil
			case ReconcileKeep:
				// The file is unchanged.
			}
			// recordMainConfigBase records the launcher's content, with a
			// keyless document read as no content.
			recorded, err := LauncherContent("settings.json", launcher)
			if err != nil {
				t.Fatalf("%s: run %d: LauncherContent error %v", name, run, err)
			}
			base = recorded
		}
	}
}

// TestReconcileReportsAMarshalFailure pins that a result the encoder cannot
// serialize is reported rather than returned as an empty write. JSON5 decodes a
// bare NaN to a float the JSON encoder refuses; a launcher key differing from
// the live file forces the merged document through the encoder.
func TestReconcileReportsAMarshalFailure(t *testing.T) {
	_, err := Reconcile(
		"settings.json",
		[]byte(`{"model":"opus"}`),
		[]byte(`{"model":"haiku"}`),
		[]byte(`{"model":"sonnet","limit":NaN}`),
	)
	if err == nil {
		t.Fatal("Reconcile over an unmarshalable value = nil error, want the marshal failure")
	}
	if !strings.Contains(err.Error(), "unsupported value") {
		t.Errorf("Reconcile error = %v, want the marshal failure", err)
	}
}

// TestApplyLauncherKeysHandlesANilLiveDocument pins the defensive guard: a nil
// live document (no keys) is written into without panicking, since maps.Clone
// keeps a nil map nil. decodeOrEmpty never returns a nil map, but the guard keeps
// applyLauncherKeys safe for any caller.
func TestApplyLauncherKeysHandlesANilLiveDocument(t *testing.T) {
	got := applyLauncherKeys(nil, nil, map[string]any{"model": "haiku"})
	if len(got) != 1 || got["model"] != "haiku" {
		t.Errorf("applyLauncherKeys(nil, nil, launcher) = %v, want the launcher's key", got)
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
