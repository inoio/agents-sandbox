---
description: Contract for the host facts the launcher applies as defaults to a project VM's environment — the host time zone, the UTF-8 locale, and the git commit identity — the precedence rule that an explicitly configured value wins, and the suppression rule for a user-provisioned git config.
read_if: Changing how the project VM environment is assembled or fingerprinted (`prepare.loadEnvFingerprints`, `envsecret.LoadEnvAndSecrets`, `vm.buildProjectVMEnv`, `desiredFingerprints`); adding a host fact forwarded into the sandbox; or diagnosing a guest `date`, locale, or commit identity that differs from the host, a missing `TZ`/`LANG`/`GIT_AUTHOR_*` in the VM, or a custom base whose `date` is off by the host's UTC offset.
---

# Host environment defaults

The project VM's clock runs in UTC and its locale is `C`, whatever the host's are, because the sandbox boots from an
image whose only `ENV` is `PATH`. The launcher therefore resolves a small set of host facts and applies them as
**defaults** to the project VM environment, so every session in a fresh VM — the agent attach, the interactive shell,
startup hooks, detached jobs, and ad-hoc exec — sees the host's time zone, a UTF-8 locale, and the host's git commit
identity without user configuration. Found in the 0.8 e2e pass (#783): Claude Code reported "done 4:05 PM" for a turn
the host saw at 17:05.

The defaults are the same values on every run for a given host, so they participate in the env fingerprint like any
other env entry: a host fact that changes recreates the VM on the next run, and one that cannot be resolved is left
unset rather than warned about.

## Precedence

An env value the user sets explicitly **wins over the host default**. The host defaults are merged under the
file-loaded env map, filling only the keys it leaves absent (`prepare.loadEnvFingerprints` overlays them onto
`envsecret.EnvSecrets.Vars` before `desiredFingerprints` computes the fingerprint). A user who wants UTC, a different
git identity, or no locale sets `TZ`, `GIT_AUTHOR_*`/`GIT_COMMITTER_*`, or `LANG` in a config env file and gets that
value.

The defaults are part of the desired env, so they reach the VM through the same path as a configured value:
`desiredFingerprints` hashes them, and `vm.buildProjectVMEnv` merges them into the sandbox spec's env, where the
microsandbox runtime applies them to the sandbox and exec/attach overlay their own `HOME` on top without replacing
them.

## The defaults

### Time zone

`TZ` is set to the host's IANA zone:

- the host process's `TZ` when set, used verbatim (it may be an IANA name, a `:`-prefixed name, or a POSIX string);
- otherwise the `/etc/localtime` symlink target when it lies under a `zoneinfo/` directory, taking the suffix after
  `zoneinfo/` as the zone name (works for the Linux `/usr/share/zoneinfo/<zone>` and macOS
  `/var/db/timezone/zoneinfo/<zone>` forms);
- otherwise unset, leaving the guest on UTC.

Resolving a zone requires the guest to carry zoneinfo (`tzdata`). The embedded managed base's `debian:trixie-slim`
already provides it, so managed images need nothing. A custom base must provide it or its `date` and any zone-aware
process stay on UTC; `docs/runner-image.md` states this in the base image contract.

### Locale

`LANG` is set to `C.UTF-8` when the host's effective locale is UTF-8 (`LC_ALL`, else `LC_CTYPE`, else `LANG`,
matched case-insensitively against `utf-8`/`utf8`). `C.UTF-8` rather than the host's exact value because the image
ships no generated locales and `C.UTF-8` is built into glibc, so a host locale such as `en_US.UTF-8` would warn and
fall back if forwarded verbatim. When the host locale is not UTF-8, `LANG` is left unset and the guest stays on `C`,
matching the host.

Only `LANG` is set, never `LC_ALL`, so a user's per-category settings are not clobbered and a configured `LANG` still
wins.

### Git commit identity

The host's global git config `user.name`/`user.email` (read with the host `git` binary; absent `git` or a failed read
leaves the identity unset with no warning) is emitted as `GIT_AUTHOR_NAME`, `GIT_AUTHOR_EMAIL`, `GIT_COMMITTER_NAME`,
and `GIT_COMMITTER_EMAIL`, only when both name and email resolve — a half identity is not useful. The four variables
are set so a commit needs no config lookup for its author or committer.

These environment variables outrank `user.name`/`user.email` from **any** git config file, including one the agent
writes inside the VM on the persistent home volume, because git treats the ident variables as overrides. The escape
hatch is the precedence rule above: put `GIT_AUTHOR_*`/`GIT_COMMITTER_*` in a config env file and the user's value
wins. The launcher writes no git config file.

**Suppression.** The identity default is skipped entirely when any config scope's `home:` provision keys provisions a
git global config (`.gitconfig` or `.config/git/config`). The rule is: provisioning a git config means the user owns
the sandbox's git identity. The detection reads each scope's home manifest
(`homeconfig.ReadHomeFromConfigDir` over `configpaths.ConfigScopes`); it does not inspect the file's contents, so an
alias-only gitconfig suppresses the identity too.

## Non-goals

- No configuration key; the behavior is not tunable. A user overrides individual values through the existing env
  files.
- No forward of the host's terminal, editor, pager, or other presentation variables.
- No host SSH agent, GPG material, or CA private keys; secrets reach a VM only through microsandbox's secret
  mechanism.
- No generated `~/.gitconfig`.
