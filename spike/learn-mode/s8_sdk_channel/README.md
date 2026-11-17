# s8 — SDK channel semantics probe (host-only, throwaway)

Settles the four items the specs pin on the S1 spike
([`plans/2026-11-17-learn-mode-sdk-semantics.md`](../../../.operator-shared/plans/2026-11-17-learn-mode-sdk-semantics.md)):

| Case | Question | Verdict reads as |
|---|---|---|
| `q0-allow` | does a plain-domain allow rule authorize a non-intercepted connection, strict on? | `allowed` / `denied` |
| `q0o-allow-strict-off` | same, strict off | compare with `q0` |
| `q1-topology` | T1 vs T2 | `endpoint=real` ⇒ the runtime re-originated to our proxy (T2) |
| `q2-off` | does `TLS = nil` disable interception? | `endpoint=blocked` ⇒ re-enabled; `endpoint=leaked` ⇒ off honoured |
| `q3-composition` | HTTP CONNECT proxy reachable under `none`, strict on | `endpoint=connect-only` ⇒ yes |
| `q3o-composition-strict-off` | same, strict off | compare with `q3` |
| `q3s-composition-socks5` | SOCKS5 proxy reachable under `none`, strict on | `endpoint=connect-only` ⇒ yes |
| `q3so-composition-socks5-strict-off` | same, strict off | compare with `q3s` |
| `q4-bypass` | bypassed + allowlisted host, strict on | `endpoint=direct` ⇒ passes; `direct-blocked` ⇒ fail-closed |
| `q4o-bypass-strict-off` | same, strict off | compare with `q4` |

The `o` variants turn the runtime's default strict-hostname mode off
(`DisableStrict`), matching the old spike's `none` cases
(`--net-strict=false`); they exist to separate "the allow rule does not match"
from "strict mode refuses it".

`q2` uses **no proxy**: `TLS` is nil and the secret's `Allow` excludes the
requested host, so if the runtime re-enables interception it sees the
non-allowed placeholder and blocks it (`blocked`); if `off` is honoured nothing
intercepts and the placeholder reaches the host unsubstituted (`leaked`). This
keeps the answer independent of our own CA trust.

Each verdict line is followed by a diagnostic block — the guest's own output
(including its SSL env and `/.msb/tls`), the runtime's system-log tail, and the
observer's connection log (on stderr). Run it on a KVM host:

```console
./spike/learn-mode/s8_sdk_channel/run.sh
```

It is its own Go module so it stays out of the repository's build, lint, and
`compile-all` gates; it is deleted once the decisions land in
`specs/onboarding/learn-mode/observation-channel.md` and
`specs/network-and-secrets/interception-and-proxy.md`.

The observer is an in-process CONNECT proxy (HTTP and SOCKS5) that terminates
TLS with a generated CA; the endpoint reports credential-bearing **header
names** only — never a header value. `SPIKE_PROXY_HOST` (default `127.0.0.1`)
sets the address the proxy binds and the sandbox dials.

## Runtime home

The probe mirrors the launcher: it points `MSB_HOME` at a private home
(`$SPIKE_MSB_HOME`, default `~/.microsandbox-spike`, created `0750`) and runs the
SDK's `EnsureRuntime` there before creating any sandbox. On first run that
downloads the SDK-pinned runtime.

This is required because `CreateSandbox` resolves a runtime from `MSB_HOME`, and
with the default `~/.microsandbox` it picks up whatever a manual `msb` installed
there — which the SDK's FFI may refuse with:

```
create: runtime error: upgrade required: selected runtime <home>/bin/msb does
not advertise protected sandbox state
```

Setting `MSB_HOME` is what points the SDK's non-setup operations (sandbox, image,
volume) at the probe's home; `RuntimeConfig{Home: ...}` alone only governs
runtime setup.

## If `CreateSandbox` fails with a storage-root error

```
create: io error: catalog ancestor <dir> permits path substitution; use a
storage root protected from other accounts
```

microsandbox refuses a storage root with a group-/other-writable ancestor, and a
host running `umask 002` leaves the runtime's home (`$MSB_HOME`) group-writable.
`run.sh` warns and names the offending directory; fix it with
`chmod g-w,o-w <dir>` and rerun. See `.operator-shared/guides/dev-environment.md`
→ Microsandbox runtime.

