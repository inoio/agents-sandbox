package notify

import "testing"

func TestConfigActive(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"empty", Config{}, false},
		{"desktop", Config{Desktop: true}, true},
		{"audio system", Config{Audio: AudioSystem}, true},
		{"audio bell", Config{Audio: AudioBell}, true},
		{"audio off", Config{Audio: AudioOff}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.Active(); got != tc.want {
				t.Errorf("Active() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseAudioMode(t *testing.T) {
	if m, err := ParseAudioMode("system"); err != nil || m != AudioSystem {
		t.Errorf("system: got %q err %v", m, err)
	}
	if _, err := ParseAudioMode("loud"); err == nil {
		t.Error("expected error for unknown audio mode")
	}
}

func TestParseOverride(t *testing.T) {
	for _, in := range []string{"on", "off", "desktop", "audio"} {
		if _, err := ParseOverride(in); err != nil {
			t.Errorf("ParseOverride(%q) error = %v", in, err)
		}
	}
	if _, err := ParseOverride("bogus"); err == nil {
		t.Error("expected error for unknown override")
	}
}

func TestApplyOverride(t *testing.T) {
	base := Config{Desktop: true, Audio: AudioSystem, OnInput: true, OnDone: true, OnError: true}
	if got := ApplyOverride(base, OverrideOff); got.Active() {
		t.Errorf("OverrideOff should deactivate, got %+v", got)
	}
	if got := ApplyOverride(base, OverrideDesktop); got.Desktop != true || got.Audio != AudioOff {
		t.Errorf("OverrideDesktop got %+v", got)
	}
	if got := ApplyOverride(base, OverrideAudio); got.Desktop || got.Audio != AudioSystem {
		t.Errorf("OverrideAudio got %+v", got)
	}
	if got := ApplyOverride(base, OverrideNone); got.Desktop != true || got.Audio != AudioSystem {
		t.Errorf("OverrideNone should leave config untouched, got %+v", got)
	}
	// Triggers are preserved through overrides.
	if got := ApplyOverride(base, OverrideAudio); !got.OnInput || !got.OnDone || !got.OnError {
		t.Errorf("triggers should be preserved, got %+v", got)
	}
}
