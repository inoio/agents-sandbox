# Sandbox identity and fail-closed removal

Status: implemented (issues #158, #162), merged
Date: 2026-10-15, updated 2026-10-28

## Name vs identity

A sandbox's **name** is a mutable label: the launcher derives it from the project/agent key, and that name can be removed and recreated to point at a different sandbox. A sandbox's **identity** is the stable ID the microsandbox runtime assigns at creation (SDK v0.7.8 exposes `SandboxHandle.ID()` and `Sandbox.ID()`). Operations that act on a *specific* sandbox must key off identity; names are for discovery and creation only. The launcher's `msb` seam does not need `ID()` for any removal/attach decision; it was added to `SandboxHandle` later only so `sandbox inspect` can display the stable identity (Slice 3 of the runtime-management epic).

## Fail-closed contract

The launcher does not compare IDs itself. It relies on the SDK's identity-aware lifecycle operations (`Remove`, `Destroy`, `Stop`, `Kill`, `Refresh`, `Connect`, `Start`, `WaitForStatus`, `Restart`), which carry the receiver's stored identity and return `ErrSandboxReplaced` when the name now refers to a different persisted sandbox. The seam helper `msb.IsReplaced(err)` (mirroring the existing `msb.IsNotFound`) classifies that outcome.

All of this is **same-process**: the launcher relies on a `SandboxHandle`/`Sandbox` it already holds. No cross-process identity is persisted.

Per-path handling:

- **Prune** (best-effort cleanup): skip-and-report on replaced or gone; removal must never use a bare name.
- **Throwaway task sandboxes** (prefill/copy/edit): destroy through the live sandbox they created; tolerate replaced/not-found.
- **Reconfig replace**: destroy the held old VM through its handle with the SDK's atomic `Destroy` (identity-safe stop-and-remove) when its config changed. An already-gone name is recreated; a replaced identity **fails closed** (errors) — the name now holds a sandbox this session did not create, so routing to recreate would silently attach to it and skip the requested image/mount changes. The destroy runs inside the same per-`(slug, agent)` creation flock (`ensure-vm.lock`) that serializes creation, so a concurrent invocation cannot drive a create/recreate while this one is still tearing the old VM down (#397).
- **Reaper**: its live sandbox being replaced or gone is terminal — stop waiting, return.

Invariant: the launcher never silently adopts a same-name replacement to satisfy a recreate request. Recreate either removes the sandbox this session held (then creates fresh) or fails closed; it must not re-look-up the name and attach to whatever now holds it.

The name-based `msb.Client.RemoveSandbox(ctx, name)` is retired so no path can act on a bare name; removal goes through `SandboxHandle.Remove`/`Destroy` and live `Sandbox.Destroy(ctx)`, both on the seam.

## No persistence

Identity is compared live only. No last-known ID is written to `state.yaml` and no state migration is added: a freshly acquired handle legitimately adopts the current identity, so a persisted ID would create false positives.

## Relationship to #144

#144 (two checkouts sharing one VM name) is a separate naming/derivation concern. This contract is defense-in-depth: it makes a name collision fail closed instead of redirecting a destructive operation to the wrong VM.
