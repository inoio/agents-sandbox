package configmigration

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"github.com/inoio/agents-sandbox/internal/termio"
)

func Apply(plan *Plan, ui termio.UI) error {
	if plan == nil {
		return errors.New("nil migration plan")
	}
	secrets := cloneSecrets(plan.Secrets)
	if err := completeSecretHostsAtPath(secrets, plan.SecretPath, ui); err != nil {
		return err
	}
	secretData, err := migrationMergeSecretFile(plan.SecretPath, secrets)
	if err != nil {
		return err
	}
	if err := validatePlanOutputs(plan, secrets); err != nil {
		return err
	}
	outputs := manifestOutputs(plan, secretData)
	if err := writeMigrationManifest(migrationManifest{
		Version:    manifestVersion,
		State:      migrationStateApplying,
		Agent:      plan.AgentName,
		SourceHash: plan.SourceHash,
		Outputs:    outputs,
		Pending:    nil,
	}); err != nil {
		return fmt.Errorf("record migration state: %w", err)
	}
	for _, file := range plan.Files {
		if err := writeIfChanged(file.Path, file.Data, file.Mode); err != nil {
			return fmt.Errorf("write migrated file %s: %w", file.Path, err)
		}
	}
	if len(secrets) > 0 {
		if err := writeIfChanged(plan.SecretPath, secretData, defaultAuthMode); err != nil {
			return fmt.Errorf("write migrated secrets: %w", err)
		}
	}
	if len(plan.ConfigData) > 0 {
		if err := writeIfChanged(plan.ConfigPath, plan.ConfigData, defaultConfigMode); err != nil {
			return fmt.Errorf("write launcher config: %w", err)
		}
	}
	state := migrationStateCompleted
	var pending []pendingLogin
	if plan.HasLogin() {
		state = migrationStateLoginPending
		pending = pendingLoginsFromPlan(plan)
	}
	if err := writeMigrationManifest(migrationManifest{
		Version:    manifestVersion,
		State:      state,
		Agent:      plan.AgentName,
		SourceHash: plan.SourceHash,
		Outputs:    outputs,
		Pending:    pending,
	}); err != nil {
		return fmt.Errorf("complete migration state: %w", err)
	}
	return nil
}

func writeIfChanged(path string, data []byte, mode os.FileMode) error {
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	return atomicWriteFn(path, data, mode)
}
