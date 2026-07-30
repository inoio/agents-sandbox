package stdio

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

//nolint:gochecknoglobals // static lookup table, never mutated
var spinnerChars = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type spinner struct {
	w      io.Writer
	color  bool
	level  Level
	msg    string
	start  time.Time
	stopCh chan struct{}
	done   chan struct{}
	mu     sync.Mutex
	active bool
}

func newSpinner(w io.Writer, color bool, level Level, msg string) *spinner {
	s := &spinner{w: w, color: color, level: level}
	s.Start(msg)
	return s
}

func (s *spinner) Start(msg string) {
	s.mu.Lock()
	if s.active || s.level == LevelQuiet {
		s.mu.Unlock()
		return
	}
	s.active = true
	s.msg = msg
	s.start = time.Now()
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	if s.color {
		s.done = make(chan struct{})
		go s.animate()
		return
	}
	fmt.Fprintf(s.w, "%s... ", s.msg)
}

func formatElapsedLive(elapsed time.Duration) string {
	return fmt.Sprintf("(%ds)", int(elapsed.Seconds()))
}

func formatElapsedDone(elapsed time.Duration) string {
	return fmt.Sprintf("(%.1fs)", elapsed.Seconds())
}

func (s *spinner) animate() {
	defer close(s.done)
	i := 0
	for {
		select {
		case <-s.stopCh:
			return
		default:
		}
		elapsed := time.Since(s.start)
		fmt.Fprintf(s.w, "\r\033[K%s %s%s%s", s.msg, spinnerChars[i%len(spinnerChars)], " ", formatElapsedLive(elapsed))
		i++
		time.Sleep(spinnerInterval)
	}
}

func (s *spinner) finish(result string) {
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		return
	}
	s.active = false
	elapsed := time.Since(s.start)
	s.mu.Unlock()

	if s.level == LevelQuiet {
		return
	}

	suffix := formatElapsedDone(elapsed)
	var final string
	switch {
	case result == "done":
		final = "✅" + suffix
	case strings.HasPrefix(result, "failed: "):
		final = fmt.Sprintf("failed %s: %s", suffix, strings.TrimPrefix(result, "failed: "))
	default:
		final = result + " " + suffix
	}

	if s.color {
		close(s.stopCh)
		<-s.done
		fmt.Fprintf(s.w, "\r\033[K%s %s\n", s.msg, final)
		return
	}
	fmt.Fprintf(s.w, "%s\n", final)
}

func (s *spinner) Stop() {
	s.finish("done")
}

func (s *spinner) StopError(err error) {
	s.finish(fmt.Sprintf("failed: %v", err))
}
