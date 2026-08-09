package sandbox

import (
	"context"
	"fmt"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// reconcileEnvAndSecrets diffs the desired env and secrets against the applied
// (applied) fingerprint state and applies the changes via the SDK Modify API.
// It returns whether anything changed and was applied, plus the new fingerprint
// state for env and secrets. Callers must decide/handle the opencode daemon
// restart separately.
//
// desiredSecrets must be the output of BuildSecrets ([]msbSdk.SecretEntry) so
// the diff and the Modify specs are derived from exactly the same parsed
// entries — never re-parse the raw "VAR=value@host" strings here.
//
// Zero values of appliedEnv/appliedSecrets signal "no persisted state yet"
// (first run or state not yet loaded).
func reconcileEnvAndSecrets( //nolint:nonamedreturns // readability: clearer return documentation
	ctx context.Context,
	handle SandboxHandle,
	desiredEnv map[string]string,
	desiredSecrets []msbSdk.SecretEntry,
	appliedEnv EnvState,
	appliedSecrets SecretState,
) (changed bool, envState EnvState, secretState SecretState, err error) {
	if handle == nil {
		return false, envState, secretState, nil
	}

	envChanged := envChanged(appliedEnv, desiredEnv)
	secretChanged := secretsChanged(appliedSecrets, desiredSecrets)

	if !envChanged && !secretChanged {
		return false, appliedEnv, appliedSecrets, nil
	}

	var mo msbSdk.ModifyOptions
	mo.Policy = msbSdk.ModificationPolicyNoRestart

	if envChanged {
		applyEnvSpec(appliedEnv, desiredEnv, &mo)
	}

	if secretChanged {
		applySecretSpec(appliedSecrets, desiredSecrets, &mo)
	}

	if _, err := handle.Modify(ctx, mo); err != nil {
		return false, envState, secretState, fmt.Errorf(
			"modify env/secrets: %w", err,
		)
	}

	newEnv := appliedEnv
	if envChanged {
		newEnv = buildEnvState(desiredEnv)
	}
	newSecret := appliedSecrets
	if secretChanged {
		newSecret = buildSecretState(desiredSecrets)
	}
	return true, newEnv, newSecret, nil
}

// applyEnvSpec populates a ModifyOptions for the env portion: sets mo.Env to
// the desired map (nil means no env — no change) and appends to mo.EnvRemove
// each name from applied.Names that is not present in desired.
func applyEnvSpec(applied EnvState, desired map[string]string, mo *msbSdk.ModifyOptions) {
	// nil desired means "no env at all" (not "no change") — only apply if
	// the caller explicitly passes an empty map.  This avoids clobbering
	// existing env vars when the caller has no desired-env state yet.
	if desired == nil {
		return
	}
	mo.Env = desired

	for _, name := range applied.Names {
		if _, ok := desired[name]; !ok {
			mo.EnvRemove = append(mo.EnvRemove, name)
		}
	}
}

// applySecretSpec populates a ModifyOptions for the secrets portion: sets
// mo.Secrets to the secret modify specs derived from the desired entries, and
// appends to mo.SecretsRemove each name from applied.Names not present in the
// desired secret name set.
func applySecretSpec(applied SecretState, desired []msbSdk.SecretEntry, mo *msbSdk.ModifyOptions) {
	mo.Secrets = secretModifySpecsFromEntries(desired)

	wantNames := make(map[string]struct{}, len(desired))
	for _, e := range desired {
		wantNames[e.EnvVar] = struct{}{}
	}

	for _, name := range applied.Names {
		if _, ok := wantNames[name]; !ok {
			mo.SecretsRemove = append(mo.SecretsRemove, name)
		}
	}
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
