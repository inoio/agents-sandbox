package notify

import (
	"os/exec"
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

func TestDesktopNotifierCommand(t *testing.T) {
	orig := execCommand
	defer func() { execCommand = orig }()

	var ran []string
	execCommand = func(name string, _ ...string) *exec.Cmd {
		ran = append(ran, name)
		return exec.Command("/bin/true") // never actually run via Run below
	}

	d := &DesktopNotifier{}
	if _, err := d.buildCmd("opencode", "waiting"); err != nil {
		t.Fatalf("buildCmd error = %v", err)
	}
	if len(ran) == 0 {
		t.Error("buildCmd did not build a command")
	}
}
