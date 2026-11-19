package state

import (
	"errors"
	"time"
)

// LearnSession records what the after-run needs to analyse a session later, when
// the owning process is gone. It holds paths, a timestamp, and a manifest —
// never a value.
type LearnSession struct {
	StartedAt    time.Time            `yaml:"started_at"`
	HomeVolume   string               `yaml:"home_volume"`
	ArtefactPath string               `yaml:"artefact_path,omitempty"`
	Baseline     []LearnManifestEntry `yaml:"baseline,omitempty"`
}

// LearnManifestEntry mirrors readback.ManifestEntry in the state package, which
// cannot import readback (readback imports state).
type LearnManifestEntry struct {
	Key     string    `yaml:"key"`
	Size    int64     `yaml:"size,omitempty"`
	ModTime time.Time `yaml:"mod_time,omitempty"`
	Hash    string    `yaml:"hash,omitempty"`
}

// RecordLearnSession writes s as the key's recorded learning session, preserving
// the other HomeState fields. A second call replaces the first.
func RecordLearnSession(k Key, s LearnSession) error {
	return WithHomeState(k, func(cur *HomeState, _ bool) (bool, error) {
		cur.Learn = &s
		return true, nil
	})
}

// ReadLearnSession returns the key's recorded learning session and whether one
// exists. A missing state file or a state that records no session reports false
// with a zero session.
func ReadLearnSession(k Key) (LearnSession, bool, error) {
	homeState, err := ReadState(k)
	if err != nil {
		if errors.Is(err, ErrStateNotFound) {
			return LearnSession{}, false, nil
		}
		return LearnSession{}, false, err
	}
	if homeState.Learn == nil {
		return LearnSession{}, false, nil
	}
	return *homeState.Learn, true, nil
}

// ClearLearnSession removes the key's recorded learning session. It is a no-op
// when none is recorded, and it creates no state file when none exists.
func ClearLearnSession(k Key) error {
	return WithHomeState(k, func(cur *HomeState, exists bool) (bool, error) {
		if !exists || cur.Learn == nil {
			return false, nil
		}
		cur.Learn = nil
		return true, nil
	})
}
