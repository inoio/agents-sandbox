---
title: Installation
layout: default
parent: Install
nav_order: 20
---

## Installation

* Via Homebrew (macOS and Linux):

  ```console
  brew tap inoio/agents-sandbox https://github.com/inoio/agents-sandbox
  brew install agents-sandbox
  ```

  Updates ship as ordinary releases; to get one:

  ```console
  brew update && brew upgrade agents-sandbox
  ```

  Homebrew installs are detected automatically: the launcher disables its
  [self-upgrade]({% link configuration/self-upgrade.md %}) so Homebrew's version bookkeeping stays accurate, and
  `agents-sandbox upgrade` points you back to `brew upgrade`.

* Or download the latest binary:

  **Linux (x86_64):**

  ```console
  curl -L -o agents-sandbox https://github.com/inoio/agents-sandbox/releases/latest/download/agents-sandbox-linux-amd64
  ```

  **macOS (Apple Silicon):**

  ```console
  curl -L -o agents-sandbox https://github.com/inoio/agents-sandbox/releases/latest/download/agents-sandbox-darwin-arm64
  ```

  **Linux (arm64):**

  ```console
  curl -L -o agents-sandbox https://github.com/inoio/agents-sandbox/releases/latest/download/agents-sandbox-linux-arm64
  ```
* Install:
  ```console
  chmod u+x agents-sandbox
  mv agents-sandbox ~/.local/bin # or any other directory in your PATH 
  ```
