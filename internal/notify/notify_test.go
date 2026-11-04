package notify

import (
	"testing"
)

func TestParseAudioMode(t *testing.T) {
	if m, err := ParseAudioMode("system"); err != nil || m != AudioSystem {
		t.Errorf("system: got %q err %v", m, err)
	}
	if _, err := ParseAudioMode("loud"); err == nil {
		t.Error("expected error for unknown audio mode")
	}
}

// TestParseOverride pins the accepted --notify values, each returned as given,
// and that anything else, including an empty value, is refused.
func TestParseOverride(t *testing.T) {
	for _, in := range []string{"on", "off", "desktop", "audio"} {
		if got, err := ParseOverride(in); err != nil || got != OverrideMode(in) {
			t.Errorf("ParseOverride(%q) = %q, %v; want %q", in, got, err, in)
		}
	}
	for _, in := range []string{"bogus", ""} {
		if _, err := ParseOverride(in); err == nil {
			t.Errorf("ParseOverride(%q) error = nil, want a refusal", in)
		}
	}
}

// TestApplyOverride pins that an override sets only the channels, from
// whatever the config had, and keeps the per-trigger toggles; no override
// leaves the config as it is.
func TestApplyOverride(t *testing.T) {
	enabled := Config{Desktop: true, Audio: AudioSystem, OnInput: true, OnDone: false, OnError: true}
	disabled := Config{Desktop: false, Audio: AudioOff, OnInput: true, OnDone: false, OnError: true}
	cases := []struct {
		mode        OverrideMode
		from        Config
		wantDesktop bool
		wantAudio   AudioMode
	}{
		{mode: OverrideOn, from: disabled, wantDesktop: true, wantAudio: AudioSystem},
		{mode: OverrideOff, from: enabled, wantDesktop: false, wantAudio: AudioOff},
		{mode: OverrideDesktop, from: Config{Audio: AudioSystem}, wantDesktop: true, wantAudio: AudioOff},
		{mode: OverrideAudio, from: Config{Desktop: true}, wantDesktop: false, wantAudio: AudioSystem},
		{mode: OverrideNone, from: enabled, wantDesktop: true, wantAudio: AudioSystem},
	}
	for _, tc := range cases {
		t.Run(string(tc.mode), func(t *testing.T) {
			got := ApplyOverride(tc.from, tc.mode)
			if got.Desktop != tc.wantDesktop || got.Audio != tc.wantAudio {
				t.Errorf(
					"channels = desktop %v, audio %v; want %v, %v",
					got.Desktop,
					got.Audio,
					tc.wantDesktop,
					tc.wantAudio,
				)
			}
			if got.OnInput != tc.from.OnInput || got.OnDone != tc.from.OnDone || got.OnError != tc.from.OnError {
				t.Errorf("triggers changed: %+v from %+v", got, tc.from)
			}
		})
	}
}

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
