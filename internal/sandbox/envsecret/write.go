package envsecret

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	oneyaml "oneyaml.dev/go"

	"github.com/inoio/agents-sandbox/internal/atomicwrite"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configread"
	"github.com/inoio/agents-sandbox/internal/projectguard"
	"github.com/inoio/agents-sandbox/internal/yamlfmt"
)

// secretFileName is the structured secret file the writer edits, inside the
// directory the caller resolved (a config scope directory).
const secretFileName = configpaths.EnvSecretFileName + ".yaml"

// writeSecretFile is the durable writer the secret store edits through. Tests
// substitute it to exercise the write-failure path a full disk or a vanished
// directory otherwise produces.
var writeSecretFile = atomicwrite.WriteFileMode

// WriteSecretSpec merges one keyed secret spec into <dir>/env.secret.yaml,
// creating the file mode 0600 when absent and preserving every other key and
// comment. It never writes a spec with an empty value or with neither hosts nor
// an explicit dangerous flag (that spec would be dropped at VM build).
func WriteSecretSpec(dir, envVar string, spec SecretSpec) error {
	if reason, refused := refuseWrite(envVar, spec); refused {
		return fmt.Errorf("write secret %q: %s", envVar, reason)
	}
	document, existed, err := parseSecretDocument(dir)
	if err != nil {
		return err
	}
	if err := setEntry(document, envVar, secretSpecValue(spec)); err != nil {
		return fmt.Errorf("write secret %q in %s: %w", envVar, filepath.Join(dir, secretFileName), err)
	}
	return writeSecretDocument(dir, document, existed)
}

// RemoveSecretSpec deletes envVar from <dir>/env.secret.yaml, preserving the
// rest; a no-op when the file or key is absent.
func RemoveSecretSpec(dir, envVar string) error {
	document, existed, err := parseSecretDocument(dir)
	if err != nil {
		return err
	}
	if !existed {
		return nil
	}
	pointer, found, err := entryValuePointer(document, envVar)
	if err != nil {
		return fmt.Errorf("remove secret %q from %s: %w", envVar, filepath.Join(dir, secretFileName), err)
	}
	if !found {
		return nil
	}
	if err := document.Remove(pointer); err != nil {
		return fmt.Errorf("remove secret %q from %s: %w", envVar, filepath.Join(dir, secretFileName), err)
	}
	return writeSecretDocument(dir, document, existed)
}

// refuseWrite reports why a spec must not be persisted, or that it may be.
// BuildSecretsFromSpecs drops a spec with no value, or with neither hosts nor
// the explicit dangerous flag, so persisting one would record a secret the VM
// never provisions. An empty key is refused too: it is the file's YAML key, and
// the runtime refuses to boot an empty env name.
func refuseWrite(envVar string, spec SecretSpec) (string, bool) {
	switch {
	case envVar == "":
		return "environment variable name is empty", true
	case spec.Value == "":
		return "value is empty", true
	case spec.Host == "" && len(spec.Hosts) == 0 && !spec.AllowAnyHostDangerous:
		return "no hosts and no allow_any_host_dangerous; the spec would be dropped at VM build", true
	default:
		return "", false
	}
}

// secretSpecValue renders a spec as the mapping the YAML loader decodes into
// SecretSpec: the same field names, omitempty-style, so a written entry never
// carries a field the spec did not set.
func secretSpecValue(spec SecretSpec) map[string]any {
	value := map[string]any{"value": spec.Value}
	if spec.Host != "" {
		value["host"] = spec.Host
	}
	if len(spec.Hosts) > 0 {
		value["hosts"] = spec.Hosts
	}
	if len(spec.HeaderFields) > 0 {
		value["header_fields"] = spec.HeaderFields
	}
	if spec.AllowAnyHostDangerous {
		value["allow_any_host_dangerous"] = true
	}
	return value
}

// secretPath returns the file the writer edits in dir.
func secretPath(dir string) string {
	return filepath.Join(dir, secretFileName)
}

