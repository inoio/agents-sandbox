from pathlib import Path

import psutil


VM_ENV = [
    "HOME=/home/dev",
    "NODE_ENV=development",
    "SANDBOX_USER=dev",
    "SHELL=/bin/bash",
]


def available_cpus() -> int:
    return psutil.cpu_count() or 1


def available_memory_gib() -> int:
    return max(1, psutil.virtual_memory().total // (1024**3))


def _envrc_files(worktree: Path) -> list[str]:
    return sorted(path.name for path in worktree.glob(".envrc*"))


def build_msb_run_command(
    image_tag: str,
    name: str,
    worktree: Path,
    home_volume: str | Path,
    config_tmp_dir: Path,
    secret_flags: list[str],
    env_extra: list[str],
    cpus: int,
    memory: str,
) -> list[str]:
    max_cpus = available_cpus()
    max_memory_gib = available_memory_gib()

    cmd = [
        "msb",
        "run",
        "-t",
        "--log-level",
        "debug",
        "--replace",
        "--name",
        name,
        "-c",
        str(cpus),
        "--max-cpus",
        str(max_cpus),
        "-m",
        memory,
        "--max-memory",
        f"{max_memory_gib}G",
        "-u",
        "dev",
        "-v",
        f"{home_volume}:/home/dev",
        "--copy-dir",
        f"{config_tmp_dir}:/tmp/inject/opencode",
        "--script",
        "setup=mkdir -p /home/dev/.config/opencode && cp -r /tmp/inject/opencode/. /home/dev/.config/opencode/ && (chown -R dev:dev /home/dev/.config/opencode || true)",
        "-w",
        "/home/dev/workspace",
    ]
    cmd.extend(secret_flags)
    for env in VM_ENV + env_extra:
        cmd.extend(["-e", env])
    for envrc in _envrc_files(worktree):
        cmd.extend(["--rm", f"/home/dev/workspace/{envrc}"])
    cmd.extend([image_tag, "--", "opencode"])
    return cmd
