---
title: Secrets
description: "Keep API keys and tokens on the host: agents-sandbox passes secrets as placeholders that the microsandbox proxy replaces only for allowed hosts."
layout: default
parent: Configuration
nav_order: 20
---

# Secrets

Secrets are environment variables whose real values stay on the host and are delivered through microsandbox's secret
mechanism. The VM receives a placeholder, not the real value. The network proxy replaces that placeholder only when a
request is sent to an allowed host over a connection whose destination can be verified.

This is different from an ordinary entry in `env`: values from `env` are copied into the VM as normal environment
variables. Put credentials in `env.secret` or `env.secret.yaml`, never in `env`.

> **Note:** If you use OpenCode's `/connect` command, see [OpenCode authentication](#opencode-authentication). That is the
> recommended workflow for credentials, which opencode stores in `auth.json`; it uses a literal microsandbox placeholder
> and an explicit `home:` mapping. The `provision-host-config: false` setting shown there belongs in the top-level
> launcher configuration in `~/.config/agents-sandbox/config.yaml`.

## Format

Two file formats are supported: legacy text and structured YAML. YAML files take precedence over legacy files for
the same key.

**Legacy format** — `env.secret`

One `KEY=value@host` per line. The part after the **last** `@` is a policy tag restricting which microsandbox runtime
hosts can access the secret. Values may contain `@` — everything before the last `@` is the value. Each entry must
define a host explicitly; omitting the host part drops the secret with a warning.

```shell
# .agents-sandbox/env.secret
GITHUB_TOKEN=ghp_xxxxxxxxxxxx@github.com
ANTHROPIC_API_KEY=sk-ant-xxxxxxxxxxxx@anthropic.com
```

**YAML format** — `env.secret.yaml`

A YAML object map from env-var name to `{ value, host?, hosts?, header_fields?, allow_any_host_dangerous? }`. Values
may contain **any characters** including `@`. `host` and `hosts` are optional when `allow_any_host_dangerous` is set,
but otherwise required — entries with neither hosts nor a dangerous flag are dropped with a warning. JSON is also
accepted (YAML 1.2 is a JSON superset).

```yaml
# .agents-sandbox/env.secret.yaml
GITHUB_TOKEN:
  value: "ghp_xxx@corp"
  host: microsandbox
ANTHROPIC_API_KEY:
  value: sk-ant-xxxxxxxx
  hosts: [gw-a.internal, gw-b.internal]
  header_fields: [authorization, x-api-key]
# No hosts defined — this entry is dropped with a warning
DROPPED_KEY:
  value: secret-value
TRUSTED_KEY:
  value: secret-value
  allow_any_host_dangerous: true
```

An empty `value` is valid and will be passed through unchanged.

## Precedence

Secrets are read from three scopes — generic user, per-slug user, and project — and files are merged from lowest to
highest precedence per key. The merge is tiered by format: **all** scopes' legacy `env.secret` entries are merged first
in scope order, then **all** scopes' `env.secret.yaml` entries in scope order:

1. generic user `env.secret` (legacy `KEY=value@host`)
2. per-slug user `env.secret` (legacy)
3. project `env.secret` (legacy)
4. generic user `env.secret.yaml`
5. per-slug user `env.secret.yaml`
6. project `env.secret.yaml`

A YAML entry always wins over a legacy entry for the same key, even across scopes — a generic user
`env.secret.yaml` overrides a project `env.secret`. Within a tier, a later scope wins. The YAML entry **fully
replaces** the legacy entry's hosts — if a YAML entry omits `host`, `hosts`, and `allow_any_host_dangerous`, the
resulting entry has no hosts and is dropped with a warning.

## Supported files

