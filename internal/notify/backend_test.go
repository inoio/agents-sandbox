package notify

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNewBackendActiveConfigs(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want []Backend
	}{
		{
			"desktop+system",
			Config{Desktop: true, Audio: AudioSystem},
			[]Backend{&DesktopNotifier{}, &SystemAudioNotifier{}},
		},
		{"desktop+off", Config{Desktop: true, Audio: AudioOff}, []Backend{&DesktopNotifier{}}},
		{"bell only", Config{Desktop: false, Audio: AudioBell}, []Backend{&BellNotifier{}}},
		{"system only", Config{Desktop: false, Audio: AudioSystem}, []Backend{&SystemAudioNotifier{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBackend(tc.cfg, nil)
			if b == nil {
				t.Fatal("active config should produce a backend")
			}
			cb, ok := b.(*compositeBackend)
			if !ok {
				t.Fatalf("expected compositeBackend, got %T", b)
			}
			if len(cb.parts) != len(tc.want) {
				t.Fatalf("expected %d parts, got %d", len(tc.want), len(cb.parts))
			}
			for i := range tc.want {
				if _, ok := cb.parts[i].(*DesktopNotifier); ok {
					if _, want := tc.want[i].(*DesktopNotifier); !want {
						t.Errorf("part %d: unexpected DesktopNotifier", i)
					}
				}
				if _, ok := cb.parts[i].(*SystemAudioNotifier); ok {
					if _, want := tc.want[i].(*SystemAudioNotifier); !want {
						t.Errorf("part %d: unexpected SystemAudioNotifier", i)
					}
				}
				if _, ok := cb.parts[i].(*BellNotifier); ok {
					if _, want := tc.want[i].(*BellNotifier); !want {
						t.Errorf("part %d: unexpected BellNotifier", i)
					}
				}
			}
		})
	}
}

func TestNewBackendInactive(t *testing.T) {
	if NewBackend(Config{}, nil) != nil {
		t.Error("inactive config should produce nil backend")
	}
}

