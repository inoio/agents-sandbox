import subprocess
from pathlib import Path

import click


def local_volume_name(project_slug: str) -> str:
    return f"{project_slug}-opencode-local"


def cache_volume_name(project_slug: str) -> str:
    return f"{project_slug}-opencode-cache"


def ensure_msb_volume(name: str) -> bool:
    result = subprocess.run(
        ["msb", "volume", "create", name],
        capture_output=True,
    )
    return result.returncode == 0 or b"already exists" in result.stderr


def fallback_paths(state_dir: Path, project_slug: str) -> tuple[Path, Path]:
    base = state_dir / project_slug
    return base / "local", base / "cache"


def ensure_volumes(
    project_slug: str,
    state_dir: Path,
    fallback: bool = False,
) -> tuple[str | Path, str | Path]:
    local = local_volume_name(project_slug)
    cache = cache_volume_name(project_slug)
    if fallback:
        return fallback_paths(state_dir, project_slug)
    if not ensure_msb_volume(local) or not ensure_msb_volume(cache):
        click.echo("msb volume creation failed; using host-directory fallback.", err=True)
        return fallback_paths(state_dir, project_slug)
    return local, cache
