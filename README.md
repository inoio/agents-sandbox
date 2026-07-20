# inoio-sandbox

Run opencode inside an ephemeral microsandbox VM.

## Install

```bash
./install.sh
```

## Usage

```bash
opencode          # alias for inoio-sandbox run
inoio-sandbox doctor
inoio-sandbox run --worktree my-feature
```

## Project overrides

Create `.sandbox/Dockerfile` to override the runner image.
Create `.sandbox/env` to add environment variables.
