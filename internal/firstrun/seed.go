package firstrun

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/atomicwrite"
	"github.com/inoio/agents-sandbox/internal/configdecode"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
)

// managedConfigMode is the mode of a seeded managed config file: it holds
// placeholders, not credentials, so it is ordinary configuration.
const managedConfigMode = 0o644

// SeedFromHost writes the host config into the per-slug user scope: each static
// credential value becomes a placeholder in the copied config and its value goes
// to env.secret.yaml; each login entry is dropped. It reads every value in
// memory only, threading it straight to the secret writer; the raw value never
// reaches the managed config. It returns the number of static secrets migrated.
//
// A host file the classifier cannot read (an unknown format, an undecodable
// document, or a static value it cannot scope) is left unseeded — never copied
// raw — and named in the returned error, while the readable files are still
// seeded. A nil error therefore means every host file was seeded whole.
func SeedFromHost(f Facts, paths configpaths.ConfigPaths, hostHome string) (int, error) {
	if f.Slug == "" {
		return 0, errors.New("firstrun: seed needs a repo slug for the per-slug scope")
	}
	profile, ok := agent.Lookup(f.Agent)
	if !ok {
		return 0, fmt.Errorf("firstrun: unknown agent %q", f.Agent)
	}
	dirNames, err := agent.ConfigDirNames(profile)
	if err != nil {
		return 0, fmt.Errorf("firstrun: agent %s config dir names: %w", f.Agent, err)
	}
	scopeDir := paths.UserSlugConfigDir(f.Slug)
	targetDir := configpaths.ConfigScope{
		Kind: configpaths.ScopePerSlugUser,
		Dir:  scopeDir,
	}.AgentConfigDir(dirNames[0])

	migrated := 0
	var skipped []string
	for _, hostPath := range f.HostFiles {
		if readback.SkipsInlineCredentials(hostPath) {
			// The dotenv and the login stores are value stores: they are never
			// copied, so their raw values cannot reach the managed scope.
			continue
		}
		path := hostRelativePath(hostHome, hostPath)
		content, ok := readback.ReadCappedFile(hostPath, readback.MaxHashedBytes)
		if !ok {
			skipped = append(skipped, hostPath)
			continue
		}
		static, login, err := readback.ClassifyConfigContent(path, content, f.Agent)
		if err != nil || len(login) > 0 {
			skipped = append(skipped, hostPath)
			continue
		}
		// The classification carries no value, so re-read the credentials to
		// substitute. A value the classification did not name would be copied
		// raw, so a count mismatch leaves the file unseeded.
		credentials := readback.InlineCredentialsIn(path, content)
		if len(credentials) != len(static) {
			skipped = append(skipped, hostPath)
			continue
		}
		placeholdered, ok := placeholderStaticValues(path, content, credentials)
		if !ok {
			skipped = append(skipped, hostPath)
			continue
		}
		if err := writeSeedSecrets(scopeDir, credentials); err != nil {
			return migrated, err
		}
		migrated += len(credentials)
		if err := atomicwrite.WriteFileMode(
			filepath.Join(targetDir, filepath.Base(hostPath)),
			[]byte(placeholdered),
			managedConfigMode,
		); err != nil {
			return migrated, fmt.Errorf("firstrun: write managed config %s: %w", path, err)
		}
	}
	if len(skipped) > 0 {
		return migrated, fmt.Errorf(
			"firstrun: skipped %d host config file(s) that could not be classified: %s",
			len(skipped),
			strings.Join(skipped, ", "),
		)
	}
	return migrated, nil
}

// hostRelativePath returns hostPath's path relative to hostHome, or its base
// name when it is not under hostHome. The classifier reads a home-relative path
// so a login store is recognized by its trailing elements.
func hostRelativePath(hostHome, hostPath string) string {
	rel, err := filepath.Rel(hostHome, hostPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.Base(hostPath)
	}
	return filepath.ToSlash(rel)
}

// writeSeedSecrets writes each static credential to the per-slug secret file
// through the shared secret writer, so the value lands exactly where the VM
// build reads it. The value is never logged; only env.secret.yaml holds it.
func writeSeedSecrets(dir string, credentials []readback.Credential) error {
	for _, credential := range credentials {
		err := envsecret.WriteSecretSpec(dir, credential.EnvVar, envsecret.SecretSpec{
			Value:       credential.Value,
			Hosts:       credential.Hosts,
			Placeholder: envsecret.PlaceholderFor(credential.EnvVar),
		})
		if err != nil {
			return fmt.Errorf("firstrun: write secret %s: %w", credential.EnvVar, err)
		}
	}
	return nil
}

// placeholderStaticValues replaces every static credential value in content with
// its ${ENVVAR} placeholder. ok is false when a credential cannot be migrated —
// it has no env-var name to reference, no value, or no host to scope it — or
// when a value survives the substitution, so the caller leaves the file
// unseeded rather than copy a raw value. Substitution is longest value first,
// so a value that is a prefix of another does not corrupt the longer one.
func placeholderStaticValues(
	path string,
	content []byte,
	credentials []readback.Credential,
) (string, bool) {
	for _, credential := range credentials {
		if credential.EnvVar == "" || credential.Value == "" || len(credential.Hosts) == 0 {
			return "", false
		}
	}
	ordered := slices.Clone(credentials)
	slices.SortStableFunc(ordered, func(a, b readback.Credential) int {
		return len(b.Value) - len(a.Value)
	})
	placeholdered := string(content)
	for _, credential := range ordered {
		placeholdered = strings.ReplaceAll(
			placeholdered,
			credential.Value,
			envsecret.PlaceholderFor(credential.EnvVar),
		)
	}
	if retainsStaticValue(path, placeholdered, credentials) {
		return "", false
	}
	return placeholdered, true
}

// retainsStaticValue reports whether content may still hold any of the
// credentials' values after substitution. It tests the literal content and,
// when the path's format decodes, every string the decoded document holds: a
// config file may store a value in its escaped form, which a literal search
// misses even though the harness reads the value. A known format that no longer
// parses is reported as retaining a value, since the decoded walk cannot run.
func retainsStaticValue(path, content string, credentials []readback.Credential) bool {
	for _, credential := range credentials {
		if credential.Value != "" && strings.Contains(content, credential.Value) {
			return true
		}
	}
	format, ok := configdecode.FormatFor(path)
	if !ok {
		return false
	}
	decoded, err := configdecode.Decode(format, []byte(content))
	if err != nil {
		return true
	}
	return decodedRetainsValue(decoded, credentials)
}

// decodedRetainsValue reports whether any string a decoded document holds — a
// key or a leaf value — contains one of the credentials' values.
func decodedRetainsValue(node any, credentials []readback.Credential) bool {
	switch typed := node.(type) {
	case map[string]any:
		for key, child := range typed {
			if containsCredentialValue(key, credentials) || decodedRetainsValue(child, credentials) {
				return true
			}
		}
	case map[any]any:
		for _, child := range typed {
			if decodedRetainsValue(child, credentials) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if decodedRetainsValue(child, credentials) {
				return true
			}
		}
	case string:
		return containsCredentialValue(typed, credentials)
	}
	return false
}

// containsCredentialValue reports whether text holds any credential's value.
func containsCredentialValue(text string, credentials []readback.Credential) bool {
	for _, credential := range credentials {
		if credential.Value != "" && strings.Contains(text, credential.Value) {
			return true
		}
	}
	return false
}
