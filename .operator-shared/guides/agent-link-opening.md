---
description: How the coding agents open links (what each shells out to on Linux, claude-code's own scheme allowlist and headless guard, the OSC 8 wrinkle, codex), the OAuth return-leg problem per agent, plus the network-free VM→host transport constraint for any host-side opener.
read_if: Working on opening links/URLs on the host from inside the VM (issue #273), an in-VM `xdg-open` shim, or any VM→host action channel.
---

# Agent link opening and the VM→host opener channel

Findings for issue #273 ("Open links from the agent TUI on the host"). Read from the shipped
binaries with `strings` — refresh by re-running `strings -n 6 <binary> | rg -i 'xdg-open|openUrl|openPath'`.
Observed against: opencode v1 `1.19.3` (npm `opencode-ai`), opencode2 (the `opencode2` binary in
the dev VM), claude-code `2.1.308`, pi `1.4.0`.

## What each agent runs on Linux

- **opencode (v1)** — TUI link click: `Bun.spawn(["xdg-open", url])`, no scheme check. Its
  GitHub-app install (`xdg-open "<url>"`) and GitLab/OAuth flows also shell out to `xdg-open`.
- **opencode2** — bundles the `open` npm package. `openUrl` rejects anything that is not
  `http:`/`https:`; `openPath` accepts any path or URL. Both reach `xdg-open` on Linux
  (bundled copy preferred, else `PATH`).
- **pi** — `openBrowser(target)` (its own helper) runs `xdg-open <target>` with no restriction;
  called for the login-dialog auth URL and wired as the markdown renderer's `openUrl`, so TUI
  link clicks go through it. The helper never invokes a shell (deliberate, to avoid URL
  injection).
- **claude-code** — its own opener with a **scheme allowlist** in code: `http`, `https`,
  `vscode:`, `vscode-insiders:`, `cursor:`, `windsurf:`, `zed:`, `jetbrains:`, `idea:`,
  `slack:`, `linear:`, `notion:`, `figma:`. `file:` is routed to the file manager (dbus
  `FileManager1.ShowItems`); other schemes are refused with a warning. Two behaviours matter for
  any shim: it honours `$BROWSER` (spawning that instead of `xdg-open`; a `browser` setting
  overrides it), and the headless guard runs only when no browser is set —
  `if(!browser && headless) return {reason:"no_display"}` (verified in `2.1.289`). So
  `BROWSER=xdg-open` alone gets past the guard; `DISPLAY` is not needed. It also
  has an `openByHand` fallback that prints a `! xdg-open <url>` command for the user to run.
- **codex** — Rust `webbrowser` crate (`1.2.4`): tries each `$BROWSER` entry first, then
  `xdg-open`, no display check. Used for TUI link clicks, the ChatGPT login, and MCP OAuth.

## The OSC 8 wrinkle

All four also emit OSC 8 hyperlinks (`\x1b]8;;URL`), which the *host* terminal handles — so
link rendering is not the gap. It is not sufficient either: the TUIs capture mouse input for
their own clickable UI, so the terminal never receives the click and the TUI's own opener is
what runs. OSC 8 also cannot cover URLs opened programmatically (OAuth/login flows).

## Transport constraint: keep it off the network

Any guest→host action channel must not use guest egress: the default `network: none` profile
blocks it, and this must work out of the box. microsandbox's `host` network profile exists but
is not the default.

Network-free path — the per-project **home volume** is already mounted at `/home/dev` and,
being a local microsandbox volume, exposes its host-side path to the launcher via
`VolumeHandle.Path()` (see `internal/sandbox/volume/inspect.go`). So an in-VM shim can write a
request into a spool directory under `$HOME`, and a launcher-owned watcher reads it directly
from the volume's host path. No guest egress, no listening socket, no new mount; a launcher
bind-mounted spool dir is the fallback if the volume path is not directly readable (one extra
mount, a one-time VM recreate).

The spool path is guest-writable, so every host-side access is hardened against what the guest can
plant there: the directory is opened with `O_DIRECTORY|O_NONBLOCK` (a FIFO would otherwise block the
open forever and hang the watcher, which session shutdown waits on) and each request is opened with
`O_NONBLOCK|O_NOFOLLOW` plus a regular-file check before it is read.

Direction note: the existing `internal/notify` watcher is the opposite direction — the host
reads the in-VM daemon's SSE stream through the msb exec channel (`sb.ShellStream`,
host→guest), which needs no guest egress. An opener is the launcher's first VM→host *action*
channel.

## OAuth login flows: the return leg

Opening the auth URL on the host is only half a loopback OAuth flow (RFC 8252). The agent
listens on `127.0.0.1:<port>` *inside the VM* and puts `redirect_uri=http://localhost:<port>/…`
into the auth URL, so after a host-side login the browser redirects to the host's loopback,
where nothing listens. Every loopback flow below carries its `redirect_uri` as a query
parameter of the opened URL, so a host-side opener can detect it. Observed against
claude-code `2.1.289`, opencode v1 `1.19.4`, opencode2 source `7b5b7a8`, pi `1.4.0`
(`@earendil-works/pi-ai`), codex source (`openai/codex` main, Oct 2026).

| Agent / flow | Callback port | Fallback without a working callback |
|---|---|---|
| claude-code login | random (`listen(0)`), `redirect_uri=localhost:<port>/callback` | Opens the loopback URL, *and* shows a second "manual" URL (`platform.claude.com/oauth/code/callback`) with a "Paste code here" prompt. The code from the loopback URL is not accepted there (`redirect_uri` mismatch at token exchange). |
| claude-code MCP OAuth | random 49152–65535, fallback 3118, `MCP_OAUTH_CALLBACK_PORT` | Asks for the full redirect URL from the address bar. |
| pi Anthropic | 53692, free port if taken | Races callback against a paste prompt that accepts the redirect URL; also a copy-code method. |
| pi OpenAI Codex / ChatGPT | 1455 | Paste of the redirect URL; device-code method. |
| pi GitHub Copilot, Radius | — | Device code (no callback). |
| opencode v1 OpenAI/Codex | fixed `jr` (codex plugin) | None in the browser method; device-code method exists. |
| opencode v1 MCP OAuth | 19876 (`oauth.callbackPort`) | None. |
| opencode2 ChatGPT / OpenAI browser | random (`listen(0)`) | None (`mode: "auto"`); OpenAI has a legacy device-code method. |
| opencode2 GitLab | 8080 (must match registration) | None. |
| opencode2 MCP OAuth | `callback_port`, else `redirect_uri` port, else random | None. |
| opencode2 opencode / Copilot / xAI | — | Device code. |
| codex ChatGPT login | 1455, fallback 1457 | None in the browser flow; "Device code" sign-in option (`--device-auth`). |

Consequences for a host opener:

- **Device-code flows** work end to end with a plain opener (the verification page is a normal
  `https` link).
- **Loopback flows with a paste fallback** (claude-code, pi) work today by hand. A naive opener
  that reports success makes claude-code worse: the opened tab ends on a dead
  `localhost:<port>` page after a successful login, and its code is useless in the paste
  prompt. Refusing loopback-redirect URLs (exit nonzero) keeps today's behaviour.
- **Loopback flows without a fallback** (opencode MCP/GitLab/ChatGPT browser, codex browser
  login) are broken today and stay broken unless the return leg is forwarded.

Implemented in `internal/opener` (#273): `ClassifyTarget` detects the loopback `redirect_uri`,
`CallbackForwarder` forwards it, and a failed bind falls back to refusing the URL (exit 4), which
keeps the agents' paste/device-code fallbacks. Return-leg forwarding: the watcher detects a loopback `redirect_uri`, listens on the host's
`127.0.0.1` and `::1` at that port (browsers may resolve `localhost` to `::1`; the agents bind
`127.0.0.1` in the VM), and replays the single callback GET inside the VM through the msb exec
channel (`curl -i http://127.0.0.1:<port><path>?<query>`), relaying the response. It needs no
guest egress or published port, and random ports are fine because the port comes from the URL.

Host-browser boundary of the return leg: the guest's response is untrusted, so the forwarder drops
`Set-Cookie`/`Clear-Site-Data` (host cookies are not port-separated) and serves the page with
`Content-Security-Policy: sandbox; default-src 'none'` + `nosniff` — `sandbox` removes scripts and
gives an opaque origin, `default-src 'none'` also blocks subresources, so guest HTML cannot make the
host browser fetch a host-local address (an `<img src="http://127.0.0.1:…">`, say). It relays the
guest's `Location` only when it is an absolute `http(s)` URL to a host other than the host's own
loopback; a loopback, ambiguous-numeric, relative, or non-web target is replaced with a local notice,
because following it would reach another service listening on the host — the same boundary the
`redirect_uri` check guards; that check covers the `Location` header, not resource loads, which is
why the CSP must block them. A second login on a port already being forwarded updates the served
callback path and restarts the forward's lifetime instead of rebinding, so a new path is not 404'd
by an unfinished earlier login and the new login gets the full window rather than the earlier
login's nearly expired deadline. Reuse also hands ownership of the listener to the new login: an
earlier callback still being answered in the guest does not close the listener when it returns, so
it cannot cut off the browser callback the new login is still waiting for.

The replay needs guest `curl`. The managed base provides it and the daemon agents already require
it; because the opener is on by default, every runner image now verifies `curl` in the final stage
(`launcherGuestTools` in `internal/sandbox/image/dockerfile.go`), so a custom base without `curl`
fails the image build naming the tool instead of failing at login. Documented in
`docs/runner-image.md` (base image contract) and `docs/configuration/opening-links.md`.
