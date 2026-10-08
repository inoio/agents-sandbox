package state

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
)

// EnvContentHash returns a SHA-256 hex digest of the env map contents.
// Keys are sorted and hashed as "K=V" lines for order-independence.
func EnvContentHash(env map[string]string) string {
	if env == nil {
		env = map[string]string{}
	}
	lines := make([]string, 0, len(env))
	for k, v := range env {
		lines = append(lines, k+"="+v)
	}
	sort.Strings(lines)
	h := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(h[:])
}

// SecretsContentHash returns a SHA-256 hex digest of the secret entries.
// Entries are sorted by EnvVar and hashed as a stable set of field values.
// Every field that microsandbox bakes into
// the VM at creation is included so a change to any of them (not just the
// value) triggers the VM recreate that applies it.
func SecretsContentHash(entries []msbSdk.SecretEntry) string {
	if entries == nil {
		entries = []msbSdk.SecretEntry{}
	}
	byEnv := make(map[string]msbSdk.SecretEntry, len(entries))
	for _, e := range entries {
		byEnv[e.EnvVar] = e
	}
	envVars := make([]string, 0, len(byEnv))
	for k := range byEnv {
		envVars = append(envVars, k)
	}
	sort.Strings(envVars)
	var b strings.Builder
	for _, k := range envVars {
		e := byEnv[k]
		tls := ""
		if e.RequireTLSIdentity != nil {
			tls = strconv.FormatBool(*e.RequireTLSIdentity)
		}
		headers := ""
		if e.Substitution.Headers != nil {
			headers = strconv.FormatBool(*e.Substitution.Headers)
		}
		fmt.Fprintf(&b, "%s=%s|%s|%s|%s|%s|%s|%t|%t|%s\n",
			k,
			e.Value,
			strings.Join(e.Allow, ","),
			strings.Join(e.Passthrough, ","),
			e.Placeholder,
			tls,
			headers,
			e.Substitution.Query,
			e.Substitution.Body,
			string(e.ViolationAction),
		)
	}
	h := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(h[:])
}

// EnvChanged reports whether the applied env state differs from the desired
// env map, comparing content hashes for a stable order-independent result.
// A zero-value appliedEnv indicates "no persisted state yet" (first run),
// which is considered "changed" only when the desired map is non-empty.
func EnvChanged(applied EnvState, desired map[string]string) bool {
	if applied.Hash == "" {
		return len(desired) > 0
	}
	return applied.Hash != EnvContentHash(desired)
}

// SecretsChanged reports whether the applied secret state differs from the
// desired secret entries, comparing content hashes.
// A zero-value appliedSecrets indicates "no persisted state yet" (first run),
// which is only considered "changed" when the desired slice is non-empty.
func SecretsChanged(applied SecretState, desired []msbSdk.SecretEntry) bool {
	if applied.Hash == "" {
		if desired == nil {
			return false
		}
		return len(desired) > 0
	}
	return applied.Hash != SecretsContentHash(desired)
}

// BuildEnvState computes the content hash and sorted name list for the env map.
func BuildEnvState(desired map[string]string) EnvState {
	names := make([]string, 0, len(desired))
	for k := range desired {
		names = append(names, k)
	}
	sort.Strings(names)
	return EnvState{
		Hash:  EnvContentHash(desired),
		Names: names,
	}
}

// BuildSecretState computes the content hash and sorted name list for the secret entries.
func BuildSecretState(desired []msbSdk.SecretEntry) SecretState {
	byEnv := make(map[string]msbSdk.SecretEntry, len(desired))
	for _, e := range desired {
		byEnv[e.EnvVar] = e
	}
	names := make([]string, 0, len(byEnv))
	for k := range byEnv {
		names = append(names, k)
	}
	sort.Strings(names)
	return SecretState{
		Hash:  SecretsContentHash(desired),
		Names: names,
	}
}

// NetworkChanged reports whether the applied network state differs from the
// desired network policy, comparing content fingerprints. A zero-value applied
// state indicates "no persisted state yet" (first run or a VM created before
// network policies existed), so an existing VM must be recreated to apply the
// secure default.
func NetworkChanged(applied NetworkState, desired network.Policy) bool {
	if applied.Hash == "" {
		return true
	}
	return applied.Hash != desired.Fingerprint()
}

// BuildNetworkState computes the fingerprint for the desired network policy.
func BuildNetworkState(desired network.Policy) NetworkState {
	desired = desired.Effective()
	return NetworkState{
		Hash:  desired.Fingerprint(),
		Names: []string{string(desired.Profile)},
	}
}

// MountsChanged reports whether the applied mount state differs from the
// desired host bind mounts, comparing content fingerprints. The comparison is
// fingerprint-based because the microsandbox SDK does not round-trip volumes
// when reading back an existing VM, so the live config cannot be inspected. A
// zero-value applied state indicates "no persisted state yet" (first run or a
// VM created before mounts existed); it is only considered "changed" when
// mounts are actually configured.
func MountsChanged(applied MountState, desired mounts.Mounts) bool {
	if applied.Hash == "" {
		return len(desired) > 0
	}
	return applied.Hash != mounts.Fingerprint(desired)
}

// BuildMountState computes the fingerprint for the desired host bind mounts.
func BuildMountState(desired mounts.Mounts) MountState {
	return MountState{
		Hash:  mounts.Fingerprint(desired),
		Names: mounts.MountTargets(desired),
	}
}
