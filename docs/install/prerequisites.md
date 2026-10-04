---
title: Prerequisites
description: "Supported platforms and required software for agents-sandbox: Linux with KVM, macOS on Apple Silicon or Windows (experimental), plus Docker."
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

agents-sandbox requires the following pre-installed software:

- **Docker**, **Docker Desktop** or **colima** for building VM images

The Docker endpoint is resolved like the `docker` CLI resolves it: `DOCKER_HOST` first, then the active
docker context (`docker context ls`), then the default `/var/run/docker.sock`. A colima or Docker Desktop
install that publishes its socket through a docker context therefore needs no extra configuration.

## Doctor check

If you're unsure your system fulfills the prerequisites, you can just install agents-sandbox and use it to verify your
setup. It will tell you if your system is ready to use agents-sandbox.

```shell
agents-sandbox doctor
```