- `~/.config/agents-sandbox/env.secret` — generic user-level, legacy text format
- `~/.config/agents-sandbox/env.secret.yaml` — generic user-level, structured YAML (or JSON)
- `~/.config/agents-sandbox/<slug>/env.secret` — per-slug user-level (repo slug, shared by every clone), legacy text format
- `~/.config/agents-sandbox/<slug>/env.secret.yaml` — per-slug user-level, structured YAML (or JSON)
- `.agents-sandbox/env.secret` — project-level, legacy text format
- `.agents-sandbox/env.secret.yaml` — project-level, structured YAML (or JSON)

## Placeholder values

When no custom placeholder is configured, microsandbox uses the following guest-visible value:

```text
$MSB_<SECRET_NAME>
```

For example, the guest value of `GITHUB_TOKEN` is `$MSB_GITHUB_TOKEN`.

## Placeholders in request bodies

The proxy blocks a placeholder that reaches a host the secret neither allows nor passes through. That is fatal when
the placeholder travels in a request the agent does not control. A coding agent sends its whole conversation as the
completion request body, so once a placeholder enters the transcript — a tool that prints the environment, for
example — every later completion request is blocked and the session cannot continue.

`secrets.common.allow-placeholder-for` lists the hosts that may receive **any** secret's placeholder unchanged. Set it
to the agent's model endpoint:

```yaml
# ~/.config/agents-sandbox/config.yaml
secrets:
  common:
    allow-placeholder-for:
      - model.example.com
```

The key is config-file-only: it has no environment variable and no CLI flag, so trusting a host with placeholders is
a deliberate edit. A forwarded placeholder is inert — the real value stays on the host, and the proxy rewrites a
placeholder to the real value only for a host in the secret's `host`/`hosts`.

A host cannot be both substituted for and placeholder-passthrough. Two rules keep the lists disjoint:

- A host a secret already lists in `host`/`hosts` keeps substitution and is dropped from that secret's passthrough
  hosts. The credential that authenticates to the model host is unaffected.
- A secret with `allow_any_host_dangerous: true` and no explicit `host` or `hosts` substitutes for every host, so it
  gets no placeholder-passthrough hosts.

Changing this key changes the VM's secret fingerprint, so the next run recreates the project VM once.

## Header substitution scope

By default microsandbox replaces a secret's placeholder in every request header it sees. That lets an untrusted
program inside the sandbox put the placeholder in a header the upstream echoes back, and read the real secret from
the response. `header_fields` restricts substitution to the header names you list:

```yaml
# .agents-sandbox/env.secret.yaml
GITHUB_TOKEN:
  value: ghp_xxxxxxxx
  host: github.com
  header_fields: [authorization]
```

Field names are case-insensitive, and the list is deduplicated. Omitting `header_fields`, or setting it to an empty
list, keeps the default: the placeholder is replaced in every header. Only the YAML format carries the key; the
legacy `KEY=value@host` format has no place for it.

Changing the list changes the sandbox's secret fingerprint, so the next run recreates the project VM once.

## Trusting the CA in applications

