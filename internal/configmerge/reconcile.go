package configmerge

import (
	"bytes"
	"fmt"
	"maps"
	"reflect"
)

// ReconcileAction is what to do with an agent's main config file in the VM.
type ReconcileAction int

const (
	// ReconcileKeep leaves the VM file as it is.
	ReconcileKeep ReconcileAction = iota
	// ReconcileWrite replaces the VM file with Reconciliation.Content.
	ReconcileWrite
	// ReconcileRemove deletes the VM file: no key is left in it.
	ReconcileRemove
)

// Reconciliation is the outcome of Reconcile.
type Reconciliation struct {
	Action  ReconcileAction
	Content []byte
}

// LauncherContent returns the launcher's content for an agent's main config,
// with a document that decodes to zero keys read as no content: a `{}`, `null`,
// or comment-only document owns nothing, so it must neither create a file nor be
// recorded as the base the next run reconciles against. Reconcile nils such a
// document before deciding, so a caller that records the result as the base must
// use this too — otherwise the raw keyless bytes would be recorded and the next
// run would read them as a base and delete a file the run before kept. A
// document that does not decode is returned as written, with the decode error.
func LauncherContent(configPath string, launcher []byte) ([]byte, error) {
	document, err := decodeOrEmpty(configPath, launcher)
	if err != nil {
		return launcher, err
	}
	if len(document) == 0 {
		return nil, nil
	}
	return launcher, nil
}

// Reconcile decides an agent's main config file from three documents: base, the
// launcher's content last written to it (nil when none was recorded); live, the
// file in the VM (empty when it does not exist, cannot be read, or is empty);
// and launcher, the launcher's content now (nil when it has none). The launcher
// owns only the keys it writes: every key of launcher is set to the launcher's
// value, a key of base the launcher no longer writes is removed, every other key
// of live — the agent's own settings — is kept, and tables are merged key by key.
// A result equal to live leaves the file untouched, so its formatting and
// comments survive. A base that does not decode counts as none. A launcher
// document with no keys owns nothing (see LauncherContent): it neither creates a
// file nor keeps an empty one. An empty live file gets the launcher document as
// written. A live file or launcher document that does not decode is an error; the
// caller falls back to writing the launcher document.
func Reconcile(configPath string, base, live, launcher []byte) (Reconciliation, error) {
	launcher, err := LauncherContent(configPath, launcher)
	if err != nil {
		return Reconciliation{}, fmt.Errorf("decode the launcher's config for %s: %w", configPath, err)
	}
	if isEmptyDocument(live) {
		if isEmptyDocument(launcher) {
			// gomutants:disable-next-line RETURN_ZERO reason="the keep outcome is the zero Reconciliation"
			return Reconciliation{Action: ReconcileKeep, Content: nil}, nil
		}
		// A fresh file gets the launcher document as written.
		return Reconciliation{Action: ReconcileWrite, Content: launcher}, nil
	}
	if isEmptyDocument(base) && isEmptyDocument(launcher) {
		// gomutants:disable-next-line RETURN_ZERO reason="the keep outcome is the zero Reconciliation"
		return Reconciliation{Action: ReconcileKeep, Content: nil}, nil
	}
	liveDocument, err := decodeOrEmpty(configPath, live)
	if err != nil {
		return Reconciliation{}, fmt.Errorf("decode the agent's config %s: %w", configPath, err)
	}
	// A base that does not decode reads as a nil document: no keys.
	baseDocument, _ := decodeOrEmpty(configPath, base)
	// LauncherContent already decoded the launcher document without error.
	launcherDocument, _ := decodeOrEmpty(configPath, launcher)
	result := applyLauncherKeys(baseDocument, liveDocument, launcherDocument)
	if len(result) == 0 {
		return Reconciliation{Action: ReconcileRemove, Content: nil}, nil
	}
	if reflect.DeepEqual(result, liveDocument) {
		// gomutants:disable-next-line RETURN_ZERO reason="the keep outcome is the zero Reconciliation"
		return Reconciliation{Action: ReconcileKeep, Content: nil}, nil
	}
	// A marshal failure is reported so the caller can fall back to writing the
	// launcher document.
	content, err := marshalConfig(configPath, result)
	return Reconciliation{Action: ReconcileWrite, Content: content}, err
}

func isEmptyDocument(data []byte) bool { return len(bytes.TrimSpace(data)) == 0 }

// decodeOrEmpty decodes data in configPath's format; an empty document, and a
// top-level null document (YAML "null", "~" or "---"; JSON "null"), has no keys.
func decodeOrEmpty(configPath string, data []byte) (map[string]any, error) {
	if isEmptyDocument(data) {
		// gomutants:disable-next-line RETURN_ZERO reason="an empty document is only read, never written into, so a nil map reads the same"
		return map[string]any{}, nil
	}
	document, err := decodeSnippet(configPath, data)
	if err != nil {
		return document, err
	}
	if document == nil {
		// A top-level null decodes to a nil map without an error; it has no
		// keys, so it must not reach a caller that writes into the map.
		return map[string]any{}, nil
	}
	return document, nil
}

// applyLauncherKeys returns live with every key of launcher set to the
// launcher's value and every key of base the launcher no longer writes removed;
// a table on both sides is merged key by key, so the agent's keys inside it
// survive.
func applyLauncherKeys(base, live, launcher map[string]any) map[string]any {
	result := maps.Clone(live)
	if result == nil {
		// A nil live document reads as a document with no keys; the clone keeps
		// it nil, which the assignments below cannot write into.
		result = map[string]any{}
	}
	for key := range unionKeys(base, launcher) {
		launcherValue, inLauncher := launcher[key]
		liveTable, liveIsTable := live[key].(map[string]any)
		launcherTable, launcherIsTable := launcherValue.(map[string]any)
		baseTable, _ := base[key].(map[string]any)
		switch {
		case liveIsTable && launcherIsTable:
			result[key] = applyLauncherKeys(baseTable, liveTable, launcherTable)
		case inLauncher:
			result[key] = launcherValue
		case baseTable != nil:
			// A dropped table: remove its keys, keep the agent's keys in it.
			if nested := applyLauncherKeys(baseTable, liveTable, nil); len(nested) > 0 {
				result[key] = nested
			} else {
				delete(result, key)
			}
		default:
			delete(result, key)
		}
	}
	return result
}

func unionKeys(a, b map[string]any) map[string]struct{} {
	keys := make(map[string]struct{})
	for key := range a {
		keys[key] = struct{}{}
	}
	for key := range b {
		keys[key] = struct{}{}
	}
	return keys
}