func TestBuildDesktopCommandDarwin(t *testing.T) {
	orig := execCommand
	defer func() { execCommand = orig }()

	var got []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		got = append([]string{name}, args...)
		return exec.Command("/bin/true")
	}

	buildDesktopCommand("darwin", `Ti"tle\`, `Bo"dy\`)

	want := []string{
		"osascript",
		"-e",
		`display notification "Bo\"dy\\" with title "Ti\"tle\\"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("osascript args mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestBuildDesktopCommandLinux(t *testing.T) {
	orig := execCommand
	defer func() { execCommand = orig }()

	var got []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		got = append([]string{name}, args...)
		return exec.Command("/bin/true")
	}

	buildDesktopCommand("linux", `a"b\c`, "plain body")

	want := []string{"notify-send", `a"b\c`, "plain body"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("notify-send args mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestWriteTempWAVCreateError(t *testing.T) {
	t.Setenv("TMPDIR", "/nonexistent-xyz")
	if _, err := writeTempWAV(); err == nil {
		t.Error("expected error when temp dir is unusable")
	}
}

func TestAppleQuote(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`plain`, `"plain"`},
		{`He said "hi"`, `"He said \"hi\""`},
		{`C:\path`, `"C:\\path"`},
		{`a"b\`, `"a\"b\\"`},
	}
	for _, tt := range tests {
		if got := appleQuote(tt.in); got != tt.want {
			t.Errorf("appleQuote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDesktopNotifierRunsCommand(t *testing.T) {
	orig := execCommand
	defer func() { execCommand = orig }()
	fake := &fakeCmd{}
	execCommand = fake.cmd

	(&DesktopNotifier{}).Notify(Notification{Title: "t", Body: "b"})

	if len(fake.got) == 0 {
		t.Fatal("expected buildCmd/execCommand to be invoked")
	}
	if fake.got[0] != "notify-send" {
		t.Errorf("expected notify-send on linux, got %v", fake.got)
	}
}

func TestInstallFailFastPanicsOnExecCommand(t *testing.T) {
	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	InstallFailFast()
	defer func() {
		if recover() == nil {
			t.Fatal("expected the fail-fast execCommand to panic")
		}
	}()
	execCommand("notify-send")
}

func TestSystemAudioNotifierWithToolInPath(t *testing.T) {
	for _, tool := range []string{"afplay", "paplay", "pw-play", "aplay"} {
		t.Run(tool, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tool)
			if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)

			orig := execCommand
			defer func() { execCommand = orig }()
			fake := &fakeCmd{}
			execCommand = fake.cmd

			(&SystemAudioNotifier{}).Notify(Notification{Trigger: TriggerDone})

			if len(fake.got) != 2 || fake.got[0] != tool {
				t.Errorf("expected exec of %q with wav path, got %v", tool, fake.got)
			}
		})
	}
}

func TestSystemAudioNotifierFallsBackToBell(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	orig := execCommand
	defer func() { execCommand = orig }()
	called := false
	execCommand = func(_ string, _ ...string) *exec.Cmd {
		called = true
		return exec.Command("/bin/true")
	}

	(&SystemAudioNotifier{}).Notify(Notification{Trigger: TriggerDone})

	if called {
		t.Error("expected no audio command when no tool is available")
	}
}

func TestSystemAudioNotifierSurvivesTempWAVError(t *testing.T) {
	t.Setenv("TMPDIR", "/nonexistent-xyz")
	(&SystemAudioNotifier{}).Notify(Notification{Trigger: TriggerDone})
}

// TestCompositeBackendTriggerEnabled pins that each trigger follows its own
// toggle alone, and that an unknown trigger is never enabled.
func TestCompositeBackendTriggerEnabled(t *testing.T) {
	for _, tc := range []struct {
		trigger Trigger
		only    Config
	}{
		{TriggerInput, Config{OnInput: true}},
		{TriggerDone, Config{OnDone: true}},
		{TriggerError, Config{OnError: true}},
	} {
		on := &compositeBackend{cfg: tc.only}
		if !on.triggerEnabled(tc.trigger) {
			t.Errorf("trigger %d with only its toggle on = disabled, want enabled", tc.trigger)
		}
		others := Config{OnInput: !tc.only.OnInput, OnDone: !tc.only.OnDone, OnError: !tc.only.OnError}
		if off := (&compositeBackend{cfg: others}); off.triggerEnabled(tc.trigger) {
			t.Errorf("trigger %d with only its toggle off = enabled, want disabled", tc.trigger)
		}
	}
	all := &compositeBackend{cfg: Config{OnInput: true, OnDone: true, OnError: true}}
	if all.triggerEnabled(Trigger(99)) {
		t.Error("unknown trigger should be disabled")
	}
}

func TestBellWritesBELOnCharDevice(t *testing.T) {
	origStdout := os.Stdout
	defer func() { os.Stdout = origStdout }()
	dev, err := os.OpenFile("/dev/null", os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("cannot open char device: %v", err)
	}
	defer dev.Close()
	os.Stdout = dev

	(&BellNotifier{}).Notify(Notification{Trigger: TriggerDone})
}

func TestCompositeBackendHonorsTriggerToggles(t *testing.T) {
	cfg := Config{Desktop: true, Audio: AudioSystem, OnInput: true, OnDone: false, OnError: true}
	sink := &recordingBackend{}
	cb := &compositeBackend{cfg: cfg, parts: []Backend{sink}, ui: nil}

	cb.Notify(Notification{Trigger: TriggerDone})
	if len(sink.got) != 0 {
		t.Errorf("OnDone=false: Done notification should be filtered, got %+v", sink.got)
	}
	cb.Notify(Notification{Trigger: TriggerInput})
	cb.Notify(Notification{Trigger: TriggerError})
	if len(sink.got) != 2 {
		t.Errorf("expected 2 notifications (input, error), got %+v", sink.got)
	}
}