// parseSecretDocument parses dir's env.secret.yaml losslessly, so an edit keeps
// every other key, comment and style. An absent file yields an empty document
// and existed false. A file the loader cannot parse yields an error: editing it
// would silently drop content the launcher previously skipped with a warning.
func parseSecretDocument(dir string) (*oneyaml.Document, bool, error) {
	path := secretPath(dir)
	data, err := readSecretFileAt(path)
	switch {
	// A refusal wraps fs.ErrNotExist when the approved file has been deleted, so
	// it must be checked before the absent-file branch.
	case errors.Is(err, projectguard.ErrNotApproved):
		return nil, false, err
	case errors.Is(err, os.ErrNotExist):
		document, parseErr := oneyaml.Parse(nil, oneyaml.Untrusted())
		return document, false, parseErr
	case err != nil:
		return nil, false, err
	}
	document, err := oneyaml.Parse(data, oneyaml.Untrusted())
	if err != nil {
		return nil, false, yamlfmt.WrapErr(path, err)
	}
	return document, true, nil
}

// ValidateSecretFile reports whether dir's env.secret.yaml can be edited: it is
// absent, or it parses. It returns the parse or trust-gate error otherwise and
// writes nothing, so a caller that plans several writes can refuse a finding
// whose secret file cannot be edited before the first write runs.
func ValidateSecretFile(dir string) error {
	_, _, err := parseSecretDocument(dir)
	return err
}

// readSecretFileAt reads path through the project guard, verifying the approved
// bytes at the link path the guard keys its approval on; the read itself follows
// a symlink to its target, so a symlinked file is read at its target while
// verified as the approved link. A guard refusal and an absent file are both
// returned unwrapped and unmodified: parseSecretDocument tells them apart by
// errors.Is, and a refusal also wraps fs.ErrNotExist when the approved file has
// been deleted.
func readSecretFileAt(path string) ([]byte, error) {
	// The file is a project secret file, so the read is verified against the
	// approved content the trust gate installed.
	data, err := projectguard.ReadFile(path, configread.ReadFile)
	switch {
	case errors.Is(err, projectguard.ErrNotApproved), errors.Is(err, os.ErrNotExist):
		return data, err
	case err != nil:
		return nil, fmt.Errorf("read %s: %w", path, err)
	default:
		return data, nil
	}
}

// setEntry sets mapping key to value in document, replacing the entry of the
// same literal key when present and appending it otherwise.
func setEntry(document *oneyaml.Document, key string, value any) error {
	if !document.Root().IsValid() {
		return document.Set("", map[string]any{key: value})
	}
	if pointer, found, err := entryValuePointer(document, key); err != nil {
		return err
	} else if found {
		return document.Set(pointer, value)
	}
	return document.Set(oneyaml.Pointer(key), value)
}

// entryValuePointer returns the pointer of the mapping entry whose key is
// literally key, and whether that entry exists. The document's own pointer is
// compared against the entry's, so a key oneyaml escapes (a '/' or '~' becomes
// "~1"/"~0") is still the entry that a later write replaces.
func entryValuePointer(document *oneyaml.Document, key string) (string, bool, error) {
	for existingKey, existingValue := range document.Root().Entries() {
		if existingKey.Value() != key {
			continue
		}
		pointer, err := existingValue.Pointer()
		if err != nil {
			return "", false, err
		}
		return pointer, true, nil
	}
	return "", false, nil
}

// writeSecretDocument writes document to dir's env.secret.yaml atomically,
// creating the file mode 0600 when it did not exist and keeping an existing
// file's permissions. The write targets the link-resolved path, so a symlinked
// secret file is edited at its target rather than replaced by a regular file.
// The edited bytes are accepted as the approved project content, so a later
// read in the same command does not refuse the write.
func writeSecretDocument(dir string, document *oneyaml.Document, existed bool) error {
	path := secretPath(dir)
	target := resolvedSecretTarget(path)
	perm := os.FileMode(0o600)
	if existed {
		if info, err := os.Stat(target); err == nil {
			perm = info.Mode().Perm()
		}
	}
	edited := document.Bytes()
	if err := writeSecretFile(target, edited, perm); err != nil {
		return err
	}
	projectguard.Accept(path, edited)
	return nil
}

// resolvedSecretTarget returns path's link target when it is a symlink and that
// target resolves, and path itself otherwise. An absent file resolves to itself,
// which is where a created file belongs.
func resolvedSecretTarget(path string) string {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return target
}
