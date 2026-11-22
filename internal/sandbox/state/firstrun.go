package state

import "time"

// FirstRunChoice is what a first run decided, so it does not nag again for the
// same host config.
type FirstRunChoice string

const (
	// FirstRunLearned records that a learning path was chosen.
	FirstRunLearned FirstRunChoice = "learned"
	// FirstRunManual records that the user chose to configure it themselves.
	FirstRunManual FirstRunChoice = "manual"
	// FirstRunDismissed records that the run deferred the decision.
	FirstRunDismissed FirstRunChoice = "dismissed"
)

// FirstRunRecord is one recorded first-run outcome, keyed on the host config it
// was made against: a changed source hash re-arms the prompt.
//
//exhaustruct:ignore
type FirstRunRecord struct {
	Choice     FirstRunChoice `yaml:"choice"`
	SourceHash string         `yaml:"source_hash"`
	At         time.Time      `yaml:"at"`
}

// firstRunKey keys a first-run record by repo slug and agent name; neither
// contains "/", so the join is unambiguous.
func firstRunKey(slug, agentName string) string { return slug + "/" + agentName }

// firstRun returns the record for the key and whether it is present.
func (s *RootState) firstRun(slug, agentName string) (FirstRunRecord, bool) {
	record, ok := s.FirstRuns[firstRunKey(slug, agentName)]
	return record, ok
}

// setFirstRun records the key's decision, initializing the registry if nil. A
// new record replaces the old one for the same key.
func (s *RootState) setFirstRun(slug, agentName string, record FirstRunRecord) {
	if s.FirstRuns == nil {
		s.FirstRuns = map[string]FirstRunRecord{}
	}
	s.FirstRuns[firstRunKey(slug, agentName)] = record
}

// RecordFirstRun remembers how the first run for the given repo slug and agent
// resolved. It is idempotent per key: recording again replaces the stored
// record, so the latest decision and its source hash are the ones persisted. A
// zero record.At is stamped from the launcher clock in UTC.
func RecordFirstRun(slug, agentName string, record FirstRunRecord) error {
	_, err := WithRootState(true, func(txn *RootTxn) error {
		if record.At.IsZero() {
			record.At = now().UTC()
		}
		txn.State().setFirstRun(slug, agentName, record)
		return txn.Commit()
	})
	return err
}

// ReadFirstRun returns the recorded first-run outcome for the given repo slug
// and agent. A missing document or key reports (zero, false, nil): the first run
// has not been resolved yet.
func ReadFirstRun(slug, agentName string) (FirstRunRecord, bool, error) {
	var (
		record  FirstRunRecord
		present bool
	)
	_, err := WithRootState(true, func(txn *RootTxn) error {
		record, present = txn.State().firstRun(slug, agentName)
		return nil
	})
	if err != nil {
		return FirstRunRecord{}, false, err
	}
	return record, present, nil
}
