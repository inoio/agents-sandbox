package msb

import (
	"context"
	"errors"
)

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

// streamHandleStub is a hand-rolled StreamHandle used where MockSandbox's
// ShellStream needs a configured handle; it records the events to return.
type streamHandleStub struct {
	events   []StreamEvent
	closeErr error
	recvIdx  int
}

func (s *streamHandleStub) Recv(_ context.Context) (StreamEvent, error) {
	if s.recvIdx >= len(s.events) {
		return StreamEvent{}, errors.New("stream closed")
	}
	e := s.events[s.recvIdx]
	s.recvIdx++
	return e, nil
}

func (s *streamHandleStub) Close() error { return s.closeErr }
