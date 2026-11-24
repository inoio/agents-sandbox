# msb journeys

Generated from the journey table (`Journeys`, `Exemptions`) by `TestJourneyCatalogueIsCurrent`; do not edit. Steps marked `# check` are recorded on failure and the journey goes on.
Run `go test -tags microsandbox_ffi_path ./test/msb/journeys/ -run TestJourneyCatalogueIsCurrent -args -update-catalogue` to regenerate it. See `.operator-shared/specs/testing/msb-journeys.md`.

## uc1-learn-by-using

Runs on: opencode2, oh-my-pi

Not yet:

- claude-code: learn mode on this harness waits for the product: its read-back reads only the OAuth login store; settings.json, where an endpoint and key live, is neither read back nor seeded until #821
- codex: learn mode on this harness waits for the product: its read-back reads only the auth.json login store, whose value is never read, and config.toml names no key, so no host-scoped credential is learned
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
- codex: learn mode on this harness waits for the product: its read-back reads only the auth.json login store, whose value is never read, and config.toml names no key, so no host-scoped credential is learned
- goose, grok-build, hermes, kimi-code, mistral-vibe, opencode, qwen-code: learn mode on this harness lands with the full rollout, #794
- pi: learn mode on this harness waits for the product: its read-back withholds models.json, where a custom provider's key lives, until provider headers are classified (#820, #849)

```gherkin
Scenario: uc4-learn-from-host
  Given a fresh isolated home
  And the stub provider is provisioned into the VM
  And the harness's own host config points at the stub
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
- codex: learn mode on this harness waits for the product: its read-back reads only the auth.json login store, whose value is never read, and config.toml names no key, so no host-scoped credential is learned
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
- codex: learn mode on this harness waits for the product: its read-back reads only the auth.json login store, whose value is never read, and config.toml names no key, so no host-scoped credential is learned
- goose, grok-build, hermes, kimi-code, mistral-vibe, opencode, qwen-code: learn mode on this harness lands with the full rollout, #794
- pi: learn mode on this harness waits for the product: its read-back withholds models.json, where a custom provider's key lives, until provider headers are classified (#820, #849)

```gherkin
Scenario: attribution-observed-pair
  Given a fresh isolated home
  And the stub provider is provisioned into the VM
  And the harness's own host config points at the stub
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

Runs on: opencode2, claude-code, codex, pi, oh-my-pi

Network profile: `host`

Not yet:

- goose, grok-build, hermes, kimi-code, mistral-vibe, opencode, qwen-code: core session on this harness lands with the full rollout, #794

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