Replacing a placeholder requires the proxy to terminate TLS towards the allowed host. The VM trusts a microsandbox CA
at `/.msb/tls/ca.pem`; microsandbox's guest init installs that CA into the system store and points the common runtimes
at it through `SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, and `CURL_CA_BUNDLE`. `curl`, `git`,
Python, Go, and Node need no extra setup.

Applications that keep their **own** certificate store do not read any of that. For those the runner image installs
microsandbox's [trust helper](https://github.com/superradcompany/microsandbox/blob/v0.7.8/scripts/msb-trust/msb-trust.sh)
as `msb-trust`:

```console
msb-trust all      # every supported application whose tools are installed
msb-trust java     # the JDK's default trust store
msb-trust chrome   # the current user's Chrome/Chromium NSS store
```

The JDK's trust store is root-owned, so run `msb-trust java` from `agents-sandbox shell --root`; `chrome` writes the
current user's store and needs no elevation.

The helper imports the CA, preserves the certificates that are already there, and never installs packages. `java`
needs `keytool` on `PATH` (or `JAVA_HOME` pointing at a JDK) and `chrome` needs `certutil`
(`libnss3-tools`/`nss-tools`); a missing tool is reported instead of installed. `msb-trust <application> --help` lists
what that application needs. The helper's own messages say `sh msb-trust.sh …`, the layout upstream expects when you
save the script next to your code, so translate the name to `msb-trust` when you follow them. The
[Java recipe]({% link recipes/java-ca-trust.md %}) has the full setup and the import every VM boot needs.

Two things the helper does not cover. Containers built inside the VM have their own root store, so a `docker build` that
downloads over HTTPS fails certificate verification: copy the CA out of the VM
(`/usr/local/share/ca-certificates/microsandbox-ca.crt`) into the build context and install it in that Dockerfile, then
run `update-ca-certificates`. Applications with a bundled trust store also have to be configured individually.

## OpenCode authentication

OpenCode stores credentials created by `/connect` in:

```text
~/.local/share/opencode/auth.json
```

That is different from the agents-sandbox input directory:

```text
~/.config/agents-sandbox/opencode/
```

(`opencode2` uses `~/.config/agents-sandbox/opencode2/` instead; see the fallback note in
[Agent configuration]({% link configuration/agent.md %}).) An `auth.json` placed in the latter directory is mirrored to
`/home/dev/.config/opencode/auth.json`; it is not automatically OpenCode's credential store. To use secret placeholders in OpenCode's credential store, provision the
placeholder file explicitly with the launcher's `home:` key.

For example, for GitHub Copilot:

```yaml
# ~/.config/agents-sandbox/config.yaml
provision-host-config: false
home:
  # The source is relative to ~/.config/agents-sandbox/config.yaml.
  .local/share/opencode/auth.json: opencode/auth.json
```

```json
// ~/.config/agents-sandbox/opencode/auth.json
{
  "github-copilot": {
    "type": "oauth",
    "access": "$MSB_GH_COPILOT_ACCESS_TOKEN",
    "refresh": "$MSB_GH_COPILOT_REFRESH_TOKEN",
    "expires": 0
  }
}
```

```yaml
# ~/.config/agents-sandbox/env.secret.yaml
GH_COPILOT_ACCESS_TOKEN:
  value: "<access-token>"
  hosts:
    - api.githubcopilot.com
GH_COPILOT_REFRESH_TOKEN:
  value: "<refresh-token>"
  hosts:
    - api.githubcopilot.com
```

The network policy is separate from the secret allowlist. The VM must be allowed to reach the same endpoint, for example:

```yaml
# ~/.config/agents-sandbox/config.yaml
network:
  egress-allow:
    - api.githubcopilot.com
```

### GitHub Copilot on GitHub Enterprise (GHE.com)

With a GitHub Enterprise account, OpenCode stores the enterprise domain as `enterpriseUrl` in `auth.json` and sends the
tokens to that domain's Copilot endpoints instead of `api.githubcopilot.com`. Allow the secrets for those hosts, and
allow the domain in the network policy. For example, for the enterprise domain `example.ghe.com`:

```json
// ~/.config/agents-sandbox/opencode/auth.json
{
  "github-copilot": {
    "type": "oauth",
    "access": "$MSB_GH_COPILOT_ACCESS_TOKEN",
    "refresh": "$MSB_GH_COPILOT_REFRESH_TOKEN",
    "expires": 0,
    "enterpriseUrl": "example.ghe.com"
  }
}
```

```yaml
# ~/.config/agents-sandbox/env.secret.yaml
GH_COPILOT_ACCESS_TOKEN:
  value: "<access-token>"
  hosts:
    - copilot-api.example.ghe.com
    - api.example.ghe.com
GH_COPILOT_REFRESH_TOKEN:
  value: "<refresh-token>"
  hosts:
    - copilot-api.example.ghe.com
    - api.example.ghe.com
```

```yaml
# ~/.config/agents-sandbox/config.yaml
network:
  egress-allow:
    - .example.ghe.com
