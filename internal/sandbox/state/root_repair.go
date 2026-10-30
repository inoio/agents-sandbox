package state

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// RepairRootStateCommand is the CLI command that quarantines a corrupt root
// document, named in the error a corrupt document produces.
const RepairRootStateCommand = "agents-sandbox state repair"

// quarantineSuffixLayout timestamps a quarantined document; nanoseconds keep two
// repairs in the same second from overwriting each other's evidence.
const quarantineSuffixLayout = "20060102T150405.000000000Z"

// now is a seam over time.Now for deterministic quarantine names in tests.
var now = time.Now

// renameRootStateFile is a seam over os.Rename so tests can exercise the
// quarantine's rename-failure path deterministically.
var renameRootStateFile = os.Rename

// RepairRootState is the explicit recovery path for a corrupt launcher-global
// document (#332). Under the blocking root lock it moves an unparsable
// <root>/state.yaml aside to state.yaml.corrupt-<UTC timestamp> and returns that
// path; the next transaction then starts from a zero RootState. Nothing is
// deleted, so the quarantined bytes remain available for inspection.
//
// A missing or healthy document is left alone and yields an empty path. A
// document with a newer schema is valid data and is refused with
// ErrStateSchemaUnsupported, as is any other read failure: only a document this
// binary has positively classified as unparsable is moved.
func RepairRootState() (string, error) {
	release, _, lockErr := lockRootState(true)
	if lockErr != nil {
		return "", lockErr
	}
	defer release()

	if _, readErr := ReadRootState(); readErr != nil {
		if !errors.Is(readErr, ErrStateUnparsable) {
			return "", readErr
		}
		path := rootStateFile()
		quarantinedPath := path + ".corrupt-" + now().UTC().Format(quarantineSuffixLayout)
		if err := renameRootStateFile(path, quarantinedPath); err != nil {
			return "", fmt.Errorf("quarantine root state file: %w", err)
		}
		return quarantinedPath, nil
	}
	return "", nil
}
