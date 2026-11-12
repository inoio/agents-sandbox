---
title: Access Git Servers Without an SSH Key
description: "Give the agent working git access to GitHub, GitLab, and any other HTTPS git host by configuring a token, without an SSH key."
layout: default
parent: Recipes
nav_order: 55
---

# Access Git Servers Without an SSH Key

Give the agent working git access — clone, fetch, push — to GitHub, GitLab, and other git hosts from inside the
sandbox. No SSH key is involved: a [secret]({% link configuration/secrets.md %}) hands git a placeholder, and the
proxy swaps in the real token only for the host you allow. The token itself never enters the sandbox.

> **Note:** These setups authenticate over **HTTPS**. A remote that still uses SSH (`git@host:...`) is rewritten to
> HTTPS at the end of this page.

## How it works

A [secret]({% link configuration/secrets.md %}) puts the placeholder `$MSB_<NAME>` into the sandbox instead of the
real value. When git sends that placeholder to an allowed host, the proxy replaces it with the real token on the way
out. This includes `Authorization: Basic` headers: the proxy decodes the credentials, substitutes, and re-encodes.
That is why a git credential helper works here — git sends the placeholder as a password, encoded into Basic, and the
proxy fixes it up.

Replacing a placeholder requires the proxy to see into the request, so the credential only travels over intercepted
HTTPS to a host in the secret's `hosts`. SSH is not intercepted, and plain HTTP is not suitable.

## GitHub

GitHub uses the `gh` CLI, which is installed in the runner image. A startup hook logs `gh` in on every VM start and
wires git to ask `gh` for credentials. Provision the hook through the `home:` key in your project config:

```yaml
# .agents-sandbox/config.yaml
home:
  .github/github-auth.sh:
    source: github-auth.sh
    hook: startup
```

```sh
#!/bin/sh
# .agents-sandbox/github-auth.sh
if [ -n "${GITHUB_PAT:-}" ]; then
  printf '%s\n' "$GITHUB_PAT" | gh auth login --with-token
  gh auth setup-git
fi
```

Define the token as a secret. The name and the two hosts are both significant:

```yaml
# .agents-sandbox/env.secret.yaml
GITHUB_PAT:
  value: ghp_xxxxxxxx
  hosts: [github.com, api.github.com]
  header_fields: [authorization]
```

> **Warning:** Do not name the secret `GITHUB_TOKEN` or `GH_TOKEN`. When either of those is set, `gh auth login
> --with-token` refuses to store a token and the hook fails.

`gh auth login` validates the token against the API, which is why `api.github.com` has to be in the hosts list
alongside `github.com`. `gh auth setup-git` then configures git to ask `gh` for the credential, and `gh` returns the
placeholder.

## GitLab

GitLab has no equivalent of `gh auth setup-git`. Configure git's credential store directly and write the placeholder
into `~/.git-credentials` from a startup hook:

```yaml
# .agents-sandbox/config.yaml
home:
  .github/gitlab-auth.sh:
    source: gitlab-auth.sh
    hook: startup
```

```sh
#!/bin/sh
# .agents-sandbox/gitlab-auth.sh
if [ -n "${GITLAB_TOKEN:-}" ]; then
  git config --global credential.helper store
  umask 077
  printf 'https://oauth2:%s@gitlab.com\n' "$GITLAB_TOKEN" > "$HOME/.git-credentials"
fi
```

```yaml
# .agents-sandbox/env.secret.yaml
GITLAB_TOKEN:
  value: glpat-xxxxxxxx
  hosts: [gitlab.com]
  header_fields: [authorization]
```

GitLab accepts a personal, project, or group access token as the password with any username; `oauth2` is the
convention. The token needs the `read_repository` scope, and `write_repository` as well to push.

## Other HTTPS git hosts

The credential store works for any HTTPS git host: one line per host in `~/.git-credentials`, each carrying that
host's own secret placeholder. Use this instead of the `gh` route for GitHub, not alongside it.

```yaml
# .agents-sandbox/config.yaml
home:
  .github/git-auth.sh:
    source: git-auth.sh
    hook: startup
```

```sh
#!/bin/sh
# .agents-sandbox/git-auth.sh
git config --global credential.helper store
umask 077
: > "$HOME/.git-credentials"
[ -n "${GITLAB_TOKEN:-}" ] && printf 'https://oauth2:%s@gitlab.com\n' "$GITLAB_TOKEN" >> "$HOME/.git-credentials"
[ -n "${GITHUB_PAT:-}" ]   && printf 'https://x-access-token:%s@github.com\n' "$GITHUB_PAT" >> "$HOME/.git-credentials"
```

The username in each line is ignored by most hosts; any non-empty value works. Give each host its own secret so the
proxy only ever substitutes that host's token.

### Rewrite SSH remotes to HTTPS

A repository cloned over SSH keeps an `git@host:owner/repo` remote, which git would try over SSH. Tell git to use
HTTPS for those URLs instead:

```sh
git config --global url."https://github.com/".insteadOf "git@github.com:"
git config --global url."https://github.com/".insteadOf "ssh://git@github.com/"
git config --global url."https://gitlab.com/".insteadOf "git@gitlab.com:"
git config --global url."https://gitlab.com/".insteadOf "ssh://git@gitlab.com/"
```

This rewrites the remote as git uses it, so the repository's own `.git/config` is untouched and every checkout in the
sandbox benefits.

### Send a raw header instead

If a host needs the credential in a specific header rather than basic auth, set it per URL:

```sh
git config --global http."https://gitlab.com/".extraHeader "Authorization: Bearer $MSB_GITLAB_TOKEN"
```

This avoids the credentials file, but the server has to accept the `Bearer` scheme. GitLab's and GitHub's git
endpoints expect basic auth, which the credential store sends, so prefer the credential store unless the host
documents otherwise.

## Network access

Under the default `public` profile, the public git hosts are already reachable. Under `profile: none`, add each host
so it is both allowed and resolvable:

```yaml
# .agents-sandbox/config.yaml
network:
  profile: none
  egress-allow:
    - github.com
    - api.github.com
    - gitlab.com
```

## Keep tokens out of the agent's context

A placeholder that reaches the agent's model request blocks every later turn: the agent sends its whole conversation
with each completion request, and the proxy refuses to forward a placeholder to a host that secret does not allow.
Keep tokens in `env.secret` or `env.secret.yaml`, never in `env`, and out of any file the agent reads back. See
[Secrets]({% link configuration/secrets.md %}).
