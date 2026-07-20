# inoio-sandbox: Resolved Open Questions

Research for Task 0 of the implementation plan.

## 1. `OPENCODE_CONFIG_CONTENT` merge behavior

**Finding:** opencode *deep-merges* `OPENCODE_CONFIG_CONTENT` with the rest of the
config stack, it does **not** wholesale-replace the `provider` section.

Evidence:

- The [Config docs](https://opencode.ai/docs/config/) state:
  "Configuration files are merged together, not replaced."
- The precedence list places `OPENCODE_CONFIG_CONTENT` (inline config) after the
global config, so it wins on conflicting keys but preserves non-conflicting
settings.
- The source code uses `mergeConfigConcatArrays` / `mergeDeep` for the inline
config branch (e.g. `packages/opencode/src/config/config.ts`).

**Launcher implication:** Pass the inoio provider fragment as-is. The only
caveat is that a user-defined provider with the same key (e.g. `litellm`) will be
overridden by the injected inoio catalog. This is acceptable for the MVP and can
be documented as a known limitation.

### Bonus finding: `{env:...}` token substitution in `OPENCODE_CONFIG_CONTENT`

Older opencode versions parsed `OPENCODE_CONFIG_CONTENT` with raw `JSON.parse`,
which skipped `{env:VAR}` substitution. That bug was fixed; current versions route
the inline content through the normal `load()`/`loadConfig()` path, so
`{env:LITELLM_API_KEY}` resolves correctly. If a user is on an older build, the
launcher may need to pre-substitute the value; for the MVP we assume a current
opencode version.

## 2. `msb --secret` with default egress

**Finding:** `msb run --secret LITELLM_API_KEY@litellm.inoio.de` works with the
default network policy; no `--no-net` + `--net-rule` dance is required.

Evidence:

- `msb run --help` says the default egress policy is `deny` with an implicit
  `allow@public` rule when no other rules are present.
- `litellm.inoio.de` resolves to a public IP (`2a01:4f8:13b:2be8::4711`), so
  it is covered by the implicit public-allow rule.
- The [microsandbox secrets docs](https://docs.microsandbox.dev/sandboxes/secrets)
  describe `--secret ENV@HOST` as binding a host env var to a guest placeholder;
  the real value is substituted at the network boundary for allowed hosts.
- For `--secret LITELLM_API_KEY@litellm.inoio.de`, the guest environment will
  expose `LITELLM_API_KEY=$MSB_LITELLM_API_KEY`. The provider fragment's
  `"apiKey": "{env:LITELLM_API_KEY}"` therefore evaluates to the placeholder,
  which msb swaps for the real key on outbound requests to `litellm.inoio.de`.

**Launcher implication:** Keep the network flags at their defaults (no explicit
`--net-rule`). Only add a rule if the secret target ever moves to a private
host.

## 3. `OPENCODE_CONFIG_CONTENT` size / env limits

**Finding:** Safe.

Measured on the local host:

```bash
$ getconf ARG_MAX
2097152
```

The spec estimates the URL-encoded provider fragment may be up to ~15 KB as an
upper bound, not a measured value. The actual `provider-config.json` example in
the implementation plan URL-encodes to only ~590 bytes. Either way, it is well
below the typical Linux `ARG_MAX` of 2 MB, so passing it as a single `-e` flag
is fine.

**Launcher implication:** No temporary-file/mount fallback needed for the MVP.
