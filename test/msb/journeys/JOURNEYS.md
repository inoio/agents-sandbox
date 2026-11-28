# msb journeys

Generated from the journey table (`Journeys`, `Exemptions`) by `TestJourneyCatalogueIsCurrent`; do not edit. Steps marked `# check` are recorded on failure and the journey goes on.
Run `go test -tags microsandbox_ffi_path ./test/msb/journeys/ -run TestJourneyCatalogueIsCurrent -args -update-catalogue` to regenerate it. See `.operator-shared/specs/testing/msb-journeys.md`.

## uc1-learn-by-using

Runs on: opencode2, oh-my-pi

Not yet:

- claude-code: learn mode on this harness waits for the product: its read-back reads only the OAuth login store; settings.json, where an endpoint and key live, is neither read back nor seeded until #821
- codex: learn mode on this harness waits for the product: its read-back reads only the auth.json login store, whose value is never read, and not config.toml, whose providers hold no key value, so no host-scoped credential is learned (#873)
- goose, grok-build, hermes, kimi-code, mistral-vibe, opencode, qwen-code: learn mode on this harness lands with the full rollout, #794
- pi: learn mode on this harness waits for the product: its read-back withholds models.json, where a custom provider's key lives, until provider headers are classified (#820, #849)

```gherkin
Scenario: uc1-learn-by-using
  Given a fresh isolated home
  And the stub provider is provisioned into the VM
  When the user runs `agents-sandbox run`
  Then the first-run menu offers learn-by-using
  When the user chooses learn-by-using in the first-run menu
  And the user leaves the harness's own setup, if it shows one
  Then the harness TUI comes up
  When the user sends the prompt "hello"
  And the user quits the harness and the learn exit prompt appears
  And the user answers the exit prompt with Save all
  # check
  Then the per-slug scope holds the learned credential
  # check
  And no project scaffolding appears
  # check
  And the first-run decision is recorded
```

## uc4-learn-from-host

Runs on: opencode2, oh-my-pi

Not yet:

- claude-code: learn mode on this harness waits for the product: its read-back reads only the OAuth login store; settings.json, where an endpoint and key live, is neither read back nor seeded until #821
- codex: learn mode on this harness waits for the product: its read-back reads only the auth.json login store, whose value is never read, and not config.toml, whose providers hold no key value, so no host-scoped credential is learned (#873)
- goose, grok-build, hermes, kimi-code, mistral-vibe, opencode, qwen-code: learn mode on this harness lands with the full rollout, #794
- pi: learn mode on this harness waits for the product: its read-back withholds models.json, where a custom provider's key lives, until provider headers are classified (#820, #849)

```gherkin
Scenario: uc4-learn-from-host
  Given a fresh isolated home
  And the stub provider is provisioned into the VM
  And the host holds the harness's own config
  When the user runs `agents-sandbox run`
  Then the first-run menu offers learn-from-host
  When the user chooses learn-from-host in the first-run menu
  And the user leaves the harness's own setup, if it shows one
  Then the harness TUI comes up
  When the user sends the prompt "hello"
  And the user quits the harness and the learn exit prompt appears
  And the user answers the exit prompt with Save all
  # check
  Then the per-slug scope holds the learned credential
  # check
  And the per-slug scope excludes the host login store
```

## config-learn-replay

Runs on: opencode2, oh-my-pi

Not yet:

- claude-code: learn mode on this harness waits for the product: its read-back reads only the OAuth login store; settings.json, where an endpoint and key live, is neither read back nor seeded until #821
- codex: learn mode on this harness waits for the product: its read-back reads only the auth.json login store, whose value is never read, and not config.toml, whose providers hold no key value, so no host-scoped credential is learned (#873)
- goose, grok-build, hermes, kimi-code, mistral-vibe, opencode, qwen-code: learn mode on this harness lands with the full rollout, #794
- pi: learn mode on this harness waits for the product: its read-back withholds models.json, where a custom provider's key lives, until provider headers are classified (#820, #849)

```gherkin
Scenario: config-learn-replay
  Given a fresh isolated home
  And the stub provider is provisioned into the VM
  And the user runs `agents-sandbox run --learn`
  And the user leaves the harness's own setup, if it shows one
  And the harness TUI comes up
  And the user quits the harness and the learn exit prompt appears
  And the user answers the exit prompt with Not now
  And a recorded learning session whose launcher is gone
  When the user runs `agents-sandbox config learn`
  And the user applies every proposal
  # check
  Then the per-slug scope holds the learned credential
  # check
  And the learn artefact is deleted
  # check
  And the learn session record is cleared
```

## attribution-observed-pair

Runs on: opencode2, oh-my-pi

Network profile: `public`

Not yet:

- claude-code: learn mode on this harness waits for the product: its read-back reads only the OAuth login store; settings.json, where an endpoint and key live, is neither read back nor seeded until #821
- codex: learn mode on this harness waits for the product: its read-back reads only the auth.json login store, whose value is never read, and not config.toml, whose providers hold no key value, so no host-scoped credential is learned (#873)
- goose, grok-build, hermes, kimi-code, mistral-vibe, opencode, qwen-code: learn mode on this harness lands with the full rollout, #794
- pi: learn mode on this harness waits for the product: its read-back withholds models.json, where a custom provider's key lives, until provider headers are classified (#820, #849)

```gherkin
Scenario: attribution-observed-pair
  Given a fresh isolated home
  And the stub provider is provisioned into the VM
  And the host holds the harness's own config
  When the user runs `agents-sandbox run --learn`
  And the user leaves the harness's own setup, if it shows one
  Then the harness TUI comes up
  When the user sends the prompt "hello"
  And the user quits the harness and the learn exit prompt appears
  And the user answers the exit prompt with Save all
  # check
  Then the per-slug scope holds the learned credential
  # check
  And the learned network policy is no wider than the run's
```

## core-session

Runs on: opencode2, claude-code, codex, pi, oh-my-pi, hermes, opencode

Network profile: `host`

Not yet:

- goose, grok-build, kimi-code, mistral-vibe, qwen-code: core session on this harness lands with the full rollout, #794

```gherkin
Scenario: core-session
  Given the journey runs on a project of its own (core)
  And a fresh isolated home
  And the stub provider is provisioned into the VM
  And the launcher is configured with the harness's stub settings
  And the stub answers the next turn by writing "journeys-core.txt"
  When the user runs `agents-sandbox run`
  Then the harness TUI comes up
  # check
  And the experimental notice matches the harness's profile
  # check
  And no project scaffolding appears
  When the user sends the prompt "journeys core first turn"
  Then the stub provider served the turn
  # check
  And "journeys-core.txt" is in the project on the host, owned by the host user
  # check
  And the harness shows the stub's reply
  When another client runs `agents-sandbox run` on the same project
  Then the harness TUI comes up
  # check
  And no reconfig prompt appears
  When the user sends the prompt "journeys core second client"
  # check
  Then the harness shows the stub's reply
  When the user switches to client 1
  And the user sends the prompt "journeys core first client again"
  # check
  Then the harness shows the stub's reply
  When the user switches to client 2
  And the user quits the harness and the launcher exits 0
  And the user switches to client 1
  And the user quits the harness and the launcher exits 0
  And the user stops the project VM
  When the user runs the harness again, resuming the last session
  Then the harness TUI comes up
  # check
  And the launcher boots the stopped VM again
  # check
  And the resumed session shows "journeys core first client again"
  When the user quits the harness and the launcher exits 0
```

## harness-login

Runs on: codex, claude-code, opencode2, pi, oh-my-pi

Network profile: `none`

Harnesses run serially (shared host callback ports).

Not yet:

- goose, grok-build, hermes, kimi-code, mistral-vibe, opencode, qwen-code: the provider-login journey covers loopback browser flows in the current adapter set; other auth modes remain in the harness rollout (#794, #875)

```gherkin
Scenario: harness-login
  Given the journey runs on a project of its own (login)
  And a fresh isolated home
  And the launcher is configured
  And the host's browser is a recorder
  When the user starts the harness's browser login
  Then the host's browser opens the login link, which names a callback in the VM
  When the browser returns to the callback after the sign-in
  # check
  Then the callback reaches the harness in the VM, behind the host boundary
  And the login command ends
  When the user signs the harness in with an API key from the shell (when an API-key login is available)
  And the user stops the project VM (when an API-key login is available)
  # check
  Then the harness reports the stored login from the shell (when an API-key login is available)
  When the user runs `agents-sandbox run` (when an API-key login is available)
  Then the harness TUI comes up (when an API-key login is available)
  # check
  And the harness starts signed in (when an API-key login is available)
  When the user quits the harness and the launcher exits 0 (when an API-key login is available)
```

## mcp-oauth-login

Runs on: opencode, codex, claude-code, opencode2, pi, oh-my-pi, hermes

Network profile: `host`

Not yet:

- goose, grok-build, kimi-code, mistral-vibe, qwen-code: the MCP-OAuth journey covers remote OAuth flows in the current adapter set; other harnesses remain in the rollout (#794, #875)

```gherkin
Scenario: mcp-oauth-login
  Given the journey runs on a project of its own (mcp)
  And a fresh isolated home
  And the host's browser is a recorder
  And an MCP server behind an OAuth authorization server is declared for the harness
  And the stub provider is provisioned into the VM
  And the launcher is configured with the harness's stub settings
  When the user signs the harness in to the MCP server
  Then the host's browser opens the login link, which names a callback in the VM
  When the user signs in at the authorization server in the browser
  Then the MCP sign-in completes with a token exchange in the VM
  When the user stops the project VM
  And only MCP calls after the VM stop count as persistence evidence
  And the user runs `agents-sandbox run`
  Then the harness TUI comes up
  # check
  And the launcher boots the stopped VM again
  When the harness checks the MCP connection when startup is lazy
  # check
  And the harness calls the MCP server with the sign-in's token
  When the user quits the harness and the launcher exits 0
```

## proxy-observes-the-stub

Runs on: opencode2

Not yet:

- claude-code, codex, goose, grok-build, hermes, kimi-code, mistral-vibe, oh-my-pi, opencode, pi, qwen-code: the proxy spike asks whether a stub route passes the learn proxy, which one harness answers (#793)

```gherkin
Scenario: proxy-observes-the-stub
  Given the run opts in with AGENTS_SANDBOX_JOURNEYS_PROXY_SPIKE
  And a fresh isolated home
  And the stub provider is provisioned into the VM
  When the user runs `agents-sandbox run --learn`
  Then the harness TUI comes up
  When the user sends the prompt "hello"
  And the user quits the harness and the learn exit prompt appears
  # check
  Then the learn proxy observed the stub's host
  When the user answers the exit prompt with Save all
```

## learn-exposed-login-key

Runs on: codex, claude-code

Not yet:

- goose, grok-build, hermes, kimi-code, mistral-vibe, oh-my-pi, opencode, opencode2, pi, qwen-code: learn mode inspects only codex's and claude-code's login stores for a plain-text API key (#873, #892)

```gherkin
Scenario: learn-exposed-login-key
  Given a fresh isolated home
  And the stub provider is provisioned into the VM
  And the harness's login store holds a plain-text API key
  When the user runs `agents-sandbox run --learn`
  Then the harness TUI comes up
  When the user quits the harness and the learn exit prompt appears
  And the user answers the exit prompt with Save all
  # check
  Then the per-slug scope holds the key as a secret
  # check
  And the VM's login store holds the placeholder, not the key
  # check
  And a later renewing login survives provisioning
  When the user resets the provisioned login's home volume
  # check
  Then the VM's login store holds the placeholder, not the key
```
