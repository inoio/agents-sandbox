package sandbox

import (
	"context"
	"fmt"
	"maps"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// envMapsEqual reports whether two env maps hold the same key/value pairs.
func envMapsEqual(have, want map[string]string) bool {
	if have == nil {
		have = map[string]string{}
	}
	return maps.Equal(have, want)
}

// reconcileEnvAndSecrets diffs the desired env and secrets against the VM's
// current config and applies the changes via the SDK Modify API (for future
// execs). It returns whether anything changed and was applied. Callers must
// decide/handle the opencode daemon restart separately.
//
// desiredSecrets must be the output of BuildSecrets ([]msbSdk.SecretEntry) so
// the diff and the Modify specs are derived from exactly the same parsed
// entries — never re-parse the raw "VAR=value@host" strings here.
func reconcileEnvAndSecrets(
	ctx context.Context,
	handle SandboxHandle,
	desiredEnv map[string]string,
	desiredSecrets []msbSdk.SecretEntry,
) (bool, error) {
	if handle == nil {
		return false, nil
	}
	cfg, err := handle.Config()
	if err != nil || cfg == nil {
		return false, nil //nolint:nilerr // config read failure treated as no-op
	}
	changed := false
	var mo msbSdk.ModifyOptions
	mo.Policy = msbSdk.ModificationPolicyNoRestart

	if desiredEnv != nil && !envMapsEqual(cfg.Env, desiredEnv) {
		mo.Env = desiredEnv
		for k := range cfg.Env {
			if _, ok := desiredEnv[k]; !ok {
				mo.EnvRemove = append(mo.EnvRemove, k)
			}
		}
		changed = true
	}

	wantSecrets := parseSecretEntries(desiredSecrets)
	if !secretsNameSetEqual(cfg.Secrets, wantSecrets) {
		mo.Secrets = secretModifySpecsFromEntries(desiredSecrets)
		for _, s := range cfg.Secrets {
			if _, ok := wantSecrets[s.EnvVar]; !ok {
				mo.SecretsRemove = append(mo.SecretsRemove, s.EnvVar)
			}
		}
		changed = true
	}

	if !changed {
		return false, nil
	}
	if _, err := handle.Modify(ctx, mo); err != nil {
		return false, fmt.Errorf("modify env/secrets: %w", err)
	}
	return true, nil
}

// parseSecretEntries indexes SecretEntry values by their EnvVar name, which is
// the sandbox-visible env-variable name (mirrors BuildSecrets, which keys
// secrets by env var name).
func parseSecretEntries(entries []msbSdk.SecretEntry) map[string]msbSdk.SecretEntry {
	byName := make(map[string]msbSdk.SecretEntry, len(entries))
	for _, s := range entries {
		byName[s.EnvVar] = s
	}
	return byName
}

// secretsNameSetEqual reports whether the env-var names configured in the VM
// (have) equal the env-var names of the desired secrets (want). Values are
// compared by presence only, matching BuildSecrets which keys secrets by env
// var name.
func secretsNameSetEqual(have []msbSdk.SecretEntry, want map[string]msbSdk.SecretEntry) bool {
	if len(want) == 0 {
		return len(have) == 0
	}
	if len(have) != len(want) {
		return false
	}
	for _, s := range have {
		if _, ok := want[s.EnvVar]; !ok {
			return false
		}
	}
	return true
}

// secretModifySpecsFromEntries converts the desired secrets (as built by
// BuildSecrets) into the SDK ModifyOptions.Secrets shape, mapping the sandbox
// env-var name to a SecretModifySpec that re-supplies the same material and
// allowed hosts. No re-parsing of raw strings happens here.
func secretModifySpecsFromEntries(desiredSecrets []msbSdk.SecretEntry) map[string]msbSdk.SecretModifySpec {
	specs := make(map[string]msbSdk.SecretModifySpec, len(desiredSecrets))
	for _, s := range desiredSecrets {
		specs[s.EnvVar] = msbSdk.SecretModifySpec{ //nolint:exhaustruct // only Value/AllowHosts needed for Modify
			Value:        s.Value,
			AllowedHosts: s.AllowHosts,
		}
	}
	return specs
}
