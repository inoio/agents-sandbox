---
description: The internal/configmigration package - safe migration of native agent config/credentials into agents-sandbox config, guided onboarding, and network-plan derivation.
read_if: Working in internal/configmigration (migration build/apply/review, guided onboarding, launcher network policy, credential redaction).
---

# configmigration

## Coverage

- `internal/configmigration/`

## Architecture

- Migrates supported native agent credentials/config into agents-sandbox managed config without copying raw credentials into the VM. Exported API: `Build`, `Review`, `Apply`, `Report`, `Guide`, `Dismiss`, `EnableNativeProvisioning`.
- Flow: `Build` plans changes host-side (no VM contact), `Review` uses `termio.UI` to confirm egress/secrets, `Apply` writes manifest/files/secrets atomically; a manifest tracks per-agent migration state (completed/dismissed/applying).
- Agent-specific behavior is data-driven via `agent.ConfigMigrationSpec` / `AuthMigrationSpec` (`agent.AsMigrationSpecProvider`); the package has package-level test seams (mutable function vars) restored by tests.
- The package was split from one `persistence.go` for focused ownership; complexity suppressions (`funlen`/`gocognit`/`gocyclo`/`cyclop`) are not permitted in this package - decompose instead.

## `internal/configmigration` Index

- `build.go` — `Build` orchestration plus `applyRequiredNetworkPlan`, `applyAuthMigration`, credential-file helpers; `newMigrationPlan`, `migrationProviderSpec`, `applyNativeConfigMigration`, `applyLauncherPlan`, `finalizeMigrationPlan`.
- `review.go` — `Review`/`Report`/`Dismiss`, Claude auth review, secret-host completion, output validation/hashing; review-flow helpers.
- `apply.go` — `Apply` (atomic manifest + file/secret writes) and `writeIfChanged`.
- `guide.go` — `Guide`/`guideSetup`/`reviewApplyAndConfirmStart`/`dismissGuidedMigration`; native file collection and `nativeAgentPaths` resolution.
- `network_plan.go` — launcher-config egress planning (`updateLauncherNetwork`, `plannedNetworkHosts`, `projectNetworkDenyHosts`, `buildLauncherConfigForPlan`, `migrationHomeMapping`).
- `store.go` — migration manifest/status types and persistence, secret-file merge, launcher config load, `EnableNativeProvisioning`.
- `migration.go` — plan/file/secret types, error vars, onboarding choice constants.
- `auth.go` — auth redaction engine (`sanitizeAuth*`, `replaceAuthField`, `authHosts`, field classification, `secretPart`/`knownHost`).
- `native_config.go` — native config migration and redaction (`migrateNativeConfig*`, `sanitizeConfigMap*`, Claude endpoint handling, endpoint-host inference, `mergePlannedSecrets`).
- `spec.go` — migration spec lookup (`defaultMigrationSpec`/`lookupMigrationSpec` + test seam), `buildLauncherConfig`, `shortHash`, `cloneSecrets`.
- `launcher_config.go` — parsed launcher config (`launcherConfig`) that preserves YAML comments on edit.
- `persistence.go` — residual package constants, test-seam globals, `migrationHomeMappings`.
