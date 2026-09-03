package notify

import (
	"os/exec"
	"reflect"
	"testing"
)

// recordingBackend records the notifications it receives.
type recordingBackend struct {
	got []Notification
}

func (r *recordingBackend) Notify(n Notification) { r.got = append(r.got, n) }

func TestNewBackendInactive(t *testing.T) {
	if NewBackend(Config{}, nil) != nil {
		t.Error("inactive config should produce nil backend")
	}
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
