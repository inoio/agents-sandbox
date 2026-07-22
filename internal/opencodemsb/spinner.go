package opencodemsb

import (
	"fmt"
	"io"
	"sync"
	"time"
)

var spinnerChars = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type spinner struct {
	w      io.Writer
	color  bool
	msg    string
	stopCh chan struct{}
	done   chan struct{}
	mu     sync.Mutex
	active bool
}

func startSpinner(msg string) *spinner {
	logMu.Lock()
	s := &spinner{w: logOut.w, color: logOut.color, msg: msg}
	logMu.Unlock()
	s.start()
	return s
}

func (s *spinner) start() {
	s.mu.Lock()
	if s.active {
		s.mu.Unlock()
		return
	}
	s.active = true
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	if s.color {
		s.done = make(chan struct{})
		go s.animate()
	} else {
		logMu.Lock()
		fmt.Fprintf(s.w, "%s... ", s.msg)
		logMu.Unlock()
	}
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
		logMu.Lock()
		fmt.Fprintf(s.w, "\r%s %s", s.msg, spinnerChars[i%len(spinnerChars)])
		logMu.Unlock()
		i++
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *spinner) finish(result string) {
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
		logMu.Lock()
		fmt.Fprintf(s.w, "\r\033[K%s %s\n", s.msg, result)
		logMu.Unlock()
	} else {
		logMu.Lock()
		fmt.Fprintf(s.w, "%s\n", result)
		logMu.Unlock()
	}
}

func (s *spinner) stop() {
	s.finish("done")
}

func (s *spinner) stopError(err error) {
	s.finish(fmt.Sprintf("failed: %v", err))
}
