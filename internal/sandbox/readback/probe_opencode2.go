package readback

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // pure-Go sqlite driver, so no CGO is needed to read opencode2's store
)

// opencode2ProbeID is the credential-probe name opencode2's profile declares.
const opencode2ProbeID = "opencode2-sqlite"

// opencode2DatabasePath is where opencode2 keeps its credential rows, relative
// to the sandbox home.
const opencode2DatabasePath = ".local/share/opencode/opencode.db"

// maxProbeValueBytes caps the value the probe materializes from one credential
// row. A credential value is a small JSON blob; the sandboxed agent controls the
// table, so a larger value is host-memory pressure the probe refuses to pay.
// The query asks SQLite for the value's length and only a bounded prefix, so an
// over-cap value never enters the projection.
const maxProbeValueBytes = 64 * 1024

// maxProbeProjectedBytes caps the total row projection the probe materializes,
// so a store packed with many small rows is bounded too. Both bounds are far
// above any real credential set.
const maxProbeProjectedBytes = 256 * 1024

// openDatabase opens the probe's read-only SQLite store. It is a package seam
// so a test can drive the open-failure branch: sql.Open defers the connection,
// so the pure-Go driver only reports an unknown driver there and never fails on
// a real database path.
var openDatabase = func(dsn string) (*sql.DB, error) {
	return sql.Open("sqlite", dsn)
}

