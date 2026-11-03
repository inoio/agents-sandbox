# Secret placeholders, the network proxy, and the model transport

Why an agent session dies once a microsandbox secret placeholder reaches the model context, and the
launcher's `secrets.common.allow-placeholder-for` escape hatch.

## The contract

A microsandbox secret's real value never enters the guest. The guest sees a placeholder — `$MSB_<ENV_VAR>`
by default, or `SecretEnvOptions.Placeholder` when set. The network proxy classifies each destination
host per secret (`SecretEnvOptions`):

- `Allow` — the proxy substitutes the real value and forwards the request.
- `AllowPlaceholderFor` (`SecretEntry.Passthrough`; the older `Passthrough` field is a deprecated alias) —
  the proxy forwards the **unchanged placeholder**.
- neither — a violation, and the sandbox-wide `SecretViolationAction` applies.

`Allow` and `Passthrough` must be disjoint; the SDK rejects "conflicting secret passthrough hosts".

The launcher does not set a `SecretViolationAction`, so msb's runtime default (block-and-log) applies.
`ViolationAction` has only blocking variants (`block`, `block-and-log`, `block-and-terminate`) and no
pass-through action, and `allow_any_host_dangerous` makes the proxy substitute on every host — i.e. it
leaks the real secret. There is no "just log it" escape.

## The trap

The agent's LLM transport POSTs the whole conversation — system prompt plus history, including tool
output and, when the harness round-trips it, reasoning — as the request **body** to the model server.
Once a placeholder is anywhere in that context, every subsequent completion request carries it to the
model host, which is not in that secret's `Allow`. The proxy blocks the request, so the turn never
reaches the model; history is append-only, so the block repeats every turn and the session cannot continue.

How the placeholder gets into context:

- Tool output: `env` / `printenv`, `cat` of a secret or config file, or reading another session's
  transcript. The launcher's own provisioning puts the placeholder in the guest environment, so a bare
  `env` is enough.
- The agent echoing it while reasoning about secrets.
- **Self-referential spread:** inspecting a broken session's transcript reads the placeholder into the
  inspector's context, so the inspector breaks the same way. Recovery must purge transcripts, not read them.
- **Reasoning is a channel.** If thinking tokens are replayed into the next request, the literal can be in
  the body even when every visible message is redacted; redacting prose is not sufficient.

## Observing it

A violation is a runtime diagnostic, not guest output. It lands in `runtime.log` inside the per-sandbox
log directory and is only surfaced when the `system` log source is requested:

```
<MSB_HOME>/sandboxes/<slug>-<agent>/logs/runtime.log
```

`<MSB_HOME>` is the launcher's private home (`configpaths.MSBHome()` →
`$XDG_DATA_HOME/agents-sandbox/msb`), and `<slug>-<agent>` is the project VM name from
`vm.projectVMName`. The SDK reads it via `Sandbox.Logs` / `Sandbox.LogStream` with
`LogSourceSystem`; the zero `LogOptions` reads only stdout/stderr, so a naive read misses it. The
launcher surfaces no sandbox logs at all — only detached-job logs through `Job.Logs`
(`vm.detached`).

The block-and-log line names the mechanism: `action=block-and-log`, `location=body`,
`secret_env_var=<name>`, and `host`/`sni` = the model server.

## The escape hatch

Set the model host as passthrough for every secret, so the proxy forwards the placeholder unchanged
instead of blocking it. The placeholder is inert, so this does not leak anything; it does mean the
placeholder reaches the model server and any upstream provider logs.

The launcher exposes it as the **config-file-only** `secrets.common.allow-placeholder-for` list
(`viperconfig.Config.Secrets.Common.AllowPlaceholderFor`, `env:"-"`, no flag, mirroring
`network.egress-allow`). `runoptions.Build` copies it to `options.RunOptions.AllowPlaceholderFor`;
`vm.loadEnvFingerprints` passes it to `envsecret.LoadEnvAndSecrets`, which hands it to
`BuildSecretsFromSpecs`. The list is transport-scoped (one declaration) because every secret's
placeholder traverses the same model connection; the launcher cannot derive the model endpoint, since
it does not parse agent config. The `secrets` namespace names the domain and leaves room for more
secret-global keys without a new top-level.

`BuildSecretsFromSpecs` fans the list into each `msbSdk.SecretEnvOptions{AllowPlaceholderFor: ...}`
via `placeholderHostsFor(allowHosts, allowPlaceholderFor)`, which enforces the invariants:

- A host in both `Allow` and the global list is dropped from that secret's passthrough (`Allow` wins,
  so the credential authenticating to the model host keeps substitution). The runtime rejects a host
  listed in both.
- An allow-any-host secret (`Allow: ["*"]`) gets no passthrough: it already substitutes everywhere.
- The result is deduplicated, sorted, and nil when empty, so an unset `secrets.common.allow-placeholder-for`
  does not change the built entry.
- `state.SecretsContentHash` already folds `SecretEntry.Passthrough`, so setting the key changes the
  digest and triggers exactly one VM recreate. The poisoned transcripts live in the home volume and
  survive the recreate, so the affected sessions start working again.
- This is a repair, not the root fix. The placeholder still reaches every other egress host (a `curl`,
  a `git push` body, an uploaded log) and blocks there. The root fix is keeping placeholders out of the
  model context at the boundary — redacting tool output and reasoning — which is harder and does not
  heal sessions already poisoned.

The per-secret alternative (`allow_placeholder_for` in `env.secret.yaml`, unioned with the common list
before the allow-exclusion) is deferred, not built: the model endpoint is the same for every secret, so
the single common list suffices today, and the `secrets.common` shape leaves room to add the per-secret
field later without moving the common key. Note that a legacy `KEY=value@host` line cannot carry it —
only the YAML format can.
