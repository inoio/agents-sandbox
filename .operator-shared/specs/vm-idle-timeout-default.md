---
description: Contract for the VM idle timeout default: it lives only in options.DefaultVMIdleTimeout; the config layer carries auto-stop-timeout as-is and never substitutes its own default.
read_if: Changing the idle timeout default, Config.AutoStopTimeout/Config.IdleTimeout, RunOptions.IdleTimeout, runoptions.Build, or the VM lifecycle's WithIdleTimeout wiring (#365).
---

# VM idle timeout default

## Contract

- The idle timeout has exactly one default: `options.DefaultVMIdleTimeout`
  (`internal/sandbox/options/timeouts.go`).
- The config layer does not invent a default. `viperconfig.Config.AutoStopTimeout`
  is the user's `auto-stop-timeout` value or zero when unset; there is no
  `Config.IdleTimeout()` accessor and no hardcoded fallback in `viperconfig`.
  `runoptions.Build` assigns `opts.IdleTimeout = cfg.AutoStopTimeout`.
- The VM lifecycle applies the default: `idleTimeout := opts.IdleTimeout; if
  idleTimeout <= 0 { idleTimeout = options.DefaultVMIdleTimeout }`, then
  `msbSdk.WithIdleTimeout(idleTimeout)`.
- The fallback is load-bearing: the SDK's `WithIdleTimeout` treats zero as
  *unlimited*, so a zero `RunOptions.IdleTimeout` must never reach the SDK.

## Why one package

A second default in `viperconfig` (a hardcoded 10s) made the VM layer's
fallback unreachable and meant tuning `DefaultVMIdleTimeout` changed nothing on
the normal path (#365). The documented `auto-stop-timeout` default (10s) is the
value of `options.DefaultVMIdleTimeout`, not a separate constant.
