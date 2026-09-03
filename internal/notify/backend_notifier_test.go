package notify

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fakeCmd wraps exec.Command so buildCmd/Notify can run a real (harmless)
// command while still recording the arguments it was given.
type fakeCmd struct {
	got []string
}

func (f *fakeCmd) cmd(name string, args ...string) *exec.Cmd {
	f.got = append([]string{}, append([]string{name}, args...)...)
	return exec.Command("/bin/true")
}

func TestNewBackendActiveConfigs(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want []Backend
	}{
		{"desktop+system", Config{Desktop: true, Audio: AudioSystem},
			[]Backend{&DesktopNotifier{}, &SystemAudioNotifier{}}},
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

func TestSystemAudioNotifierWithToolInPath(t *testing.T) {
	for _, tool := range []string{"afplay", "paplay", "pw-play", "aplay"} {
		t.Run(tool, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tool)
			if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

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

func TestWriteTempWAVCreateError(t *testing.T) {
	t.Setenv("TMPDIR", "/nonexistent-xyz")
	if _, err := writeTempWAV(); err == nil {
		t.Error("expected error when temp dir is unusable")
	}
}

func TestSystemAudioNotifierSurvivesTempWAVError(t *testing.T) {
	t.Setenv("TMPDIR", "/nonexistent-xyz")
	(&SystemAudioNotifier{}).Notify(Notification{Trigger: TriggerDone})
}

func TestCompositeBackendTriggerEnabledUnknown(t *testing.T) {
	cb := &compositeBackend{cfg: Config{OnInput: true, OnDone: true, OnError: true}, parts: nil}
	if cb.triggerEnabled(Trigger(99)) {
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
