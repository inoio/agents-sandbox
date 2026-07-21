#!/usr/bin/env python3
"""Inspect where opencode stores LSP servers inside the VM.

Run this before and after `uv run inoio-sandbox run --timing`. If pyright and
other LSP servers appear only after the opencode run, opencode installs them
lazily into the .local volume. If timestamps are always from the current run,
they are being re-installed every time.
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
image_rebuild = False

INSPECT_SCRIPT = """
echo "=== /home/dev/.opencode/bin ==="
ls -la /home/dev/.opencode/bin 2>/dev/null || echo "(not found)"
echo ""
echo "=== /home/dev/.local/share/opencode/bin ==="
ls -la /home/dev/.local/share/opencode/bin 2>/dev/null || echo "(not found)"
echo ""
echo "=== node_modules directories under /home/dev ==="
find /home/dev -type d -name node_modules -printf '  %p\\n' 2>/dev/null | head -20
echo ""
echo "=== pyright locations ==="
find /home/dev -name pyright -type d 2>/dev/null
find /tmp -name pyright -type d 2>/dev/null
echo ""
echo "=== node / bun availability ==="
which node 2>/dev/null && node --version 2>/dev/null || echo "node not found"
which bun 2>/dev/null && bun --version 2>/dev/null || echo "bun not found"
echo ""
echo "=== pyright package structure ==="
PYRIGHT_PKG=/home/dev/.cache/opencode/packages/pyright/node_modules/pyright
if [ -d "$PYRIGHT_PKG" ]; then
  ls -la "$PYRIGHT_PKG"
  echo ""
  echo "=== pyright package.json ==="
  cat "$PYRIGHT_PKG/package.json" 2>/dev/null | head -20
  echo ""
  echo "=== pyright .bin ==="
  ls -la /home/dev/.cache/opencode/packages/pyright/node_modules/.bin/ 2>/dev/null || echo "no .bin"
  echo ""
  echo "=== pyright --version (bin/pyright) ==="
  "$PYRIGHT_PKG/bin/pyright" --version 2>&1 || echo "bin/pyright failed"
  echo ""
  echo "=== pyright --version (dist/pyright.js with node) ==="
  node "$PYRIGHT_PKG/dist/pyright.js" --version 2>&1 || echo "dist/pyright.js with node failed"
  echo ""
  echo "=== pyright --version (dist/pyright.js with bun) ==="
  bun "$PYRIGHT_PKG/dist/pyright.js" --version 2>&1 || echo "dist/pyright.js with bun failed"
else
  echo "pyright package not found in cache"
fi
echo ""
echo "=== top directories by size under /home/dev/.local and /home/dev/.opencode ==="
du -sh /home/dev/.local/share/opencode/* 2>/dev/null | sort -h | tail -10
du -sh /home/dev/.opencode/* 2>/dev/null | sort -h | tail -10
echo ""
echo "=== network: resolve litellm.inoio.de ==="
getent hosts litellm.inoio.de || nslookup litellm.inoio.de 2>/dev/null || echo "resolution failed"
echo ""
echo "=== network: curl litellm.inoio.de ==="
curl -s -o /dev/null -w "%{http_code} %{time_total}s\\n" https://litellm.inoio.de/ || echo "curl failed"
echo ""
echo "=== VM environment: LITELLM_API_KEY ==="
if [ -n "${LITELLM_API_KEY:-}" ]; then
  echo "set (length: ${#LITELLM_API_KEY})"
else
  echo "not set"
fi
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
    name = f"inoio-sandbox-{project}-{worktree_mod.branch_slug(branch)}"[:128]

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
    try:
        separator = cmd.index("--")
    except ValueError as exc:
        raise SystemExit("Could not find `--` separator in msb command") from exc
    return cmd[: separator + 1] + new_command


def main():
    print("Building msb command...")
    full_cmd = build_command()
    inspect_cmd = replace_command(full_cmd, ["sh", "-c", INSPECT_SCRIPT])

    print("\nInspecting LSP locations inside fresh VM...")
    print("=" * 60)
    start = time.perf_counter()
    result = subprocess.run(inspect_cmd, capture_output=True, text=True)
    elapsed = time.perf_counter() - start

    print(result.stdout)
    if result.stderr:
        print("stderr:", result.stderr)
    print(f"Inspection completed in {elapsed:.3f}s (returncode {result.returncode})")

    print("\n" + "=" * 60)
    print("Next steps:")
    print("1. Run: uv run inoio-sandbox run --timing")
    print("2. Exit opencode as soon as the UI appears.")
    print("3. Run this script again and compare pyright locations/timestamps.")


if __name__ == "__main__":
    main()
