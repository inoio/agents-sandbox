---
title: Opening links
description: "Links that an agent opens inside the sandbox open in your browser on the host. Which links are allowed, how logins behave, and how to turn it off."
layout: default
parent: Configuration
nav_order: 75
---

# Opening links

When an agent inside the sandbox opens a link, for example because you clicked a URL in its terminal UI, the link opens
in your browser on your own computer. This is on by default and needs no setup.

> **Note:** To turn it off, set `open-links: false` in a config file, set `AGENTS_SANDBOX_OPEN_LINKS=false`, or pass
> `--open-links=false` to `run` or `shell`. Links then are not opened at all, and the agent shows them for you to copy.

## How it works

The sandbox has no screen and no browser. Agents open links by running the `xdg-open` command, so the runner image
replaces `xdg-open` (and the `x-www-browser` and `sensible-browser` names) with a small script, and sets the `BROWSER`
environment variable to it. The script hands the link to agents-sandbox on your computer and waits up to eight seconds
for an answer. agents-sandbox checks the link and opens it with your computer's default handler: `open` on macOS,
`xdg-open` on Linux.

The hand-over uses a folder in the sandbox's home volume, not the network, so it also works with
`network.profile: none`. It works only while an agents-sandbox `run` or `shell` is attached to the sandbox. A link opened
while nothing is attached, for example from a plain `msb exec`, is not opened later.

## Which links open

Only links whose scheme can't run code from the sandbox on your computer are opened:

- web links: `http` and `https`
- email: `mailto`
- editor and app links: `vscode`, `vscode-insiders`, `cursor`, `windsurf`, `zed`, `jetbrains`, `idea`, `slack`, `linear`,
  `notion`, `figma`

Everything else is refused. That includes `file:` links and plain file paths, because on both macOS and Linux opening a
file can start a program. Links to `localhost` or `127.0.0.1` are refused too: they point at a server inside the
sandbox, which your browser can't reach.

The check runs in agents-sandbox on your computer, not in the sandbox, so a program in the sandbox can't skip it.
When a link is refused, the agent gets an error and usually shows you the link instead. After the session ends,
agents-sandbox also lists the links it refused, so you can open them yourself.

## Logging in to an agent

Agent logins and MCP server logins work through your browser on the host too. Many of them open a sign-in page and then
wait for your browser to come back to a small server the agent runs inside the sandbox, at an address like
`http://localhost:43117/callback`. Your browser can't reach the sandbox, so agents-sandbox listens on the same port on
your computer while the login is open. When your browser arrives there, agents-sandbox passes the request on to the
agent in the sandbox and shows you the agent's answer. Like the link hand-over, this doesn't use the sandbox's network.

agents-sandbox stops listening after the login completes, after five minutes, or when the session ends. It only passes
on the one request to the address the agent named. It shows you the agent's answer with scripts, cookies and other
resources blocked, and refuses an answer that would redirect your browser to a local address, so a page from the
sandbox can neither read your cookies nor reach other services on your computer. The one exception is a redirect back to
the same port the login uses, such as Codex's `/success` page: agents-sandbox answers that request itself by passing it
on to the agent too.

If you build the runner image on a custom base, that base must provide `curl`: the launcher uses it to pass the callback
into the sandbox. See the [base image contract]({% link runner-image.md %}#the-base-image-contract).

If that port is already in use on your computer, agents-sandbox can't listen on it and refuses the sign-in link. The
agent then falls back to its other login options: Claude Code and pi ask you to paste a code or the final URL, and
opencode and opencode2 offer a device-code login for some providers, and Codex offers a device-code login.

Sign-in pages that only show a code, such as device-code logins, open like any other link.

## Limits

To protect your computer from a misbehaving program in the sandbox, agents-sandbox opens at most 20 links per minute
and handles links longer than 8192 bytes as refused.
