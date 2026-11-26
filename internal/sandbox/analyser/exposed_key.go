package analyser

import (
	"fmt"
	"path"
	"strings"

	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
)

// exposedKeyFindings proposes, for each login store that holds a plain-text
// API key, to move the key into an env secret.
func exposedKeyFindings(exposed []readback.ExposedKey) []Finding {
	findings := make([]Finding, 0, len(exposed))
	for _, key := range exposed {
		findings = append(findings, exposedKeyFinding(key))
	}
	return findings
}

// exposedKeyFinding moves one store's key into the env secret that replaces
// it. For a harness that reads the key from its store, the store is
// provisioned from the per-slug scope with the secret's placeholder (keep-login,
// so a renewing login later made in the VM is left alone); it replaces the
// plain-text key at the next start and survives a home-volume reset. Both
// writes are applied together: a placeholder without its secret would break
// the harness. Learn mode never writes into the home volume itself.
func exposedKeyFinding(key readback.ExposedKey) Finding {
	//exhaustruct:ignore // a store report names no denials, count, or header fields
	evidence := Evidence{
		Summary:      "An API key is stored in plain text in ~/" + key.Source + " on the home volume.",
		Destinations: key.Hosts,
		Channel:      evidenceChannelReadback,
	}
	//exhaustruct:ignore // a secret write names no config key and no removal
	secret := Write{
		Scope: ScopePerSlugUser,
		//exhaustruct:ignore // a proposal never removes; the read-back resolves the value
		Secret: &SecretWrite{EnvVar: key.EnvVar, Provider: key.Provider, Hosts: key.Hosts},
		Text: "Keep it on the host: " + key.EnvVar + " becomes an env secret for " +
			strings.Join(key.Hosts, ", ") + ", in the per-slug user config (every clone of the remote).",
	}
	writes := []Write{secret}
	if key.ProvisionedStore != "" {
		writes = append(writes, provisionedStoreWrite(key))
	}
	if key.FollowUp != "" {
		//exhaustruct:ignore // a text-only write makes no machine change
		writes = append(writes, Write{Scope: ScopePerSlugUser, Text: key.FollowUp})
	}
	//exhaustruct:ignore // EvidenceHash is stamped by ObservationFindings
	return Finding{Evidence: evidence, Writes: writes, Kind: KindObservation}
}

// provisionedStoreWrite provisions the store with the secret's placeholder in
// place of the key.
func provisionedStoreWrite(key readback.ExposedKey) Write {
	//exhaustruct:ignore // a home-file write names no config key or secret
	return Write{
		Scope: ScopePerSlugUser,
		HomeFile: &HomeFileWrite{
			Target:  key.Source,
			Name:    provisionedStoreName(key.Source),
			Content: fmt.Appendf(nil, key.ProvisionedStore, envsecret.PlaceholderFor(key.EnvVar)),
		},
		Text: "~/" + key.Source + " is provisioned from the host with the secret's placeholder, " +
			"replacing the plain-text key at the next start; a login you make there later is kept.",
	}
}

// provisionedStoreName names the store's host copy after its home path, so two
// stores cannot collide: .codex/auth.json becomes codex-auth.json.
func provisionedStoreName(source string) string {
	return strings.TrimPrefix(strings.ReplaceAll(path.Clean(source), "/", "-"), ".")
}
