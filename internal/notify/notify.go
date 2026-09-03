package notify

import "fmt"

// AudioMode selects how audio notifications are delivered.
type AudioMode string

const (
	AudioSystem AudioMode = "system" // bundled sound via afplay/paplay
	AudioBell   AudioMode = "bell"   // terminal BEL
	AudioOff    AudioMode = "off"    // no audio
)

// Config is the resolved notify setting for a session.
type Config struct {
	Desktop bool
	Audio   AudioMode
	OnInput bool
	OnDone  bool
	OnError bool
}

// Active reports whether any notification channel is enabled. Only the two
// real channel modes count: an empty (zero-value) AudioMode must be inactive.
func (c Config) Active() bool {
	return c.Desktop || c.Audio == AudioSystem || c.Audio == AudioBell
}

// ParseAudioMode validates an audio-mode string.
func ParseAudioMode(s string) (AudioMode, error) {
	switch AudioMode(s) {
	case AudioSystem, AudioBell, AudioOff:
		return AudioMode(s), nil
	default:
		return "", fmt.Errorf("unknown audio mode %q (want system, bell, or off)", s)
	}
}

// OverrideMode is a --notify / OPENCODE_SANDBOX_NOTIFY value.
type OverrideMode string

const (
	OverrideNone    OverrideMode = ""        // flag/env absent
	OverrideOn      OverrideMode = "on"      // desktop + system audio
	OverrideOff     OverrideMode = "off"     // nothing
	OverrideDesktop OverrideMode = "desktop" // desktop only
	OverrideAudio   OverrideMode = "audio"   // audio only
)

// ParseOverride validates a --notify / env override value.
func ParseOverride(s string) (OverrideMode, error) {
	switch OverrideMode(s) {
	case OverrideOn, OverrideOff, OverrideDesktop, OverrideAudio:
		return OverrideMode(s), nil
	case OverrideNone:
	}
	return "", fmt.Errorf("invalid --notify value %q (want on, off, desktop, or audio)", s)
}

// ApplyOverride applies a flag/env override to a config. Overrides affect only
// the channels; the per-trigger toggles are left unchanged.
func ApplyOverride(c Config, o OverrideMode) Config {
	switch o {
	case OverrideOff:
		c.Desktop = false
		c.Audio = AudioOff
	case OverrideDesktop:
		c.Desktop = true
		c.Audio = AudioOff
	case OverrideAudio:
		c.Desktop = false
		c.Audio = AudioSystem
	case OverrideOn:
		c.Desktop = true
		c.Audio = AudioSystem
	case OverrideNone:
	}
	return c
}
