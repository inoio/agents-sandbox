import shutil
import subprocess
from pathlib import Path

from inoio_sandbox import log


def home_volume_name(project_slug: str, image_hash: str) -> str:
    return f"{project_slug}-opencode-home-{image_hash}"


def ensure_msb_volume(name: str) -> bool:
    """Create an msb volume. Return True if newly created, False if it exists."""
    try:
        result = subprocess.run(
            ["msb", "volume", "create", name],
            capture_output=True,
        )
    except FileNotFoundError as exc:
        raise RuntimeError(
            "msb not found. Install microsandbox: https://github.com/microsandbox/microsandbox"
        ) from exc
    if result.returncode == 0:
        return True
    if b"already exists" in result.stderr:
        return False
    raise RuntimeError(f"msb volume create failed: {result.stderr.decode()}")


def prefill_home_volume(name_or_path: str, image_tag: str) -> None:
    subprocess.run(
        [
            "msb", "run",
            "-v", f"{name_or_path}:/mnt/home",
            image_tag,
            "--", "/bin/sh", "-c",
            "cp -a /home/dev/. /mnt/home/ && chown -R dev:dev /mnt/home",
        ],
        check=True,
    )


def remove_home_volume(name: str) -> None:
    subprocess.run(
        ["msb", "volume", "remove", name],
        capture_output=True,
    )


def fallback_home_path(state_dir: Path, project_slug: str, image_hash: str) -> Path:
    return state_dir / "state" / project_slug / "home" / image_hash


def ensure_home_volume(
    project_slug: str,
    image_hash: str,
    state_dir: Path,
    image_tag: str,
    fallback: bool = False,
    reset: bool = False,
) -> str | Path:
    name = home_volume_name(project_slug, image_hash)
    if fallback:
        path = fallback_home_path(state_dir, project_slug, image_hash)
        if reset and path.exists():
            shutil.rmtree(path)
        path.mkdir(parents=True, exist_ok=True)
        if not any(path.iterdir()):
            prefill_home_volume(str(path), image_tag)
        return path

    if reset:
        remove_home_volume(name)

    try:
        created = ensure_msb_volume(name)
    except RuntimeError:
        log.warn("msb volume creation failed; using host-directory fallback.")
        return ensure_home_volume(
            project_slug, image_hash, state_dir, image_tag, fallback=True
        )

    if created:
        prefill_home_volume(name, image_tag)
    return name
