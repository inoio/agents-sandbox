package firstrun

import (
	"errors"
	"fmt"
	"path/filepath"
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
// Reporting is split by kind. An expected skip is silent: a file that is not a
// config format (provision rules select broadly, so a README or a script in the
// tree is not a failure), a value store (the dotenv, or a login store), or a
// config file holding no credential and no secret-shaped value — the last is
// copied whole. A file that is credential-bearing but cannot be sanitized —
// unreadable or over the read-back cap, a known format that does not decode, a
// credential with no env-var name or no host, a value that survives
// substitution, or a non-reference string under a secret-shaped key the
// credential walk did not name — is left unseeded, never copied raw or truncated,
// and named in the returned error while the readable files are still seeded. A
// config file the agent's merge never reads — its name is not matched by the
// agent's snippet pattern, so a managed copy would take no effect — is likewise
// skipped and named. A nil error therefore means every candidate was seeded or
// silently skipped.
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
	var unseedable, unmergeable []string
	for _, hostPath := range f.HostFiles {
		if readback.SkipsInlineCredentials(hostPath) {
			// The dotenv and the login stores are value stores: they are never
			// copied, so their raw values cannot reach the managed scope.
			continue
		}
		path := hostRelativePath(hostHome, hostPath)
		if _, known := configdecode.FormatFor(path); !known {
			// Not a config format: an expected skip, not a failure.
			continue
		}
		if !agent.MergesSnippetName(f.Agent, filepath.Base(hostPath)) {
			// The agent's merge never reads this snippet name, so a managed copy
			// of it would take no effect; skip and report it.
			unmergeable = append(unmergeable, hostPath)
			continue
		}
		if seeded, err := seedOneHostFile(f.Agent, path, hostPath, scopeDir, targetDir); err != nil {
			unseedable = append(unseedable, hostPath)
		} else {
			migrated += seeded
		}
	}
	if len(unseedable) > 0 || len(unmergeable) > 0 {
		return migrated, skippedSeedFilesError(unseedable, unmergeable)
	}
	return migrated, nil
}

// skippedSeedFilesError names the host files the seed left uncopied: the ones it
// could not sanitize, and the ones the agent's merge would never read. It is the
// one reporting point, so the caller's warning covers both reasons.
func skippedSeedFilesError(unseedable, unmergeable []string) error {
	var clauses []string
	if len(unseedable) > 0 {
		clauses = append(clauses, fmt.Sprintf(
			"skipped %d host config file(s) that could not be seeded: %s",
			len(unseedable),
			strings.Join(unseedable, ", "),
		))
	}
	if len(unmergeable) > 0 {
		clauses = append(clauses, fmt.Sprintf(
			"skipped %d host config file(s) the agent merges no snippet for: %s",
			len(unmergeable),
			strings.Join(unmergeable, ", "),
		))
	}
	return fmt.Errorf("firstrun: %s", strings.Join(clauses, "; "))
}

// seedOneHostFile seeds one host config file and returns the number of static
// secrets it migrated. It reads the file under the read-back cap, classifies it,
// substitutes every classified value, and refuses — with an error — a file it
// cannot sanitize, so a raw value never reaches the managed scope.
func seedOneHostFile(
	agentName, path, hostPath, scopeDir, targetDir string,
) (int, error) {
	content, ok := readback.ReadCappedFile(hostPath, readback.MaxHashedBytes)
	if !ok {
		return 0, fmt.Errorf("unreadable or over the %d-byte cap", readback.MaxHashedBytes)
	}
	// gomutants:disable-next-line BRANCH_IF reason="a known-format decode failure is refused identically downstream: SanitizeConfig retains the value and leaves the file unseeded either way"
	if _, _, err := readback.ClassifyConfigContent(path, content, agentName); err != nil {
		return 0, err
	}
	// The classification carries no value, so re-read the credentials to
	// substitute them in memory.
	credentials := readback.InlineCredentialsIn(path, content)
	placeholdered, ok := placeholderStaticValues(path, content, credentials)
	if !ok {
		return 0, errors.New("a credential could not be replaced by a placeholder")
	}
	if err := writeSeedSecrets(scopeDir, credentials); err != nil {
		return 0, err
	}
	if err := atomicwrite.WriteFileMode(
		filepath.Join(targetDir, filepath.Base(hostPath)),
		[]byte(placeholdered),
		managedConfigMode,
	); err != nil {
		return 0, fmt.Errorf("write managed config %s: %w", path, err)
	}
	return len(credentials), nil
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
// its ${ENVVAR} placeholder through the shared readback sanitizer, with the
// host-seed guard on: a non-reference string under a secret-shaped key the walk
// did not name (e.g. an MCP Authorization header, or an options.accessToken)
// refuses the file rather than let a raw value through. ok is false when a
// credential cannot be migrated — it has no env-var name, no value, or no host
// to scope it — or when the sanitizer refuses the content, so the caller leaves
// the file unseeded rather than copy a raw value or an orphan placeholder.
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
	return readback.SanitizeConfig(content, readback.SanitizeParams{
		Path:                       path,
		Credentials:                credentials,
		AllCredentials:             credentials,
		AllowedEnvVars:             nil,
		IndirectionKey:             "",
		RefuseUndeclaredSecretKeys: true,
	})
}
