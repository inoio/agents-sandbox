#!/usr/bin/env python3
"""Diagnose inoio-sandbox startup time.

This script rebuilds the exact `msb run` command the launcher would use, but
runs a trivial `echo VM_BOOTED` command instead of interactive opencode. The
wall-clock time of that run is dominated by microsandbox VM boot plus image
initialization. Comparing it with the time you see for `uv run inoio-sandbox run`
tells you how much of the delay is launcher/VM overhead vs. opencode startup.
"""

import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "src"))

from inoio_sandbox import config, doctor as doctor_checks, image, runner, secrets, volumes
from inoio_sandbox import worktree as worktree_mod

DATA_DIR = Path(__file__).parent.parent / "src" / "inoio_sandbox" / "data"
DEFAULT_DOCKERFILE = DATA_DIR / "Dockerfile"
DEFAULT_PROVIDER_CONFIG = DATA_DIR / "provider-config.json"
STATE_DIR = Path.home() / ".local/share/inoio-sandbox"


def build_command():
    if not doctor_checks.check_all():
        raise SystemExit("preflight failed")

    cwd = Path.cwd()
    project = worktree_mod.project_slug()
    branch = worktree_mod.branch_name(cwd)
    current_wt = worktree_mod.current_worktree_path(cwd)
    wt = current_wt if current_wt else worktree_mod.ensure_worktree(cwd, STATE_DIR, project, branch)

    dockerfile = Path(".sandbox/Dockerfile") if Path(".sandbox/Dockerfile").exists() else DEFAULT_DOCKERFILE
    df_hash = image.dockerfile_hash(dockerfile)
    tag = image.image_tag(df_hash)
    image.build_and_load(dockerfile, tag)

    home_volume = volumes.ensure_home_volume(project, df_hash, STATE_DIR, tag)
    user_config_dir = Path.home() / ".config/inoio-sandbox/opencode"
    project_config_dir = Path(".sandbox/opencode") if Path(".sandbox/opencode").exists() else None
    config_tmp_dir = config.build_merged_config(
        user_config_dir, project_config_dir, DEFAULT_PROVIDER_CONFIG
    )
    secret_flags = secrets.secret_flags()
    cpus = runner.available_cpus()
    name = f"inoio-sandbox-{project}-{branch}"[:128]

    env_extra = []
    env_file = Path(".sandbox/env")
    if env_file.exists():
        for line in env_file.read_text().splitlines():
            line = line.strip()
            if line and "=" in line and not line.startswith("#"):
                env_extra.append(line)

    return runner.build_msb_run_command(
        image_tag=tag,
        name=name,
        worktree=wt,
        home_volume=home_volume,
        config_tmp_dir=config_tmp_dir,
        secret_flags=secret_flags,
        env_extra=env_extra,
        cpus=cpus,
        memory="4G",
    )


def replace_command(cmd: list[str], new_command: list[str]) -> list[str]:
    """Replace everything after `--` in an msb run command."""
    try:
        separator = cmd.index("--")
    except ValueError as exc:
        raise SystemExit("Could not find `--` separator in msb command") from exc
    return cmd[: separator + 1] + new_command


def main():
    print("Building msb command...")
    full_cmd = build_command()
    boot_cmd = replace_command(full_cmd, ["echo", "VM_BOOTED"])

    print("\nCommand for VM-boot-only measurement:")
    print(" \\\n  ".join(boot_cmd))
    print()

    print("Running VM-boot-only benchmark (exit after 'VM_BOOTED' prints)...")
    start = time.perf_counter()
    result = subprocess.run(boot_cmd, capture_output=True, text=True)
    elapsed = time.perf_counter() - start

    print(f"stdout: {result.stdout.strip()}")
    print(f"stderr: {result.stderr.strip()}")
    print(f"returncode: {result.returncode}")
    print(f"\nVM boot wall time: {elapsed:.3f}s")

    print("\n" + "=" * 60)
    print("For full startup timing (launcher + VM + opencode), run:")
    print("  uv run inoio-sandbox run --timing")
    print("Then exit opencode as soon as the UI appears and share the")
    print("stderr lines that start with [timing].")


if __name__ == "__main__":
    main()
