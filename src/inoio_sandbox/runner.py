from pathlib import Path


def build_msb_run_command(
    image_tag: str,
    worktree: Path,
    local: str | Path,
    cache: str | Path,
    config_content: str,
    secret_flags: list[str],
    env_extra: list[str],
) -> list[str]:
    cmd = [
        "msb",
        "run",
        "-v",
        f"{worktree}:/home/dev/workspace",
        "-v",
        f"{Path.home() / '.config/opencode'}:/home/dev/.config/opencode:ro",
        "-v",
        f"{local}:/home/dev/.local",
        "-v",
        f"{cache}:/home/dev/.cache",
        "-w",
        "/home/dev/workspace",
        "-e",
        config_content,
    ]
    cmd.extend(secret_flags)
    for env in env_extra:
        cmd.extend(["-e", env])
    cmd.extend([image_tag, "--", "opencode"])
    return cmd
