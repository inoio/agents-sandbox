import hashlib
import json5
import shlex
import subprocess
from pathlib import Path


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
    # stdout, stderr
    print(result)
    return json5.loads(result.stdout)


def build_and_load(dockerfile: Path, tag: str, force: bool = False) -> None:
    if not force and image_exists(tag):
        return
    subprocess.run(
        ["docker", "build", "-f", str(dockerfile), "-t", tag, str(Path.cwd())],
        check=True,
    )
    subprocess.run(
        f"set -o pipefail; docker save {shlex.quote(tag)} | msb load --tag {shlex.quote(tag)}",
        shell=True,
        executable="/bin/bash",
        check=True,
    )
