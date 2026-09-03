package notify

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	_ "embed"

	"github.com/inoio/opencode-sandbox/internal/termio"
)

//go:embed assets/notify.wav
var notifyWAV []byte

// Trigger identifies the kind of notification.
type Trigger int

const (
	TriggerInput Trigger = iota
	TriggerDone
	TriggerError
)

// Notification is a single user-facing alert.
type Notification struct {
	Trigger Trigger
	Title   string
	Body    string
}

// Backend delivers notifications.
type Backend interface {
	Notify(n Notification)
}

// execCommand is a test seam; production uses exec.Command.
//
//nolint:gochecknoglobals // test seam
var execCommand = exec.Command

// NewBackend builds the effective backend for cfg, or nil when inactive.
func NewBackend(cfg Config, ui termio.UI) Backend {
	if !cfg.Active() {
		return nil
	}
	var parts []Backend
	if cfg.Desktop {
		parts = append(parts, &DesktopNotifier{})
	}
	switch cfg.Audio {
	case AudioSystem:
		parts = append(parts, &SystemAudioNotifier{})
	case AudioBell:
		parts = append(parts, &BellNotifier{})
	case AudioOff:
	}
	return &compositeBackend{cfg: cfg, parts: parts, ui: ui}
}

// compositeBackend fans a notification out to each channel, honoring triggers.
type compositeBackend struct {
	cfg   Config
	parts []Backend
	ui    termio.UI
}

func (c *compositeBackend) Notify(n Notification) {
	if !c.triggerEnabled(n.Trigger) {
		return
	}
	for _, p := range c.parts {
		p.Notify(n)
	}
}

func (c *compositeBackend) triggerEnabled(t Trigger) bool {
	switch t {
	case TriggerInput:
		return c.cfg.OnInput
	case TriggerDone:
		return c.cfg.OnDone
	case TriggerError:
		return c.cfg.OnError
	default:
		return false
	}
}

// DesktopNotifier shows a desktop notification.
type DesktopNotifier struct{}

func (d *DesktopNotifier) Notify(n Notification) {
	cmd, err := d.buildCmd(n.Title, n.Body)
	if err != nil {
		return
	}
	_ = cmd.Run()
}

// buildCmd constructs the platform desktop-notification command.
//
//nolint:unparam // error return kept for the execCommand test seam
func (d *DesktopNotifier) buildCmd(title, body string) (*exec.Cmd, error) {
	if runtime.GOOS == "darwin" {
		script := "display notification " + shellQuote(body) + " with title " + shellQuote(title)
		return execCommand("osascript", "-e", script), nil
	}
	return execCommand("notify-send", title, body), nil
}

// SystemAudioNotifier plays the bundled WAV via the host audio tool.
type SystemAudioNotifier struct{}

func (a *SystemAudioNotifier) Notify(_ Notification) {
	path, err := writeTempWAV()
	if err != nil {
		return
	}
	defer os.Remove(path)
	var args []string
	switch {
	case commandExists("afplay"):
		args = []string{"afplay", path}
	case commandExists("paplay"):
		args = []string{"paplay", path}
	case commandExists("pw-play"):
		args = []string{"pw-play", path}
	case commandExists("aplay"):
		args = []string{"aplay", path}
	default:
		bell()
		return
	}
	_ = execCommand(args[0], args[1:]...).Run()
}

// BellNotifier emits a terminal BEL.
type BellNotifier struct{}

func (b *BellNotifier) Notify(_ Notification) { bell() }

func bell() {
	if f, err := os.Stdout.Stat(); err == nil && f.Mode()&os.ModeCharDevice != 0 {
		_, _ = os.Stdout.Write([]byte("\a"))
	}
}

func writeTempWAV() (string, error) {
	f, err := os.CreateTemp("", "opencode-notify-*.wav")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(notifyWAV); err != nil {
		return "", err
	}
	return f.Name(), nil
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
