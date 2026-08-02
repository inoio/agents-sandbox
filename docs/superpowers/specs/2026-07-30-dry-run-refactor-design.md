# Dry-Run Flag Refactor Design

## Goal

Refactor `--dry-run` and `--dry-run-vm` flags to have clear, consistent semantics across all commands, with `--dry-run` implying `--dry-run-vm`, and consolidate flag availability to only commands where they make sense.

## Flag Semantics

### --dry-run
Skips all "writing" operations:
- Building Docker/MSB images
- User prompts (confirmation dialogs)
- Running commands inside VMs (opencode, shell)

### --dry-run-vm
Skips all VM lifecycle operations:
- Creating VMs
- Starting VMs
- Connecting to VMs
- Stopping/killing VMs
- Executing commands inside VMs

### Implication Rule
`--dry-run` implies `--dry-run-vm`. When `--dry-run` is set, `--dry-run-vm` is automatically enabled. This logic is centralized in `cli.go` flag handling code.

## Flag Availability by Command

| Command | --dry-run | --dry-run-vm | Rationale |
|---------|-----------|--------------|-----------|
| `run` | ✅ | ✅ | Full workflow: validate image build, VM lifecycle, and opencode execution |
| `shell` | ✅ | ✅ | Same as run - validate setup and show what shell command would run |
| `build` | ✅ | ❌ | Only builds images, no VM operations. `--dry-run-vm` had no effect. |
| `stop` | ✅ | ❌ | Stop IS a VM operation; distinction between flags is meaningless. |
| `kill` | ✅ | ❌ | Kill IS a VM operation; distinction between flags is meaningless. |
| `prune` | ✅ | ❌ | Keep it simple - one flag for all prune operations. |
| `list` / `ls` | ❌ | ❌ | Read-only command, no mutations. |
| `config show` | ❌ | ❌ | Read-only command, no mutations. |
| `image list` | ❌ | ❌ | Read-only command, no mutations. |
| `volume list` | ❌ | ❌ | Read-only command, no mutations. |
| `doctor` | ❌ | ❌ | Read-only command, no mutations. |

## Message Format

All dry-run messages use consistent format:
```
dry-run: Would <action> <target>
```

Examples:
- `dry-run: Would build Docker image "opencode-msb/runner-base:latest"`
- `dry-run: Would start VM "opencode-msb-vm-myproject"`
- `dry-run: Would run opencode in VM`
- `dry-run: Would stop project VM "opencode-msb-vm-myproject"`
- `dry-run: Would prune 3 VMs, 2 volumes, 5 images`

## Code Structure

Each operation that supports dry-run uses clear if/else branching:

```go
if dryRun {
    logger.Infof("dry-run: Would build Docker image %q", tag)
} else {
    // Actual operation
    err := buildDockerImage(...)
    if err != nil {
        return err
    }
}
```

## Implementation Requirements

### cli.go Changes

1. **Flag implication logic**: After parsing flags, if `dryRun` is true, set `dryRunVM = true`.

2. **Remove --dry-run-vm from commands**: Remove the flag from `build`, `stop`, `kill`, and `prune` commands.

3. **Keep both flags on**: `run` and `shell` commands keep both flags.

### sandbox package Changes

1. **Standardize message format**: Update all dry-run messages to use `"dry-run: Would ..."` format.

2. **Clarify if/else branches**: Ensure each dry-run check is a clear if/else with the actual operation in the else branch.

3. **Function signatures**: Update function signatures to remove `dryRunVM` parameter where no longer needed.

## Backwards Compatibility

This is a breaking change for users who:
- Used `--dry-run-vm` with `build`, `stop`, `kill`, or `prune` commands

Migration:
- For `build`: Use `--dry-run` instead (behavior is identical)
- For `stop`/`kill`: Use `--dry-run` instead (behavior is identical)
- For `prune`: Use `--dry-run` instead (VMs will now also be shown as "would delete")

## Testing

1. Verify flag implication works correctly in `run` and `shell`
2. Verify removed flags no longer appear in help text
3. Verify message format is consistent across all operations
4. Verify read-only commands don't have dry-run flags
