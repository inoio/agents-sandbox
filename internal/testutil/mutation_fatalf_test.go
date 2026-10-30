package testutil_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/testutil"
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

func TestRunGit_FatalfReportsFailedCommand(t *testing.T) {
	fake := &recordingTB{}
	message := captureFatalf(t, fake, func() {
		testutil.RunGit(fake, t.TempDir(), "nonexistentcommand123")
	})
	if !strings.Contains(message, "nonexistentcommand123") {
		t.Errorf("RunGit fatal message %q does not name the failing command", message)
	}
}

func TestWritePath_FatalfReportsUnwritablePath(t *testing.T) {
	fake := &recordingTB{}
	path := filepath.Join(t.TempDir(), "missing-dir", "file.txt")
	message := captureFatalf(t, fake, func() {
		testutil.WritePath(fake, path, "content")
	})
	if !strings.Contains(message, path) {
		t.Errorf("WritePath fatal message %q does not name the path %q", message, path)
	}
}

func TestWriteYAML_FatalfReportsMarshalError(t *testing.T) {
	fake := &recordingTB{}
	message := captureFatalf(t, fake, func() {
		testutil.WriteYAML(fake, t.TempDir(), "bad.yaml", map[string]any{"bad": failingMarshaler{}})
	})
	if !strings.Contains(message, "marshal yaml") {
		t.Errorf("WriteYAML fatal message %q does not mention YAML marshalling", message)
	}
}

func TestInitRepo_FatalfReportsExistingRepository(t *testing.T) {
	dir := t.TempDir()
	testutil.RunGit(t, dir, "init", "--quiet")

	fake := &recordingTB{tempDir: dir}
	message := captureFatalf(t, fake, func() {
		testutil.InitRepo(fake)
	})
	if !strings.Contains(message, "init repo") {
		t.Errorf("InitRepo fatal message %q does not mention repository init", message)
	}
}
