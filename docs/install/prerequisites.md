---
title: Prerequisites
description: "Supported platforms and required hardware for agents-sandbox: Linux with KVM, macOS on Apple Silicon or Windows (experimental), plus the microsandbox runtime."
layout: default
parent: Install
nav_order: 10
---

# Prerequisites

## Supported platforms

agents-sandbox supports the following platforms:

* Linux (KVM)
* macOS (Apple Silicon)
* Windows (experimental)

## Software prerequisites

agents-sandbox needs no separate container runtime. Runner images are built inside a microsandbox VM, and the
microsandbox runtime (`msb`) is installed and managed by agents-sandbox itself on first use. The host only has to
provide hardware virtualization and outbound network access.

## Doctor check

If you're unsure your system fulfills the prerequisites, you can just install agents-sandbox and use it to verify your
setup. It will tell you if your system is ready to use agents-sandbox.

```shell
agents-sandbox doctor
```
