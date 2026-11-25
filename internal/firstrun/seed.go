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
// skipped and named.
//
// It then plans the agent's instruction files from the host again and copies
// them verbatim to the same relative path under the managed agent config dir,
// which the config-dir mirror carries into the VM (see seedInstructions). An
// instruction file with a secret-shaped line, a skill with a non-Markdown file,
// a symlink leaving the home, and the files past the seed cap are skipped and
// reported. A nil error therefore means every candidate was seeded or silently
// skipped.
func SeedFromHost(f Facts, paths configpaths.ConfigPaths, hostHome string) (SeedResult, error) {
	if f.Slug == "" {
		return SeedResult{}, errors.New("firstrun: seed needs a repo slug for the per-slug scope")
	}
	profile, ok := agent.Lookup(f.Agent)
	if !ok {
		return SeedResult{}, fmt.Errorf("firstrun: unknown agent %q", f.Agent)
	}
	dirNames, err := agent.ConfigDirNames(profile)
	if err != nil {
		return SeedResult{}, fmt.Errorf("firstrun: agent %s config dir names: %w", f.Agent, err)
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
	instructions := seedInstructions(profile, hostHome, targetDir)
	skips := seedSkips{
		unseedable:              append(unseedable, instructions.unseedable...),
		unmergeable:             unmergeable,
		secretShapedInstruction: instructions.secretShaped,
		scriptedSkills:          instructions.scriptedSkills,
		escapingLinks:           instructions.escapingLinks,
		overCapInstructions:     instructions.overCap,
	}
	result := SeedResult{Secrets: migrated, InstructionFiles: instructions.copied, InstructionDir: targetDir}
	return result, skips.err()
}

// SeedResult is what SeedFromHost wrote: the number of static secrets it
// migrated to env.secret.yaml, and the number of instruction files it copied
// into InstructionDir, the per-slug managed agent config dir.
type SeedResult struct {
	Secrets          int
	InstructionFiles int
	InstructionDir   string
}

// seedSkips are the host files the seed left uncopied, by reason.
type seedSkips struct {
	unseedable              []string
	unmergeable             []string
	secretShapedInstruction []string
	scriptedSkills          []string
	escapingLinks           []string
	overCapInstructions     int
}

// err names what the seed left uncopied: the host files it could not sanitize,
// read, or write, the config files the agent's merge would never read, the
// instruction files that look like they hold a secret, the skills with
// non-Markdown files, the symlinks leaving the home, and the number of
// instruction files past the seed cap. It is the one reporting point, so the
// caller's warning covers every reason; nil when nothing was skipped.
func (s seedSkips) err() error {
	var clauses []string
	addList := func(format string, paths []string) {
		if len(paths) > 0 {
			clauses = append(clauses, fmt.Sprintf(format, len(paths), truncatedList(paths)))
		}
	}
	addList("skipped %d host file(s) that could not be seeded: %s", s.unseedable)
	addList("skipped %d host config file(s) the agent merges no snippet for: %s", s.unmergeable)
	addList("skipped %d instruction file(s) that look like they hold a secret: %s", s.secretShapedInstruction)
	addList("skipped %d skill(s) with non-Markdown files; use the drop-in or copy them yourself: %s",
		s.scriptedSkills)
	addList("skipped %d symlink(s) that resolve outside your home: %s", s.escapingLinks)
	if s.overCapInstructions > 0 {
		clauses = append(clauses, fmt.Sprintf(
			"skipped %d instruction file(s) over the seed cap of %d files and %d bytes",
			s.overCapInstructions,
			maxSeedInstructionFiles,
			maxSeedInstructionBytes,
		))
	}
	if len(clauses) == 0 {
		return nil
	}
	return fmt.Errorf("firstrun: %s", strings.Join(clauses, "; "))
}

// maxListedSkips is how many paths one skip clause names before it only counts
// the rest, so a large refused tree does not flood the warning.
const maxListedSkips = 10

// truncatedList joins paths, naming at most maxListedSkips and counting the
// rest ("and N more").
func truncatedList(paths []string) string {
	if len(paths) <= maxListedSkips {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s, and %d more",
		strings.Join(paths[:maxListedSkips], ", "), len(paths)-maxListedSkips)
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
	placeholdered, ok := placeholderStaticValues(path, content, credentials, nativeEnvIndirectionKey(agentName))
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

// nativeEnvIndirectionKey returns the config key the agent's harness reads an
// environment variable's name from, for a harness that does not expand ${VAR} in
// its config (kimi's api_key_env, codex's env_key), or "" for one that passes a
// config value through verbatim. The seed writes the same reference form the
// after-run write-back does.
func nativeEnvIndirectionKey(agentName string) string {
	// An unknown agent looks up as nil, which declares no indirection either.
	profile, _ := agent.Lookup(agentName)
	describer, ok := agent.AsEnvIndirectionDescriber(profile)
	if !ok {
		return ""
	}
	return describer.EnvIndirectionKey()
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
// credential cannot be migrated — it has no value, or no host to scope it; an
// absent env-var name is refused identically by the sanitizer downstream — or
// when the sanitizer refuses the content, so the caller leaves the file unseeded
// rather than copy a raw value or an orphan placeholder.
func placeholderStaticValues(
	path string,
	content []byte,
	credentials []readback.Credential,
	indirectionKey string,
) (string, bool) {
	for _, credential := range credentials {
		if credential.Value == "" || len(credential.Hosts) == 0 {
			return "", false
		}
	}
	return readback.SanitizeConfig(content, readback.SanitizeParams{
		Path:                       path,
		Credentials:                credentials,
		AllCredentials:             credentials,
		AllowedEnvVars:             nil,
		IndirectionKey:             indirectionKey,
		RefuseUndeclaredSecretKeys: true,
	})
}
