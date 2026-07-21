#!/usr/bin/env python3
"""Test whether symlinking cache packages to .local/bin/node_modules fixes LSP activation.

This runs the same setup as the launcher, but before starting opencode it creates:
  /home/dev/.local/share/opencode/bin/node_modules -> /home/dev/.cache/opencode/packages

If LSPs now initialize when editing Python files, opencode expects packages in
.local/share/opencode/bin/node_modules and we can make this permanent in the launcher.
"""

import shutil
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "src"))

from inoio_sandbox import config, doctor as doctor_checks, image, runner, secrets, volumes
from inoio_sandbox import worktree as worktree_mod

DATA_DIR = Path(__file__).parent.parent / "src" / "inoio_sandbox" / "data"
DEFAULT_DOCKERFILE = DATA_DIR / "Dockerfile"
DEFAULT_PROVIDER_CONFIG = DATA_DIR / "provider-config.json"
STATE_DIR = Path.home() / ".local/share/inoio-sandbox"
image_rebuild = False

INIT_OPENCODE = """
mkdir -p /home/dev/.local/share/opencode/bin
ln -sf /home/dev/.cache/opencode/packages /home/dev/.local/share/opencode/bin/node_modules
ls -la /home/dev/.local/share/opencode/bin/node_modules
exec opencode
"""


def build_command():
    if not doctor_checks.check_all():
        raise SystemExit("preflight failed")

    cwd = Path.cwd()
    project = worktree_mod.project_slug()
    branch = worktree_mod.branch_name(cwd)
    current_wt = worktree_mod.current_worktree_path(cwd)
    wt = current_wt if current_wt else worktree_mod.ensure_worktree(cwd, STATE_DIR, project, branch)

    dockerfile = Path(".sandbox/Dockerfile") if Path(".sandbox/Dockerfile").exists() else DEFAULT_DOCKERFILE
    if image.references_base(dockerfile):
        image.ensure_base_image(DEFAULT_DOCKERFILE, force=image_rebuild)
    df_hash = image.dockerfile_hash(dockerfile)
    tag = image.image_tag(df_hash)
    image.build_and_load(dockerfile, tag, force=image_rebuild)

    home_volume = volumes.ensure_home_volume(project, df_hash, STATE_DIR, tag)
    user_config_dir = Path.home() / ".config/inoio-sandbox/opencode"
    project_config_dir = Path(".sandbox/opencode") if Path(".sandbox/opencode").exists() else None
    config_tmp_dir = config.build_merged_config(user_config_dir, project_config_dir, DEFAULT_PROVIDER_CONFIG)
    secret_flags = secrets.secret_flags()
    cpus = runner.available_cpus()
    name = f"inoio-sandbox-{project}-{worktree_mod.branch_slug(branch)}-lsp-test"[:128]

    env_extra = []
    env_file = Path(".sandbox/env")
    if env_file.exists():
        for line in env_file.read_text().splitlines():
            line = line.strip()
            if line and "=" in line and not line.startswith("#"):
                env_extra.append(line)

    return (
        runner.build_msb_run_command(
            image_tag=tag,
            name=name,
            worktree=wt,
            home_volume=home_volume,
            config_tmp_dir=config_tmp_dir,
            secret_flags=secret_flags,
            env_extra=env_extra,
            cpus=cpus,
            memory="4G",
        ),
        config_tmp_dir,
    )


def replace_command(cmd: list[str], new_command: list[str]) -> list[str]:
    try:
        separator = cmd.index("--")
    except ValueError as exc:
        raise SystemExit("Could not find `--` separator in msb command") from exc
    return cmd[: separator + 1] + new_command


def main():
    print("Building msb command...")
    full_cmd, config_tmp_dir = build_command()
    test_cmd = replace_command(full_cmd, ["sh", "-c", INIT_OPENCODE])

    print("\nStarting opencode with LSP cache symlink...")
    print("=" * 60)
    try:
        result = subprocess.run(test_cmd)
    except KeyboardInterrupt:
        result = subprocess.CompletedProcess(args=test_cmd, returncode=130)
    finally:
        shutil.rmtree(config_tmp_dir, ignore_errors=True)
    sys.exit(result.returncode)


if __name__ == "__main__":
    main()
