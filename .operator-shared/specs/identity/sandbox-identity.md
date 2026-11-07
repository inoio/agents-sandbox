---
description: Contract distinguishing a sandbox's mutable *name* from its stable *identity* - operations that act on a specific sandbox key off the SDK's identity-aware lifecycle methods and fail closed on `ErrSandboxReplaced` (`msb.IsReplaced`); the per-path handling for prune, throwaway task sandboxes, reconfig replace, the reaper, project-VM create, and `ConnectOrStart`; and no persisted identity.
read_if: Working on the msb seam's sandbox create/removal/attach paths, `EnsureProjectVM`, prune, the reaper, reconfig-replace, or reasoning about name-vs-identity safety; or diagnosing `ErrSandboxReplaced`/`ErrSandboxAlreadyExists`.
---

# Sandbox identity and fail-closed removal

## Name vs identity

A sandbox's **name** is a mutable label: the launcher derives it from the project/agent key, and that name can be removed and recreated to point at a different sandbox. A sandbox's **identity** is the stable ID the microsandbox runtime assigns at creation (the SDK exposes `SandboxHandle.ID()` and `Sandbox.ID()`). Operations that act on a *specific* sandbox must key off identity; names are for discovery and creation only. The launcher's `msb` seam does not need `ID()` for any removal/attach decision; it was added to `SandboxHandle` only so `sandbox inspect` can display the stable identity.

## Fail-closed contract

The launcher does not compare IDs itself. It relies on the SDK's identity-aware lifecycle operations (`Remove`, `Destroy`, `Stop`, `Kill`, `Refresh`, `Connect`, `Start`, `ConnectOrStart`, `WaitForStatus`, `Restart`), which carry the receiver's stored identity and return `ErrSandboxReplaced` when the name now refers to a different persisted sandbox. The seam helper `msb.IsReplaced(err)` (mirroring `msb.IsNotFound`) classifies that outcome.

All of this is **same-process**: the launcher relies on a `SandboxHandle`/`Sandbox` it already holds. No cross-process identity is persisted.

Per-path handling:

- **Prune** (best-effort cleanup): skip-and-report on replaced or gone; removal must never use a bare name.
- **Throwaway task sandboxes** (prefill/copy/edit): destroy through the live sandbox they created; tolerate replaced/not-found.
- **Reconfig replace**: destroy the held old VM — the handle the reconfig decision was read from (`readLiveProjectVM`) — through its handle with the SDK's atomic `Destroy` (identity-safe stop-and-remove) when its config changed. An already-gone name is recreated; a replaced identity **fails closed** (errors) — the name now holds a sandbox this session did not create, so routing to recreate would silently attach to it and skip the requested image/mount changes. The destroy runs inside the same per-`(slug, agent)` creation flock (`ensure-vm.lock`) that serializes creation, so a concurrent invocation cannot drive a create/recreate while this one is still tearing the old VM down.
- **Reaper**: its live sandbox being replaced or gone is terminal — stop waiting, return.
- **Project VM create**: `CreateSandbox` without `WithReplace`, under the creation flock after the post-lock re-check. A name taken in between fails with `ErrSandboxAlreadyExists` (`msb.IsSandboxAlreadyExists`) and is reported, never replaced or attached to. `ConnectOrCreateSandbox` is not used: it adopts an existing same-name sandbox and ignores the create options, which would also record fingerprints for a config the VM does not have. Throwaway task sandboxes carry a unique name and create without replace too.
- **Existing project VM**: one `SandboxHandle.ConnectOrStart` on the held handle connects, waits for a concurrent start, or starts the VM; a replaced handle fails closed. It starts attached, so the returned sandbox owns the VM process exactly when this call booted it: the seam reports that `OwnsLifecycle()` reading as `booted`, which drives the once-per-boot setup (`VMBootStarted`). Of concurrent callers on one stopped VM exactly one boots it. An unreadable ownership is an error, not a guess. `TestRealClientSandboxLifecycle` (`realstack_msb_test.go`, `msb` tag) pins the taken-name, single-boot, and replaced-handle semantics against the real runtime.

Invariant: the launcher never silently adopts a same-name replacement to satisfy a recreate request. Recreate either removes the sandbox this session held (then creates fresh) or fails closed; it must not re-look-up the name and attach to whatever now holds it.

The name-based `msb.Client.RemoveSandbox(ctx, name)` is retired so no path can act on a bare name; removal goes through `SandboxHandle.Remove`/`Destroy` and live `Sandbox.Destroy(ctx)`, both on the seam.

## No persistence

Identity is compared live only. No last-known ID is written to `state.yaml` and no state migration is added: a freshly acquired handle legitimately adopts the current identity, so a persisted ID would create false positives.

This contract is defense-in-depth against a name collision redirecting a destructive operation to the wrong VM; the separate concern of two checkouts deriving one VM name is `checkout-identity.md`.