// opencode2Probe reads opencode2's credential table only. The database is
// ~13 MB and mostly a churny model catalog, so its file is never hashed: the
// digest covers the credential rows, which is what a credential change moves.
//
// A missing database means a home that never stored a credential: the probe
// reports nothing and hashes the empty row set, never an error.
//
// A row the agent made larger than maxProbeValueBytes, or rows whose combined
// projection exceeds maxProbeProjectedBytes, make the store unreadable: the
// oversized content is never materialized, and the caller reports the probe as
// undetermined rather than silently dropping the row.
//
//deadcode:ignore reason="opencode2's credential reader; invoked through runCredentialProbe once the S7 run --learn after-run reads the home back"
func opencode2Probe(agentName, home string) (probeResult, error) {
	databasePath := filepath.Join(home, opencode2DatabasePath)
	if _, err := os.Stat(databasePath); errors.Is(err, os.ErrNotExist) {
		return probeResult{Credentials: nil, RowHash: hashCredentialRows(nil)}, nil
	}

	db, err := openDatabase("file:" + databasePath + "?mode=ro")
	if err != nil {
		return probeResult{}, fmt.Errorf("open opencode2 database %s: %w", databasePath, err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.QueryContext(
		context.Background(),
		`SELECT integration_id, label, active,
		        length(CAST(value AS BLOB)) AS value_bytes,
		        substr(CAST(value AS BLOB), 1, ?) AS value_prefix
		 FROM credential`,
		// gomutants:disable-next-line INTEGER_DECREMENT,INTEGER_INCREMENT reason="substr's length bound of maxProbeValueBytes+1, +0, or +2 returns identical bytes for any value not longer than the bound, and a longer value is rejected by the value_bytes check before its bytes are used"
		maxProbeValueBytes+1,
	)
	if err != nil {
		return probeResult{}, fmt.Errorf("query opencode2 credential rows: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var projected []string
	var credentials []Credential
	projectedBytes := 0
	for rows.Next() {
		var integrationID, label string
		var active, valueBytes sql.NullInt64
		var valuePrefix []byte
		if scanErr := rows.Scan(&integrationID, &label, &active, &valueBytes, &valuePrefix); scanErr != nil {
			return probeResult{}, fmt.Errorf("scan opencode2 credential row: %w", scanErr)
		}
		if valueBytes.Int64 > maxProbeValueBytes {
			return unreadableProbeResult(), nil
		}
		value := string(valuePrefix)
		isActive := active.Int64 != 0
		projection := strings.Join(
			[]string{integrationID, label, strconv.FormatBool(isActive), value}, "\x00",
		)
		projectedBytes += len(projection) + 1
		if projectedBytes > maxProbeProjectedBytes {
			return unreadableProbeResult(), nil
		}
		projected = append(projected, projection)
		if !isActive {
			// Only an active credential is a fact; an inactive row stays in the
			// digest so a deactivation is still a change.
			continue
		}
		credentials = append(credentials, opencode2Credential(agentName, integrationID, value)...)
	}
	if err := rows.Err(); err != nil {
		return probeResult{}, fmt.Errorf("read opencode2 credential rows: %w", err)
	}
	return probeResult{Credentials: credentials, RowHash: hashCredentialRows(projected)}, nil
}

// opencode2Credential maps an integration id and its stored JSON value to the
// credential it describes. A "key" value carries the secret inline, so
// it is a static credential; any other type (opencode2's "oauth") is a login,
// whose value is not read out.
//
//deadcode:ignore reason="maps an opencode2 credential row to a Credential; reached through opencode2Probe from the S7 run --learn after-run"
func opencode2Credential(agentName, integrationID, value string) []Credential {
	var stored struct {
		Type string `json:"type"`
		Key  string `json:"key"`
	}
	if err := json.Unmarshal([]byte(value), &stored); err != nil {
		return nil
	}
	switch stored.Type {
	case credentialTypeKey:
		return []Credential{{
			Kind:     CredentialStatic,
			Source:   probeSource(agentName, opencode2ProbeID),
			Provider: integrationID,
			Value:    stored.Key,
		}}
	case credentialTypeOAuth:
		return []Credential{{
			Kind:     CredentialLogin,
			Source:   probeSource(agentName, opencode2ProbeID),
			Provider: integrationID,
		}}
	default:
		return nil
	}
}

// credentialTypeKey and credentialTypeOAuth are the stored-value type
// discriminators opencode2 writes into a credential row.
const (
	credentialTypeKey   = "key"
	credentialTypeOAuth = "oauth"
)

// hashCredentialRows digests the projected credential rows. The projection is
// sorted first, so the digest depends on the rows' content, not their order.
//
//deadcode:ignore reason="digests the credential rows for the probe's manifest entry; reached through opencode2Probe"
func hashCredentialRows(projected []string) string {
	sorted := slices.Clone(projected)
	slices.Sort(sorted)
	digest := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return hex.EncodeToString(digest[:])
}

// unreadableProbeMarker is the digest input of a probe whose store could not be
// read. It is disjoint from every real row projection: a projection is built
// from an integration id, so it can never be this bare marker.
const unreadableProbeMarker = "unreadable"

// unreadableProbeHash is the stable digest a probe entry carries when its read
// failed. It differs from the empty-set digest, so a transition into or out of
// unreadability is detected, while two consecutive failures compare equal.
var unreadableProbeHash = hashCredentialRows([]string{unreadableProbeMarker})

// unreadableProbeResult is a probe's outcome when its store could not be read or
// exceeded the probe's read bound: the sentinel digest, no credentials, and the
// unreadable flag the caller turns into ReadBack.Unreadable. Both cases are
// undetermined, never an empty credential set.
//
//deadcode:ignore reason="builds a probe's unreadable outcome; reached through opencode2Probe and runCredentialProbe"
func unreadableProbeResult() probeResult {
	return probeResult{Credentials: nil, RowHash: unreadableProbeHash, Unreadable: true}
}

// probeResult is one probe's reading of a credential store.
//
//exhaustruct:ignore
type probeResult struct {
	Credentials []Credential
	RowHash     string
	// Unreadable records that the store could not be read, so the caller can
	// name the probe in ReadBack.Unreadable rather than silently reporting no
	// credentials.
	Unreadable bool
}

// runCredentialProbe runs the named probe and returns what it read. A read
// failure yields the sentinel hash, no credentials, and Unreadable true rather
// than no result: the caller must still record exactly one entry per declared
// probe, so a transient failure is not mistaken for the probe's removal.
//
//deadcode:ignore reason="dispatches an agent's declared credential probes; reached through Read and TakeManifest from the S7 run --learn after-run"
func runCredentialProbe(agentName, home, probe string) probeResult {
	if probe != opencode2ProbeID {
		return unreadableProbeResult()
	}
	result, err := opencode2Probe(agentName, home)
	if err != nil {
		return unreadableProbeResult()
	}
	return result
}
