package learn

import (
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/channel"
)

// LearnArtefactAge is how long an unreviewed artefact survives, so a session
// whose process died is still reviewable by config learn.
const LearnArtefactAge = 30 * 24 * time.Hour

// PruneArtefacts removes learn artefacts last modified before
// now-LearnArtefactAge, returning how many it removed.
func PruneArtefacts(now time.Time) (int, error) {
	return channel.Prune(now.Add(-LearnArtefactAge))
}
