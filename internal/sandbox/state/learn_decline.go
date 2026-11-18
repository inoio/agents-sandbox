package state

import "time"

// LearnDecline is one declined learn-mode finding, remembered so the applier
// does not re-prompt for it. It is keyed on the finding's evidence hash, so the
// finding is proposed again as soon as the evidence changes (more destinations,
// a different count), and it never records the finding's proposed write.
//
//exhaustruct:ignore
type LearnDecline struct {
	EvidenceHash string    `yaml:"evidence_hash"`
	At           time.Time `yaml:"at"`
}

// RecordLearnDecline remembers that the user declined the finding whose evidence
// hashes to evidenceHash for the given repo slug. It is idempotent: recording an
// already-recorded hash keeps the original entry (and its timestamp) and writes
// nothing.
//
//deadcode:ignore reason="decline store; the pending S6 applier is the production caller"
func RecordLearnDecline(slug, evidenceHash string) error {
	_, err := WithRootState(true, func(txn *RootTxn) error {
		if txn.State().learnDeclined(slug, evidenceHash) {
			return nil
		}
		txn.State().addLearnDecline(slug, LearnDecline{EvidenceHash: evidenceHash, At: now().UTC()})
		return txn.Commit()
	})
	return err
}

// LearnDeclined reports whether the finding whose evidence hashes to
// evidenceHash was already declined for the given repo slug. A missing document,
// slug, or hash reports false: an unrecorded finding is proposed.
//
//deadcode:ignore reason="decline store; the pending S6 applier is the production caller"
func LearnDeclined(slug, evidenceHash string) (bool, error) {
	var declined bool
	_, err := WithRootState(true, func(txn *RootTxn) error {
		declined = txn.State().learnDeclined(slug, evidenceHash)
		return nil
	})
	if err != nil {
		return false, err
	}
	return declined, nil
}

// learnDeclined reports whether slug already holds a decline for evidenceHash.
//
//deadcode:ignore reason="helper reached only through the marked RecordLearnDecline/LearnDeclined until the S6 applier lands"
func (s *RootState) learnDeclined(slug, evidenceHash string) bool {
	for _, decline := range s.LearnDeclines[slug] {
		if decline.EvidenceHash == evidenceHash {
			return true
		}
	}
	return false
}

// addLearnDecline appends a decline to the slug's record, initializing the
// registry if nil.
//
//deadcode:ignore reason="helper reached only through the marked RecordLearnDecline until the S6 applier lands"
func (s *RootState) addLearnDecline(slug string, decline LearnDecline) {
	if s.LearnDeclines == nil {
		s.LearnDeclines = map[string][]LearnDecline{}
	}
	s.LearnDeclines[slug] = append(s.LearnDeclines[slug], decline)
}
