package state

import (
	"errors"
	"os"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestWithHomeStateCreatesWhenMissing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	if err := WithHomeState(k, func(hs *HomeState, exists bool) (bool, error) {
		if exists {
			t.Fatal("exists = true, want false")
		}
		hs.HomeVolume = "vol-1"
		return true, nil
	}); err != nil {
		t.Fatalf("WithHomeState: %v", err)
	}
	got, _ := ReadState(k)
	if got == nil || got.HomeVolume != "vol-1" {
		t.Fatalf("state = %+v, want vol-1", got)
	}
}

func TestWithHomeStateCorruptReturnsErrorWithoutWriting(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	if err := os.MkdirAll(KeyDir(k), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile(k), []byte("{ corrupt: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	err := WithHomeState(k, func(*HomeState, bool) (bool, error) { called = true; return true, nil })
	if !errors.Is(err, ErrStateUnparsable) {
		t.Fatalf("err = %v, want ErrStateUnparsable", err)
	}
	if called {
		t.Fatal("fn called on a corrupt document")
	}
	if _, statErr := os.Stat(stateFile(k)); statErr != nil {
		t.Fatalf("corrupt file was removed: %v", statErr)
	}
}

func TestWithHomeStateNoWriteIsNoop(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	if err := WithHomeState(k, func(*HomeState, bool) (bool, error) { return false, nil }); err != nil {
		t.Fatalf("WithHomeState: %v", err)
	}
	if _, err := ReadState(k); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("read = %v, want ErrStateNotFound", err)
	}
}

func TestWithHomeStateCallbackErrorDoesNotWrite(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	wantErr := errors.New("boom")
	err := WithHomeState(k, func(*HomeState, bool) (bool, error) { return true, wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if _, rerr := ReadState(k); !errors.Is(rerr, ErrStateNotFound) {
		t.Fatalf("read = %v, want ErrStateNotFound", rerr)
	}
}

func TestWithHomeStatePresentExistsTrue(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	if err := WriteState(k, HomeState{HomeVolume: "vol-1", AgentVersion: "1.2.3"}); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	if err := WithHomeState(k, func(hs *HomeState, exists bool) (bool, error) {
		if !exists {
			t.Fatal("exists = false, want true for a present state file")
		}
		hs.HomeVolume = "vol-2"
		return true, nil
	}); err != nil {
		t.Fatalf("WithHomeState: %v", err)
	}
	got, err := ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.HomeVolume != "vol-2" || got.AgentVersion != "1.2.3" {
		t.Fatalf("state = %+v, want vol-2 with agent version preserved", got)
	}
}

func TestReadStateCorruptIsUnparsable(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	if err := os.MkdirAll(KeyDir(k), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile(k), []byte("{ corrupt: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadState(k)
	if !errors.Is(err, ErrStateUnparsable) {
		t.Fatalf("err = %v, want ErrStateUnparsable", err)
	}
	if errors.Is(err, ErrStateNotFound) {
		t.Fatalf("err = %v, must not be classified as not-found", err)
	}
}
