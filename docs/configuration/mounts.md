---
title: Host mounts
layout: default
parent: Configuration
nav_order: 40
---

# Host mounts

The `mounts` map exposes additional host directories inside the sandbox. Each key is an absolute guest target and its
value is either the host source directory or a mapping with `source` and optional `readonly`. Sources may be absolute or
start with `~/`. Mounts are writable by default; set `readonly: true` when the sandbox must not modify the host directory.

```yaml
mounts:
  /home/dev/.m2: ~/.m2
  /home/dev/reference:
    source: /opt/company/reference
    readonly: true
```

Configured source directories must already exist on the host and must be directories. The managed mount targets
`/home/dev`, `/workspace`, and `/tmp` cannot be replaced, and a target may not shadow a parent of them. Nesting a mount
inside `/workspace` or `/tmp` is rejected because it would hide managed content; nesting inside `/home/dev` is allowed
and is the common case. A mount configuration change recreates the project VM. Writable mounts let sandbox processes
modify host files directly, so only mount directories whose contents may be changed by sandboxed tools.
