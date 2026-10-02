# Security Policy

## Reporting a vulnerability

If you find a security vulnerability, **do not open a public issue**. Report it privately to:

<opensource@inoio.de>

Please include:

- The version of `agents-sandbox` (`agents-sandbox version`)
- Your host platform (Linux x86_64 / Linux arm64 / macOS arm64)
- A description of the issue and its security impact
- Steps to reproduce or a minimal proof of concept
- Any relevant configuration or environment variables you use

You can expect an acknowledgement within a few days and a coordinated fix before public disclosure.

## Supported versions

Security fixes are provided for the **latest release** only. Older releases are not patched; upgrade to the current
release to receive fixes. See [Releases](https://github.com/inoio/agents-sandbox/releases).

## Security-relevant project surface

- Raw values configured through `env.secret` or `env.secret.yaml` are passed into VMs only via microsandbox's secret mechanism
  (along with `OPENCODE_SERVER_PASSWORD` / `OPENCODE_SERVER_USERNAME` for basic auth on the served opencode daemon). They are
  never logged or written to the project volume. Native credential files are copied into the VM only when host-config
  provisioning is explicitly enabled with `provision-host-config: true`; see the [agent configuration documentation](docs/configuration/agent.md).
- The served opencode port is bound to the host loopback only and never exposed on the LAN.
- The project directory is mounted as `/workspace` inside the VM; treat anything the VM writes as affecting your
  project tree.
- **Known limitation:** `.env(rc)` secrets located in the project directory are not yet hidden from the VM. Avoid
  placing secrets in `.env` files inside a project you run through the sandbox.

## Reporting abuse

For non-security issues (abuse, harassment, behavior violations), see the
[Code of Conduct](CODE_OF_CONDUCT.md) reporting path.
