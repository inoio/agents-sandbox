---
description: Contract for the launcher config-key registry: the env-var and CLI-flag key sets are derived from the Config struct's mapstructure tags instead of hand-maintained parallel lists, env:"-" marks a config-file-only key, and a bound flag's default type comes from the owning field's Go kind.
read_if: Adding or changing a launcher config key, its env var, or its CLI flag; or reasoning about why an env var is silently ignored, why a config-file-only key has no env var, or where flagTypedDefault's type comes from.
---

# Config-key registry

A launcher config key used to have to be added to four places by hand: the `Config` struct, a flag-key list,
an env-key list, and `flagTypedDefault`'s type switch. Omitting a key from an env list or mistyping its
default silently ignored the env/config value, so `internal/viperconfig/configkeys.go` is now the registry.

## One source: the struct tags

`deriveConfigKeys()` reflects over `Config` and its nested config structs (in field order), yielding each
`mapstructure`-tagged key (an unexported field contributes none, whatever its tags):

- `configEnvKeys` = the derived keys + `KeyDind`, bound to `AGENTS_SANDBOX_<KEY>` (and the deprecated
  `OPENCODE_SANDBOX_<KEY>` alias). `dind` is the one env-bound key with **no** `Config` field: it is the
  deprecated alias for `docker` (see [deprecation policy](../conventions/deprecation-policy.md)).
- `configBindKeys` = the derived keys offered to `bindConfigFlags`, which looks each key's flag up by name and
  skips keys with no flag. A key therefore never has to declare that it is a flag, and `dind` is deliberately
  absent — binding `--dind` to viper would make `IsSet("dind")` always true and turn `resolveDockerAlias` into
  a spurious dind deprecation warning.
- The one name exception is `ports`, whose flag is registered as `--port`/`-p` (matching msb's own flag) rather
  than a hypothetical `--ports`: `configFlagAliases` maps the key to the flag name so `bindConfigFlags` binds it
  and the flag layer reaches `validate()`. An aliased key gets no `SetDefault` — an unset StringSlice flag's
  default is the raw string `"[]"`, which would make `IsSet` always true and leak a bogus entry into the
  resolved config; viper consults an unchanged bound flag only as a last resort, so the binding alone suffices.
- A bound flag's default type comes from the owning field's Go kind (`bool` → bool, `uint8` → uint8, otherwise
  the flag's raw string), not from the pflag: viper's flag-default fallback returns a raw string for every
  pflag type it does not cast, `uint8` among them.

## env:"-" means config-file-only

An exported field tagged `env:"-"` is omitted from every derived list: it has no env var and no CLI flag and is
set in the config file only. Today that is `network.Policy.EgressAllow`/`EgressDeny`
(see [network profiles](../network-and-secrets/network-profiles.md) and `docs/configuration/networking.md`) and
`SecretCommon.AllowPlaceholderFor`. A field tagged `mapstructure:"-"` is decoded outside viper (mounts, home,
notify) and is likewise skipped.

This is enforced by **not** enabling viper's `AutomaticEnv`: `NewResolver` binds an environment variable
explicitly for every key in `envKeys` — the derived set plus the hand-listed `notify.*` keys (whose `Notify`
field is decoded outside viper, so the walk cannot see them) — to `AGENTS_SANDBOX_<KEY>` and the deprecated
`OPENCODE_SANDBOX_<KEY>` alias, canonical first. Every other key therefore has no env var at all, so `env:"-"`
is sufficient on its own. `AutomaticEnv` would instead resolve *any* key, letting an `AGENTS_SANDBOX_<KEY>`
variable override a config-file-only key whenever a config file sets it.

The bare `notify` key is deliberately absent from `envKeys` (and stays unbound): it would collide with the
`notify.*` keys in viper, and `runoptions` reads `AGENTS_SANDBOX_NOTIFY`/`OPENCODE_SANDBOX_NOTIFY` directly.

## Tests

`internal/viperconfig/configkeys_test.go` pins the env-key set as a golden list (a dropped or added key fails
there), asserts `dind` is absent from the bind list, asserts the egress lists stay out of `envKeys` and win over
their env var when a config file sets them too, and pins the struct-derived flag-default types.
`walkConfigKeys` is also asserted directly against a synthetic type, because `Config` itself has no untagged
exported field and no tagged unexported field for the walk's two skip guards to act on.
`TestResolverNotifyDottedEnvVars`/`TestResolverNotifyLegacyEnvVars` cover the explicitly bound `notify.*` keys.
