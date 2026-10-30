---
title: Host mounts
description: "Expose additional host directories inside the sandbox, writable or read-only."
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

The `/workspace` bind mount mirrors ordinary `rwx` permission bits set inside the VM onto the real host files (ownership
and setuid/setgid are never propagated). This keeps host git working trees consistent with the VM, which otherwise
silently drop the executable bit on rewritten files while the VM still sees a clean tree. Configured mounts keep
microsandbox's default `private` host-permissions policy; guest permission changes there do not reach the host. The
policy applies to the project VM when it is created, so an existing VM picks it up on its next recreate. Because
mirroring calls `chmod` on the real host inode, an in-VM permission change on a host file the launcher process does not
own fails instead of being virtualized.
