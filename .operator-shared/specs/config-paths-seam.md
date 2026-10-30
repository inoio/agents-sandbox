---
description: Contract for the internal/configpaths.ConfigPaths seam that resolves every host path agents-sandbox reads or writes - including the host home - so production never hand-builds a config path and tests never touch the real home.
read_if: Adding or changing host config/home path resolution, or writing tests that exercise config paths or host-home resolution.
---

# Config-paths seam

## Contract

`internal/configpaths` is the single authority for the **host** paths agents-sandbox resolves. `configpaths.Get()`
returns a `ConfigPaths`; production code obtains host paths through it, never by hand-building them.

- Methods cover the tool's XDG dirs (`UserConfigDir`/`UserCacheDir`/`UserStateDir`/`UserDataDir`), the per-agent
  config dirs, the project config dir, the env/secret/Dockerfile paths, and `UserHomeDir() (string, error)` — the host
  user's home, the root the home-relative agent config and `home:` manifests resolve against.
- The per-agent config-dir accessors (`UserAgentConfigDir`, `UserAgentConfigDirCandidates`, `ProjectAgentConfigDir`,
  `ProjectAgentConfigDirCandidates`) return an error when a profile's config-dir name is not a single clean path element
  (`""`, `"."`, `".."`, or one containing a path separator), and `validConfigDirName` is the one guard. It returns the
  error rather than panicking, so a malformed profile fails at the resolution boundary — `reprovision.ResolveConfigSource`
  / `PrimaryDirs` / `LoadConfigFilesForHost` — with a message naming the agent (#366).
- `MSBHome()` is a free function (not a method) so the low-level `sandbox/msb` adapter can resolve the private
  microsandbox home without importing the higher-level runtime package.
- Config-file discovery (`FindConfigFile`, with `ExtJSONC`/`ExtJSON5`) is a free function here too: the launcher-config
  and home-manifest loaders both call it, so the extension order and the first-existing-file lookup have one definition
  and the two loaders cannot silently read different `config.<ext>` files from one directory.
- Guest/VM config paths (e.g., `agent.VMConfigPath(home)`, `reprovision.AgentConfigPath`) are sandbox paths, derived
  from the VM home, and deliberately do **not** use this seam.

Deliberate exceptions (host paths that are not the tool's config and keep their own resolution): `mounts.expandHome`
(user-supplied `~/` bind-mount sources), `sandbox/docker`'s `~/.docker` (third-party CLI config with its own
`DOCKER_CONFIG` override), and the `xdgBaseDir` home fallback inside `configpaths` itself.

## Test isolation

Under tests `configpaths.Get()` defaults to a fail-fast implementation that panics on first use; a test exercising
home-relative resolution installs `WithMockConfigPaths(t)` (which also redirects `$HOME` into its temp tree) or
`WithMockConfigPathsHomeError(t, err)`. `homeconfig` and `runtime` tests that resolve the home via an empty or `~`
manifest source therefore opt in explicitly. See [test isolation](test-isolation.md) for the full fail-fast and
host-home-redirect contract, and `reconfig-decision.md` for the spurious "agent config changed" prompt that motivated
it.
