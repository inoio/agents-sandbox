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

// opencode2Probe reads opencode2's credential table only. The database is
// ~13 MB and mostly a churny model catalog, so its file is never hashed: the
// digest covers the credential rows, which is what a credential change moves.
//
// A missing database means a home that never stored a credential: the probe
// reports nothing and hashes the empty row set, never an error.
func opencode2Probe(home string) (probeResult, error) {
	databasePath := filepath.Join(home, opencode2DatabasePath)
	if _, err := os.Stat(databasePath); errors.Is(err, os.ErrNotExist) {
		return probeResult{Credentials: nil, RowHash: hashCredentialRows(nil)}, nil
	}

	db, err := sql.Open("sqlite", "file:"+databasePath+"?mode=ro")
	if err != nil {
		return probeResult{}, fmt.Errorf("open opencode2 database %s: %w", databasePath, err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.QueryContext(
		context.Background(),
		`SELECT integration_id, label, active, value FROM credential`,
	)
	if err != nil {
		return probeResult{}, fmt.Errorf("query opencode2 credential rows: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var projected []string
	var credentials []Credential
	for rows.Next() {
		var integrationID, label, value string
		var active bool
		if scanErr := rows.Scan(&integrationID, &label, &active, &value); scanErr != nil {
			return probeResult{}, fmt.Errorf("scan opencode2 credential row: %w", scanErr)
		}
		projected = append(projected, strings.Join(
			[]string{integrationID, label, strconv.FormatBool(active), value}, "\x00",
		))
		credentials = append(credentials, opencode2Credential(integrationID, value)...)
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
func opencode2Credential(integrationID, value string) []Credential {
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
			Source:   probeSource("opencode2", opencode2ProbeID),
			Provider: integrationID,
			Value:    stored.Key,
		}}
	case credentialTypeOAuth:
		return []Credential{{
			Kind:     CredentialLogin,
			Source:   probeSource("opencode2", opencode2ProbeID),
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

// probeResult is one probe's reading of a credential store.
//
//exhaustruct:ignore
type probeResult struct {
	Credentials []Credential
	RowHash     string
}

// runCredentialProbe runs the named probe and returns what it read. A read
// failure yields the sentinel hash and no credentials rather than no result:
// the caller must still record exactly one entry per declared probe, so a
// transient failure is not mistaken for the probe's removal.
func runCredentialProbe(home, probe string) probeResult {
	if probe != opencode2ProbeID {
		return probeResult{Credentials: nil, RowHash: unreadableProbeHash}
	}
	result, err := opencode2Probe(home)
	if err != nil {
		return probeResult{Credentials: nil, RowHash: unreadableProbeHash}
	}
	return probeResult{Credentials: result.Credentials, RowHash: result.RowHash}
}
