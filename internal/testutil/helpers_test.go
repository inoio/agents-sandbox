package testutil

import (
	"errors"
	"fmt"
	"testing"
)

// failingMarshaler makes yaml.Marshal return an error (it recovers a failed
// Marshaler), which reaches WriteYAML's marshal-failure branch.
type failingMarshaler struct{}

func (failingMarshaler) MarshalYAML() (any, error) {
	return nil, errors.New("deliberate marshal failure")
}

// recordingTB is a testing.TB that records Fatalf calls and panics with
// fatalfSentinel instead of calling runtime.Goexit, so a test can assert that a
// test-support helper reported a failure. Embedding testing.TB satisfies the
// interface's private method.
type recordingTB struct {
	testing.TB

	tempDir string
	fatals  []string
}

type fatalfSentinel struct{ message string }

func (b *recordingTB) Helper() {}

func (b *recordingTB) TempDir() string { return b.tempDir }

func (b *recordingTB) Fatalf(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	b.fatals = append(b.fatals, message)
	panic(fatalfSentinel{message: message})
}

// captureFatalf runs fn and returns the message the recording TB reported via
// Fatalf. It fails the test if fn returns normally or panics with anything
// other than fatalfSentinel, which is exactly what happens when the guarded
// Fatalf is compiled away.
func captureFatalf(t *testing.T, tb *recordingTB, fn func()) string {
	t.Helper()
	recovered := func() (recovered any) {
		defer func() { recovered = recover() }()
		fn()
		return nil
	}()
	sentinel, ok := recovered.(fatalfSentinel)
	if !ok {
		t.Fatalf(
			"expected Fatalf to be called, recovered %T (%v); recorded fatals: %v",
			recovered,
			recovered,
			tb.fatals,
		)
	}
	return sentinel.message
}
