---
title: Development
description: "How to set up a development environment."
layout: default
nav_order: 200
---

# Development

This section describes how you can set up a development environment for working on agents-sandbox.

## Prerequisites

[Goenv](https://github.com/go-nv/goenv) is required to manage the pinned Go version. You can
[install](https://github.com/go-nv/goenv/blob/master/INSTALL.md) it via homebrew or manually.

## Setup

Once goenv is installed, run `make bootstrap` to install:

- the Go version pinned in `.go-version`
- golangci-lint
- [Zig](https://ziglang.org/) for cross-compilation, which enables the cross platform build target `make build-release-all`
