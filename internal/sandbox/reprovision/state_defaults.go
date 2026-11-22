package reprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/atomicwrite"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// stateFileMode keeps an agent's state file private: it holds login metadata.
const stateFileMode = 0o600

// StateDefault is an agent.StateDefault at its VM path.
type StateDefault struct {
	VMPath string
	Keys   map[string]any
}

// stateDefaultsFor returns the agent's state defaults at their VM paths.
func stateDefaultsFor(a agent.Agent, vmHome string) []StateDefault {
	defaulter, ok := agent.AsStateDefaulter(a)
	if !ok {
		return nil
	}
	var defaults []StateDefault
	for _, stateDefault := range defaulter.StateDefaults() {
		defaults = append(
			defaults,
			StateDefault{VMPath: filepath.Join(vmHome, stateDefault.Path), Keys: stateDefault.Keys},
		)
	}
	return defaults
}

// mergeStateDefaults sets keys in the JSON state document content (empty when
// the file does not exist yet), keeping every other key and the exact numbers.
// It returns the merged document, whether it changed, and whether content was a
// JSON object: the agent's state file is never overwritten with a guess.
func mergeStateDefaults(content []byte, keys map[string]any) ([]byte, bool, bool) {
	document := map[string]any{}
	if len(bytes.TrimSpace(content)) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(content))
		decoder.UseNumber()
		// A JSON null decodes into a nil document: not an object either.
		if err := decoder.Decode(&document); err != nil || document == nil {
			return nil, false, false
		}
	}
	if !setKeys(document, keys) {
		return nil, false, true
	}
	// HTML escaping is off, so only the keys set here change in the file.
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	err := encoder.Encode(document)
	// gomutants:disable-next-line RETURN_TRUE reason="a document decoded from JSON always encodes again, so err is always nil"
	return encoded.Bytes(), true, err == nil
}

// setKeys sets every key of keys that document lacks, descending into tables,
// and reports whether anything changed.
func setKeys(document, keys map[string]any) bool {
	changed := false
	for key, value := range keys {
		table, isTable := value.(map[string]any)
		if !isTable {
			// A default fills a missing key only; a value the agent or the user
			// set, such as an explicit false, is kept.
			if _, present := document[key]; !present {
				document[key] = value
				changed = true
			}
			continue
		}
		// A new or replaced table counts as a change through the keys set in it.
		nested, isNested := document[key].(map[string]any)
		if !isNested {
			nested = map[string]any{}
			document[key] = nested
		}
		if setKeys(nested, table) {
			changed = true
		}
	}
	return changed
}

// stateTempSuffix names the temporary file a state default is written to before
// it replaces the state file, so the agent never reads a half-written file.
const stateTempSuffix = ".agents-sandbox.tmp"

// applyStateDefaultsInSandbox merges each default into its state file in the
// VM, writing only a file the merge changed, and returns what it created or
// wrote for the caller's chown. A state file that is not a JSON object, or that
// exists but cannot be read, is left alone: it holds the agent's login and
// history, and is never replaced on a guess. The merged file replaces the old
// one by a rename.
func applyStateDefaultsInSandbox(
	ctx context.Context, sb msb.SandboxExecutor, fs msb.SandboxFS, defaults []StateDefault,
) ([]string, error) {
	var written []string
	for _, stateDefault := range defaults {
		content, readable := readStateInSandbox(ctx, fs, stateDefault.VMPath)
		if !readable {
			continue
		}
		merged, changed, _ := mergeStateDefaults(content, stateDefault.Keys)
		if !changed {
			continue
		}
		made, err := mkdirAllFS(ctx, fs, filepath.Dir(stateDefault.VMPath))
		written = append(written, made...)
		if err != nil {
			return written, err
		}
		if err := replaceStateInSandbox(ctx, sb, fs, stateDefault.VMPath, merged); err != nil {
			return written, err
		}
		written = append(written, stateDefault.VMPath)
	}
	return written, nil
}

// readStateInSandbox returns a state file's content (nil when it does not
// exist), and false when its existence or content cannot be read.
func readStateInSandbox(ctx context.Context, fs msb.SandboxFS, path string) ([]byte, bool) {
	exists, err := fs.Exists(ctx, path)
	if err != nil {
		return nil, false
	}
	if !exists {
		return nil, true
	}
	content, err := fs.Read(ctx, path)
	return content, err == nil
}

// replaceStateInSandbox writes data beside path, makes it private, and renames
// it over path.
func replaceStateInSandbox(
	ctx context.Context,
	sb msb.SandboxExecutor,
	fs msb.SandboxFS,
	path string,
	data []byte,
) error {
	temporary := path + stateTempSuffix
	if err := fs.Write(ctx, temporary, data); err != nil {
		return fmt.Errorf("write agent state %s: %w", path, err)
	}
	if err := renamePrivate(ctx, sb, temporary, path); err != nil {
		_ = fs.Remove(ctx, temporary)
		return fmt.Errorf("replace agent state %s: %w", path, err)
	}
	return nil
}

// renamePrivate makes temporary private and renames it over path.
func renamePrivate(ctx context.Context, sb msb.SandboxExecutor, temporary, path string) error {
	if err := chmodFile(ctx, sb, temporary, stateFileMode); err != nil {
		return err
	}
	out, err := sb.Exec(ctx, "mv", []string{"-f", temporary, path}, msb.ExecRequest{User: rootUser})
	if err != nil {
		return err
	}
	if !out.Success() {
		return errors.New(strings.TrimSpace(out.Stderr()))
	}
	return nil
}

// materializeStateDefaults is applyStateDefaultsInSandbox for a host directory.
func materializeStateDefaults(defaults []StateDefault, vmHome, targetDir string) error {
	for _, stateDefault := range defaults {
		// A path outside the home maps to "", which reads as absent; the write
		// below reports it.
		dest, _ := targetPath(stateDefault.VMPath, vmHome, targetDir)
		content, err := os.ReadFile(dest)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			continue
		}
		merged, changed, _ := mergeStateDefaults(content, stateDefault.Keys)
		if !changed {
			continue
		}
		if err := atomicwrite.WriteFileMode(dest, merged, stateFileMode); err != nil {
			return fmt.Errorf("materialize agent state: %w", err)
		}
	}
	return nil
}
