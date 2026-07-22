package log

import (
	"fmt"
	"io"
	"sync"
	"time"
)

var spinnerChars = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type Spinner struct {
	w      io.Writer
	color  bool
	msg    string
	stopCh chan struct{}
	done   chan struct{}
	mu     sync.Mutex
	active bool
}

func NewSpinner(l *Logger) *Spinner {
	return &Spinner{w: l.w, color: l.color}
}

func (s *Spinner) Start(msg string) {
	s.mu.Lock()
	if s.active {
		s.mu.Unlock()
		return
	}
	s.active = true
	s.msg = msg
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	if s.color {
		s.done = make(chan struct{})
		go s.animate()
	} else {
		fmt.Fprintf(s.w, "%s... ", s.msg)
	}
}

func (s *Spinner) animate() {
	defer close(s.done)
	i := 0
	for {
		select {
		case <-s.stopCh:
			return
		default:
		}
		fmt.Fprintf(s.w, "\r%s %s", s.msg, spinnerChars[i%len(spinnerChars)])
		i++
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *Spinner) finish(result string) {
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		return
	}
	s.active = false
	s.mu.Unlock()

	if s.color {
		close(s.stopCh)
		<-s.done
		fmt.Fprintf(s.w, "\r\033[K%s %s\n", s.msg, result)
	} else {
		fmt.Fprintf(s.w, "%s\n", result)
	}
}

func (s *Spinner) Stop() {
	s.finish("done")
}

func (s *Spinner) StopError(err error) {
	s.finish(fmt.Sprintf("failed: %v", err))
}
