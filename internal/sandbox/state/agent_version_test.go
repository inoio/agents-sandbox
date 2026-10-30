package state

import (
	"errors"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestRecordAgentVersionWritesExistingState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}
	if err := writeState(k, HomeState{HomeVolume: "vol"}); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	if err := RecordAgentVersion(k, "2.0.0"); err != nil {
		t.Fatalf("RecordAgentVersion: %v", err)
	}

	got, err := ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.AgentVersion != "2.0.0" {
		t.Errorf("AgentVersion = %q, want 2.0.0", got.AgentVersion)
	}
	if got.HomeVolume != "vol" {
		t.Errorf("HomeVolume = %q, want preserved vol", got.HomeVolume)
	}
}

func TestRecordAgentVersionSkipsMissingState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}

	if err := RecordAgentVersion(k, "2.0.0"); err != nil {
		t.Fatalf("RecordAgentVersion: %v", err)
	}

	if _, err := ReadState(k); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("ReadState error = %v, want ErrStateNotFound (no state created)", err)
	}
}

func TestRecordAgentVersionSkipsEmptyVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}

	if err := RecordAgentVersion(k, ""); err != nil {
		t.Fatalf("RecordAgentVersion: %v", err)
	}
	if _, err := ReadState(k); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("ReadState error = %v, want ErrStateNotFound", err)
	}
}
