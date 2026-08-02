# Config Update with Restart Prompt

**Goal:** When the embedded provider config changes, opencode-msb prompts the user whether to restart the opencode serve daemon (which disconnects active clients) or proceed with the old config.

**Scope:** Changes to `internal/sandbox/runner.go`, `internal/sandbox/provision.go`, and daemon management flow.

**Out of scope:** Changes to the opencode server, hash persistence across runs, or background update mechanisms.

---

## Design

### Problem

Opencode-msb embeds a provider config (`provider-config.json5`) into the binary via `//go:embed`. On every run, `loadConfigFiles` merges this with user/project configs and `provisionSandbox` writes the merged config to `/home/dev/.config/opencode/` inside the VM. However, the opencode serve daemon inside the VM only reads config on startup — if the daemon is already running, the new config sits on disk but is never applied.

The opencode server has no reload endpoint or signal handler. The only way to apply a new config is to kill and restart the daemon, which severs all connected WebSocket clients.

### Solution

Before provisioning, read the config currently on disk inside the VM and compare its hash with the newly generated config. If they differ and the daemon is running, prompt the user to either restart (apply new config, disconnect clients) or proceed (keep old config, clients stay attached).

### Flow

```
prepareSandbox()
  → EnsureProjectVM                    // create / reuse / start VM
  → loadConfigFiles(userDir)           // compute new config + hash
  → readConfigFromVM()                 // cat opencode.jsonc, sha256sum on host
  → compare(newHash, diskHash)
     ├─ same           → skip provision, skip EnsureDaemon, continue to attach
     ├─ different, daemon running → prompt user (default: proceed)
     │    ├─ restart   → provisionSandbox + EnsureDaemon (kill + restart)
     │    └─ proceed   → skip provision, skip EnsureDaemon
     └─ different, daemon not running → skip prompt (nothing to disconnect)
           → provisionSandbox + EnsureDaemon (start daemon)
  → AttachClient
```

### Key Details

**Read from VM** (cross-boundary read):
- `sb.Shell(ctx, "cat /home/dev/.config/opencode/opencode.jsonc 2>/dev/null")`
- Hash the output with `sha256sum` on the host (reuse `encoding/sha256` in a helper)
- If the file doesn't exist, treat as "no disk config" → no comparison needed

**Prompt** (host-side `prompt.go`):
- Only shown when daemon IS running AND hashes differ
- Two choices: "Restart daemon (apply new config, active clients will reconnect)" / "Proceed without changes"
- Default: "Proceed" (safer — doesn't disconnect clients)
- Timeout: 30s (non-responsive user = proceed, same as default)

**Provision skip** (`provisionSandbox`):
- Called only when user chooses restart (or daemon was dead)
- Not a partial call — the function writes ALL config files and cleans envrc
- If skipped, no config changes happen in the VM; the daemon continues with old config

**EnsureDaemon**:
- Reverted to original behavior (healthcheck only, no hash logic)
- The hash check is now in `prepareSandbox`, not inside `EnsureDaemon`
- After provision (restart path), `EnsureDaemon` handles the restart via its existing restart-on-unhealthy logic

### Edge Cases

| Scenario | Behavior |
|---|---|
| First VM, no config on disk | No comparison → provision + start daemon |
| Reuse, config unchanged | Hash match → skip provision, skip restart, attach |
| Reuse, config changed, daemon running | Prompt: restart vs proceed |
| Reuse, config changed, daemon crashed | No prompt (nothing running to disconnect) → provision + start |
| Reuse, daemon not healthy but configs match | Healthcheck fails → `EnsureDaemon` restarts daemon (no provider config change) |

### Files Changed

| File | Change |
|---|---|
| `internal/sandbox/runner.go` | Rewrite `prepareSandbox()` flow, add `configHash`, `readConfigFromVM` |
| `internal/sandbox/provision.go` | Make `provisionSandbox` callable without being auto-invoked (already public) |
