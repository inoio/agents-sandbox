import hashlib
import json5
import subprocess
from pathlib import Path

BASE_TAG = "inoio-sandbox/runner:base"


def dockerfile_hash(dockerfile: Path) -> str:
    return hashlib.sha256(dockerfile.read_bytes()).hexdigest()[:12]


def image_tag(hash_value: str) -> str:
    return f"inoio-sandbox/runner:{hash_value}"


def image_exists(tag: str) -> bool:
    result = subprocess.run(
        ["msb", "images", "--format", "json"],
        capture_output=True,
        text=True,
        check=True,
    )
    images = json5.loads(result.stdout)
    return any(img.get("reference") == tag for img in images)


def build_and_load(dockerfile: Path, tag: str, force: bool = False) -> None:
    if not force and image_exists(tag):
        return
    subprocess.run(
        ["docker", "build", "-f", str(dockerfile), "-t", tag, str(Path.cwd())],
        check=True,
    )
    docker_save = subprocess.Popen(["docker", "save", tag], stdout=subprocess.PIPE)
    try:
        subprocess.run(
            ["msb", "load", "--tag", tag],
            stdin=docker_save.stdout,
            check=True,
        )
    finally:
        if docker_save.stdout is not None:
            docker_save.stdout.close()
        docker_save.wait()
    if docker_save.returncode != 0:
        raise subprocess.CalledProcessError(docker_save.returncode, docker_save.args)


def references_base(dockerfile: Path) -> bool:
    """True if the Dockerfile has a FROM line referencing the base runner image."""
    for line in dockerfile.read_text().splitlines():
        stripped = line.lstrip()
        if stripped.startswith("FROM") and BASE_TAG in stripped:
            return True
    return False


def ensure_base_image(dockerfile: Path, force: bool = False) -> None:
    """Build and load the base runner image (tagged :base) if missing or when forced."""
    build_and_load(dockerfile, BASE_TAG, force=force)