```

### GitHub Copilot with opencode2

`opencode2` keeps its credentials in its database (`~/.local/share/opencode/opencode.db`) instead of `auth.json`. It
imports `auth.json` only when it upgrades a database an earlier version created; a fresh database, which every new
sandbox home volume starts with, ignores the file. A provisioned `auth.json` therefore leaves GitHub Copilot without a
connection in every new project, while API-key providers configured in `opencode.jsonc` keep working.

Register the placeholder with the `opencode2` daemon instead, from a startup hook. The hook stores the value of
`$GH_COPILOT_REFRESH_TOKEN` (the placeholder, not your token) as the Copilot credential once the daemon is up, and
does nothing when a Copilot credential already exists or the sandbox runs another agent. `opencode2` sends that token
to `copilot-api.<domain>`, which `GH_COPILOT_REFRESH_TOKEN` must list in its `hosts`, as in
[GitHub Copilot on GitHub Enterprise (GHE.com)](#github-copilot-on-github-enterprise-ghecom). The script
is written for GitHub Enterprise (GHE.com); set `ENTERPRISE_DOMAIN` to your domain.

```sh
#!/bin/sh
# ~/.config/agents-sandbox/opencode2-copilot-credential.sh
ENTERPRISE_DOMAIN="example.ghe.com"
SERVER="http://127.0.0.1:4096"
AUTH="opencode:opencode"

command -v opencode2 >/dev/null 2>&1 || exit 0
[ -n "$GH_COPILOT_REFRESH_TOKEN" ] || exit 0

register() {
	attempts=0
	until credentials=$(curl -fsS -m 5 -u "$AUTH" "$SERVER/api/credential" 2>/dev/null); do
		attempts=$((attempts + 1))
		[ "$attempts" -ge 120 ] && return 1
		sleep 2
	done
	if printf '%s' "$credentials" | jq -e '.data[] | select(.integrationID == "github-copilot")' >/dev/null; then
		return 0
	fi
	jq -n --arg token "$GH_COPILOT_REFRESH_TOKEN" --arg domain "$ENTERPRISE_DOMAIN" '{
		integrationID: "github-copilot", label: "msb secret", activate: true,
		value: {type: "oauth", methodID: "device", refresh: $token, access: $token, expires: 0,
			metadata: {enterpriseUrl: $domain}}
	}' | curl -fsS -m 30 -u "$AUTH" -H "Content-Type: application/json" \
		-X POST --data @- "$SERVER/api/credential" >/dev/null
}

if [ "$1" = "--foreground" ]; then
	register
	exit $?
fi
# The daemon starts after the hooks, so wait for it in a new session.
setsid -f "$0" --foreground </dev/null >/tmp/opencode2-copilot-credential.log 2>&1
```

```yaml
# ~/.config/agents-sandbox/config.yaml
home:
  .local/bin/opencode2-copilot-credential.sh:
    source: opencode2-copilot-credential.sh
    hook: startup
```

The hook needs `curl` and `jq` in the runner image; the managed base provides both. It runs when the sandbox starts,
so restart a running sandbox once to pick it up. The credential is kept in the home volume's database, so a **reset**
home volume gets it again from the hook on its next start.

If a secret's hosts do not match the endpoint OpenCode calls, the request fails with
`Forbidden: Request blocked by secret policy.` The proxy refuses to forward a placeholder to a host the secret is not
allowed for. Add the host from the failing request to the secret's `hosts`.

Use `agents-sandbox config home` to verify the `home:` mapping. `agents-sandbox config agent` shows the agent mirror and
drop-in candidates, but does not show `home:` mappings or files already present in the persistent home volume.

> **Note:** After running `/connect` with opencode _inside_ the agents-sandbox, you can use `!` to switch to shell mode and then run
> ```console
> cat ~/.local/share/opencode/auth.json
> ```
> to see the credentials stored in the sandbox (to store them outside in `env.secret.yaml`).
