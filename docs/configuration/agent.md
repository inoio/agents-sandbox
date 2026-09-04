---
title: Agent configuration
layout: default
parent: Configuration
nav_order: 60
---

# Agent configuration

## Example: Permissions

Opencode permissions are configured through opencode config snippets, which opencode-sandbox merges (user-first, then
project; see [Agent configuration]({% link configuration/index.md %})). Place a snippet in your project, e.g.
`.opencode-sandbox/opencode/permission.json5`.

**Quasi-auto:** allow everything except what is explicitly denied:

```json5
{
  // .opencode-sandbox/opencode/permission.json5
  permission: {
    "*": "allow",
  },
}
```

**Protect secrets:** deny reads of `.env` and `.envrc` files:

```json5
{
  // .opencode-sandbox/opencode/permission.json5
  permission: {
    denylist: [
      { tool: "read", files: [".env", ".envrc"] },
    ],
  },
}
```

> **Caveat:** these rules are advisory for opencode's own Q&A tools. The `bash` tool executes arbitrary commands inside
> the VM and can read any file regardless of these deny rules, so they are not a security boundary — keep secrets out of
> the VM or rely on the [secret mechanism]({% link configuration/secrets.md %}) instead.

