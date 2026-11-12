---
title: Access Git Servers Without an SSH Key
description: "Give the agent working git access to GitHub, GitLab, and any other HTTPS git host by configuring a token, without an SSH key."
layout: default
parent: Recipes
nav_order: 55
---

# Access Git Servers Without an SSH Key

Give the agent working git access to GitHub, GitLab, and other git hosts from inside the
sandbox without provisioning SSH keys, but by using the secret mechanism.

## Common steps

### Rewriting SSH remotes to HTTPS

All approaches below authenticate over HTTPS. A repository cloned over SSH keeps an `git@host:owner/repo` remote, which git would try over SSH. The examples below include snippets for rewriting SSH access to HTTPS access.

### Network access

Sandboxes need to be able to reach the git server hosts. Under the default `public` profile, the public git hosts are already reachable. Under non-public profiles, add each host so it is both allowed and resolvable:

```yaml
# .agents-sandbox/config.yaml
network:
  profile: none
  egress-allow:
    - github.com
    - api.github.com
    - gitlab.com
```

### Secrets

Secrets should not be stored in the project config, but in the project-specific user config. Determine the path and
create the directory if it's missing:

```bash
asb config paths|grep "per-project user config"
# output e.g. "  per-project user config  /home/user/.config/agents-sandbox/example-project-123456789abcde  (missing)"
mkdir -p /home/user/.config/agents-sandbox/example-project-123456789abcde
```

For all approaches below, edit secrets under the respective path, e.g. `/home/user/.config/agents-sandbox/example-project-123456789abcde/env.secret.yaml`.

## GitHub

GitHub uses the `gh` CLI. Install it via a custom Dockerfile and initialize it via a startup hook.

When using the managed base image (Debian), add this snippet to your project Dockerfile (for custom base images, adapt
the snippet):

```Dockerfile
# .agents-sandbox/Dockerfile
RUN curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_$(dpkg --print-architecture).deb" -o /tmp/gh.deb \
 && dpkg -i /tmp/gh.deb \
 && rm -f /tmp/gh.deb
```

Configure the startup hook:

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
  git config --global url."https://github.com/".insteadOf "git@github.com:"
  git config --global --add url."https://github.com/".insteadOf "ssh://git@github.com/"
fi

```

Define the token as secret. The name and the two hosts are both significant:

```yaml
# in project-specific user-config directory
GITHUB_PAT:
  value: ghp_xxxxxxxx
  hosts: [ github.com, api.github.com ]
  header_fields: [ authorization ]
```

> **Warning:** Do not name the secret `GITHUB_TOKEN` or `GH_TOKEN`. When either of those is set, `gh auth login
> --with-token` refuses to store a token and the hook fails.

`gh auth login` validates the token against the API, which is why `api.github.com` has to be in the hosts list
alongside `github.com`. `gh auth setup-git` then configures git to ask `gh` for the credential, and `gh` returns the
placeholder.

A nice side-effect of this setup is that agents can use the cli to broadly interact with github, e.g. open/merge/close
PRs.

## GitLab

Configure git's credential store directly and write the placeholder into `~/.git-credentials` from a startup hook:

```yaml
# .agents-sandbox/config.yaml
home:
  .gitlab/gitlab-auth.sh:
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
  git config --global url."https://gitlab.com/".insteadOf "git@gitlab.com:"
  git config --global --add url."https://gitlab.com/".insteadOf "ssh://git@gitlab.com/"  
fi
```

Then, configure the secret with a GitLab token:

```yaml
# in project-specific user-config directory
GITLAB_TOKEN:
  value: glpat-xxxxxxxx
  hosts: [ gitlab.com ]
  header_fields: [ authorization ]
```

GitLab accepts a personal, project, or group access token as the password with any username; `oauth2` is the
convention. The token needs the `read_repository` scope, and `write_repository` for pushing.

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

Here's an example hook that you can adapt to your git host:

```sh
#!/bin/sh
# .agents-sandbox/git-auth.sh
git config --global credential.helper store
umask 077
: > "$HOME/.git-credentials"
if [ -n "${GITLAB_TOKEN:-}" ]; then
  printf 'https://oauth2:%s@gitlab.com\n' "$GITLAB_TOKEN" >> "$HOME/.git-credentials"
  git config --global url."https://gitlab.com/".insteadOf "git@gitlab.com:"
  git config --global --add url."https://gitlab.com/".insteadOf "ssh://git@gitlab.com/" # with port: ssh://git@gitlab<.com:23/
fi  
if [ -n "${GITHUB_PAT:-}" ]; then
  printf 'https://x-access-token:%s@github.com\n' "$GITHUB_PAT" >> "$HOME/.git-credentials"
  git config --global url."https://github.com/".insteadOf "git@github.com:"
  git config --global --add url."https://github.com/".insteadOf "ssh://git@github.com/" # with port: ssh://git@github.com:23/
fi
```

The username in each line is ignored by most hosts; any non-empty value works. Give each host its own secret so the
proxy only ever substitutes that host's token.

Then, define the necessary secrets:

```yaml
# in project-specific user-config directory
GITLAB_TOKEN:
  value: glpat-xxxxxxxx
  hosts: [ gitlab.com ]
  header_fields: [ authorization ]
GITHUB_PAT:
  value: ghp_xxxxxxxx
  hosts: [ github.com, api.github.com ]
  header_fields: [ authorization ]
```

### Send a raw header instead

If a host needs the credential in a specific header rather than basic auth, set it per URL (in your hook):

```sh
git config --global http."https://gitlab.com/".extraHeader 'Authorization: Bearer $MSB_GITLAB_TOKEN'
```

This avoids the credentials file, but the server has to accept the `Bearer` scheme. GitLab's and GitHub's git
endpoints expect basic auth, which the credential store sends, so prefer the credential store unless the host
documents otherwise.