# inoio-sandbox Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a standalone Python/click launcher that runs opencode inside an ephemeral microsandbox VM, binding the project as a git worktree and persisting opencode state in msb named volumes.

**Architecture:** A click CLI (`inoio-sandbox`) delegates to focused modules (`doctor`, `image`, `worktree`, `volumes`, `config`, `secrets`, `runner`). It builds/loads a runner image, ensures a branch worktree and two msb named volumes, then execs `msb run` with read-only host config, named state/cache volumes, provider config injected via `OPENCODE_CONFIG_CONTENT`, and secrets via `msb --secret`.

**Tech Stack:** Python 3.10+, click, pytest, ruff; microsandbox CLI, Docker, git; Debian-based runner image with opencode.

---

## File Structure

| File | Responsibility |
|---|---|
| `pyproject.toml` | Package metadata, dependencies, test/lint scripts |
| `src/inoio_sandbox/__init__.py` | Package marker |
| `src/inoio_sandbox/cli.py` | Click entry point and subcommands (`run`, `doctor`) |
| `src/inoio_sandbox/doctor.py` | Preflight checks for msb, docker, git, /dev/kvm |
| `src/inoio_sandbox/image.py` | Hash Dockerfile, build, load into msb |
| `src/inoio_sandbox/worktree.py` | Resolve project slug + branch, find/create worktree |
| `src/inoio_sandbox/volumes.py` | Ensure msb named volumes; host-dir fallback |
| `src/inoio_sandbox/config.py` | Load provider fragment and build `OPENCODE_CONFIG_CONTENT` |
| `src/inoio_sandbox/secrets.py` | Map host env vars to `--secret` flags |
| `src/inoio_sandbox/runner.py` | Assemble and execute the final `msb run` command |
| `src/inoio_sandbox/data/Dockerfile` | Default runner image |
| `src/inoio_sandbox/data/provider-config.json` | inoio LiteLLM provider/model catalog |
| `install.sh` | Bootstrap installer (pipx/venv, msb check, alias offer) |
| `README.md` | Minimal HOWTO |
| `tests/unit/test_*.py` | pytest unit tests |
| `.gitlab-ci.yml` | lint + unit test stages |

---

## Task 0: Resolve Open Questions

**Files:**
- Read: `docs/superpowers/specs/2026-07-20-inoio-sandbox-merged-design.md`
- Create/Modify: `docs/superpowers/plans/2026-07-20-inoio-sandbox-implementation.md` (update assumptions below if findings change)

Before writing launcher code, verify the three unresolved assumptions from the merged spec so later tasks are built on facts, not guesses.

- [x] **Step 1: Verify `OPENCODE_CONFIG_CONTENT` merge behavior**

Question: Does opencode deep-merge `OPENCODE_CONFIG_CONTENT` with `~/.config/opencode/opencode.jsonc`, or does it replace the `provider` section?

Approach:
1. If opencode is installed locally, run:
   ```bash
   OPENCODE_CONFIG_CONTENT='{"provider":{"test-provider":{"name":"Test"}}}' opencode debug config
   ```
   or inspect `~/.config/opencode/opencode.jsonc` before/after.
2. If opencode is not installed, search the opencode documentation/source for `OPENCODE_CONFIG_CONTENT` handling and summarize the observed behavior.
3. Document the result and the recommended launcher behavior in a short note.

Expected outcome: A one-paragraph note stating whether the launcher should:
- pass the fragment as-is (if deep-merge is confirmed), or
- warn that the inoio provider overrides personal providers (if replacement is confirmed).

**Finding:** Deep-merge. Pass the fragment as-is.

- [x] **Step 2: Verify `msb --secret` works with default egress**

Question: Can `msb run --secret LITELLM_API_KEY@litellm.inoio.de` be used with msb's default egress policy (`deny` with an implicit `allow@public` rule), or does it require `--no-net` + `--net-rule`?

Approach:
1. If `msb` is installed and an image is loaded, run a tiny VM:
   ```bash
   msb run -e TEST=1 --secret LITELLM_API_KEY@litellm.inoio.de <image> -- env
   ```
   and check that `LITELLM_API_KEY` is set to the placeholder `$MSB_LITELLM_API_KEY`, not the real value.
2. If `msb` is not available, read the microsandbox CLI help/docs for `--secret` and summarize whether it depends on network rules.

Expected outcome: Confirmation that the launcher can keep default egress and still use `--secret`, or a note that network rules must be added for MVP.

**Finding:** Works with default egress; `litellm.inoio.de` is public and default policy allows `@public`.

- [x] **Step 3: Check `OPENCODE_CONFIG_CONTENT` size / env limits**

Question: The spec estimates the provider fragment may be up to ~15 KB as an upper bound. Will passing it as a single `-e` flag exceed Linux env/argv limits?

Approach:
1. Check the local `ARG_MAX`:
   ```bash
   getconf ARG_MAX
   ```
2. Verify the encoded fragment length is well below `ARG_MAX` (the actual example fragment is ~590 bytes, and the spec's upper-bound estimate of ~15 KB is still far below the typical 2 MB).

Expected outcome: A note that the current approach is safe, or an alternative (write to a tmp file and mount it) if limits are unexpectedly low.

**Finding:** Safe (`ARG_MAX` = 2,097,152 bytes).

- [x] **Step 4: Update plan assumptions**

If any finding contradicts the current plan, edit this plan's "Open Questions / Risks" section to reflect the truth before implementation continues.

- [x] **Step 5: Commit research notes**

Create a short research note under `docs/superpowers/notes/` or append findings to this plan, then commit:

```bash
git add docs/superpowers/plans/2026-07-20-inoio-sandbox-implementation.md docs/superpowers/notes/2026-07-20-inoio-sandbox-open-questions.md
git commit -m "docs: resolve open questions before implementation"
```

---

## Task 1: Project Skeleton

**Files:**
- Create: `pyproject.toml`
- Create: `src/inoio_sandbox/__init__.py`
- Create: `src/inoio_sandbox/cli.py`

- [ ] **Step 1: Write `pyproject.toml`**

```toml
[project]
name = "inoio-sandbox"
version = "0.1.0"
description = "Run opencode inside an ephemeral microsandbox VM"
requires-python = ">=3.10"
dependencies = [
    "click>=8.0",
]

[project.optional-dependencies]
test = [
    "pytest>=7.0",
]

[project.scripts]
inoio-sandbox = "inoio_sandbox.cli:main"

[build-system]
requires = ["hatchling"]
build-backend = "hatchling.build"

[tool.hatch.build.targets.wheel]
packages = ["src/inoio_sandbox"]

[tool.ruff]
line-length = 100
target-version = "py310"

[tool.ruff.lint]
select = ["E", "F", "I", "W"]
```

- [ ] **Step 2: Create package marker**

```python
# src/inoio_sandbox/__init__.py
"""inoio-sandbox launcher."""
__version__ = "0.1.0"
```

- [ ] **Step 3: Create minimal CLI stub**

```python
# src/inoio_sandbox/cli.py
import click


@click.group()
def cli():
    """inoio-sandbox launcher."""
    pass


@cli.command()
def doctor():
    """Check prerequisites."""
    click.echo("doctor: not implemented yet")


@cli.command()
@click.option("--worktree", default=None, help="Worktree name")
@click.option("--image-rebuild", is_flag=True, help="Force image rebuild")
def run(worktree, image_rebuild):
    """Run opencode in a microsandbox VM."""
    click.echo("run: not implemented yet")


def main():
    cli()
```

- [ ] **Step 4: Verify install**

Run:
```bash
python -m venv .venv
source .venv/bin/activate
pip install -e ".[test]"
inoio-sandbox --help
```

Expected: shows `doctor` and `run` subcommands.

- [ ] **Step 5: Commit**

```bash
git add pyproject.toml src/inoio_sandbox/__init__.py src/inoio_sandbox/cli.py
git commit -m "feat: project skeleton and click CLI stub"
```

---

## Task 2: Doctor (Preflight Checks)

**Files:**
- Create: `src/inoio_sandbox/doctor.py`
- Create: `tests/unit/test_doctor.py`
- Modify: `src/inoio_sandbox/cli.py`

- [ ] **Step 1: Write failing tests**

```python
# tests/unit/test_doctor.py
from unittest.mock import patch

from inoio_sandbox import doctor


def test_check_msb_missing(capsys):
    with patch("shutil.which", return_value=None):
        result = doctor.check_msb()
        assert result is False
        captured = capsys.readouterr()
        assert "msb not found" in captured.out


def test_check_docker_missing(capsys):
    with patch("shutil.which", return_value=None):
        result = doctor.check_docker()
        assert result is False
        assert "docker not found" in captured.out


def test_check_kvm_missing():
    with patch("os.path.exists", return_value=False):
        result = doctor.check_kvm()
        assert result is False


def test_check_all_healthy():
    with patch("shutil.which", return_value="/usr/bin/msb"):
        with patch("inoio_sandbox.doctor.check_docker", return_value=True):
            with patch("inoio_sandbox.doctor.check_kvm", return_value=True):
                assert doctor.check_all() is True
```

- [ ] **Step 2: Run tests to verify failure**

```bash
pytest tests/unit/test_doctor.py -v
```

Expected: failures (functions not defined).

- [ ] **Step 3: Implement `doctor.py`**

```python
# src/inoio_sandbox/doctor.py
import os
import shutil

import click


def check_msb() -> bool:
    if shutil.which("msb") is None:
        click.echo(
            "msb not found. Install microsandbox: https://github.com/microsandbox/microsandbox",
            err=True,
        )
        return False
    return True


def check_docker() -> bool:
    if shutil.which("docker") is None:
        click.echo(
            "docker not found. Install Docker or Podman with docker-compatible CLI.",
            err=True,
        )
        return False
    return True


def check_kvm() -> bool:
    if not os.path.exists("/dev/kvm"):
        click.echo(
            "/dev/kvm not found. Load kvm module and ensure user is in the kvm group.",
            err=True,
        )
        return False
    return True


def check_git() -> bool:
    if shutil.which("git") is None:
        click.echo("git not found.", err=True)
        return False
    return True


def check_all() -> bool:
    return all([check_msb(), check_docker(), check_kvm(), check_git()])
```

- [ ] **Step 4: Wire into CLI**

Replace `doctor` command in `src/inoio_sandbox/cli.py`:

```python
from inoio_sandbox import doctor


@cli.command()
def doctor():
    """Check prerequisites."""
    ok = doctor.check_all()
    if not ok:
        raise click.ClickException("preflight failed")
    click.echo("doctor: all checks passed")
```

- [ ] **Step 5: Run tests**

```bash
pytest tests/unit/test_doctor.py -v
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add src/inoio_sandbox/doctor.py tests/unit/test_doctor.py src/inoio_sandbox/cli.py
git commit -m "feat: doctor preflight checks"
```

---

## Task 3: Project Slug + Worktree Resolution

**Files:**
- Create: `src/inoio_sandbox/worktree.py`
- Create: `tests/unit/test_worktree.py`

- [ ] **Step 1: Write failing tests**

```python
# tests/unit/test_worktree.py
from pathlib import Path
from unittest.mock import patch

from inoio_sandbox import worktree


def test_project_slug_from_git_common_dir(tmp_path):
    git_dir = tmp_path / ".git"
    git_dir.mkdir()
    common_dir = tmp_path / "common.git"
    common_dir.mkdir()
    (git_dir / "commondir").write_text(str(common_dir) + "\n")
    with patch("pathlib.Path.cwd", return_value=tmp_path):
        slug = worktree.project_slug()
        assert slug.startswith("p-")
        assert len(slug) == 11  # p- + 8 hex chars


def test_project_slug_fallback(tmp_path):
    non_git = tmp_path / "not-a-repo"
    non_git.mkdir()
    with patch("pathlib.Path.cwd", return_value=non_git):
        with patch("click.echo"):
            slug = worktree.project_slug()
            assert slug.startswith("p-")
            assert len(slug) == 11


def test_branch_name(tmp_path):
    with patch("subprocess.check_output", return_value=b"feature-x\n"):
        assert worktree.branch_name(tmp_path) == "feature-x"


def test_worktree_path(tmp_path):
    state_dir = tmp_path / "state"
    slug = "p-deadbeef"
    branch = "feature-x"
    path = worktree.worktree_path(state_dir, slug, branch)
    assert path == state_dir / "worktrees" / "p-deadbeef" / "feature-x"
```

- [ ] **Step 2: Run tests to verify failure**

```bash
pytest tests/unit/test_worktree.py -v
```

Expected: failures.

- [ ] **Step 3: Implement `worktree.py`**

```python
# src/inoio_sandbox/worktree.py
import hashlib
import subprocess
from pathlib import Path

import click


def project_slug() -> str:
    """Return a stable short slug for the current repo."""
    cwd = Path.cwd()
    git_common_dir = _git_common_dir(cwd)
    if git_common_dir:
        key = str(git_common_dir.resolve())
    else:
        click.echo(
            "Warning: not inside a git repo; using CWD hash as project slug.",
            err=True,
        )
        key = str(cwd.resolve())
    h = hashlib.sha256(key.encode()).hexdigest()[:8]
    return f"p-{h}"


def _git_common_dir(cwd: Path) -> Path | None:
    try:
        output = subprocess.check_output(
            ["git", "rev-parse", "--git-common-dir"],
            cwd=cwd,
            stderr=subprocess.DEVNULL,
        )
        path = Path(output.decode().strip())
        if not path.is_absolute():
            path = cwd / path
        return path.resolve()
    except (subprocess.CalledProcessError, FileNotFoundError):
        return None


def branch_name(cwd: Path | None = None) -> str:
    cwd = cwd or Path.cwd()
    output = subprocess.check_output(
        ["git", "rev-parse", "--abbrev-ref", "HEAD"],
        cwd=cwd,
        stderr=subprocess.DEVNULL,
    )
    return output.decode().strip()


def worktree_path(state_dir: Path, project_slug: str, branch: str) -> Path:
    return state_dir / "worktrees" / project_slug / branch


def ensure_worktree(repo_root: Path, state_dir: Path, project_slug: str, branch: str) -> Path:
    target = worktree_path(state_dir, project_slug, branch)
    if target.exists():
        return target
    target.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(
        ["git", "worktree", "add", str(target), branch],
        cwd=repo_root,
        check=True,
    )
    return target
```

- [ ] **Step 4: Run tests**

```bash
pytest tests/unit/test_worktree.py -v
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add src/inoio_sandbox/worktree.py tests/unit/test_worktree.py
git commit -m "feat: project slug and worktree resolution"
```

---

## Task 4: Dockerfile + Image Build/Load

**Files:**
- Create: `src/inoio_sandbox/data/Dockerfile`
- Create: `src/inoio_sandbox/image.py`
- Create: `tests/unit/test_image.py`

- [ ] **Step 1: Create default Dockerfile**

```dockerfile
# src/inoio_sandbox/data/Dockerfile
FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
    && rm -rf /var/lib/apt/lists/*

ARG USER_UID=1000
ARG USER_GID=1000
RUN groupadd -g "$USER_GID" dev && \
    useradd -m -u "$USER_UID" -g "$USER_GID" -s /bin/bash dev && \
    mkdir -p /home/dev/.config /home/dev/workspace && \
    chown -R dev:dev /home/dev

USER dev
RUN curl -fsSL https://opencode.ai/install | bash

ENV HOME=/home/dev
ENV PATH=/home/dev/.opencode/bin:/home/dev/.local/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
WORKDIR /home/dev/workspace
```

- [ ] **Step 2: Write failing tests**

```python
# tests/unit/test_image.py
import hashlib
from pathlib import Path

from inoio_sandbox import image


def test_dockerfile_hash(tmp_path):
    df = tmp_path / "Dockerfile"
    df.write_text("FROM debian\nRUN echo hi\n")
    h = image.dockerfile_hash(df)
    expected = hashlib.sha256(df.read_bytes()).hexdigest()[:12]
    assert h == expected


def test_image_tag():
    h = "abc123"
    assert image.image_tag(h) == "inoio-sandbox/runner:abc123"
```

- [ ] **Step 3: Run tests to verify failure**

```bash
pytest tests/unit/test_image.py -v
```

Expected: failures.

- [ ] **Step 4: Implement `image.py`**

```python
# src/inoio_sandbox/image.py
import hashlib
import subprocess
from pathlib import Path


def dockerfile_hash(dockerfile: Path) -> str:
    return hashlib.sha256(dockerfile.read_bytes()).hexdigest()[:12]


def image_tag(hash_value: str) -> str:
    return f"inoio-sandbox/runner:{hash_value}"


def image_exists(tag: str) -> bool:
    result = subprocess.run(
        ["msb", "images", "--format", "{{.Tag}}"],
        capture_output=True,
        text=True,
    )
    return tag in result.stdout.splitlines()


def build_and_load(dockerfile: Path, tag: str, force: bool = False) -> None:
    if not force and image_exists(tag):
        return
    subprocess.run(
        ["docker", "build", "-f", str(dockerfile), "-t", tag, str(dockerfile.parent)],
        check=True,
    )
    subprocess.run(
        f"docker save {tag} | msb load --tag {tag}",
        shell=True,
        check=True,
    )
```

- [ ] **Step 5: Run tests**

```bash
pytest tests/unit/test_image.py -v
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add src/inoio_sandbox/data/Dockerfile src/inoio_sandbox/image.py tests/unit/test_image.py
git commit -m "feat: default Dockerfile and image build/load"
```

---

## Task 5: Volumes

**Files:**
- Create: `src/inoio_sandbox/volumes.py`
- Create: `tests/unit/test_volumes.py`

- [ ] **Step 1: Write failing tests**

```python
# tests/unit/test_volumes.py
from inoio_sandbox import volumes


def test_volume_names():
    assert volumes.local_volume_name("p-deadbeef") == "p-deadbeef-opencode-local"
    assert volumes.cache_volume_name("p-deadbeef") == "p-deadbeef-opencode-cache"


def test_volume_paths(tmp_path):
    local, cache = volumes.fallback_paths(tmp_path, "p-deadbeef")
    assert local == tmp_path / "p-deadbeef" / "local"
    assert cache == tmp_path / "p-deadbeef" / "cache"
```

- [ ] **Step 2: Run tests to verify failure**

```bash
pytest tests/unit/test_volumes.py -v
```

Expected: failures.

- [ ] **Step 3: Implement `volumes.py`**

```python
# src/inoio_sandbox/volumes.py
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
    base = state_dir / "state" / project_slug
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
```

- [ ] **Step 4: Run tests**

```bash
pytest tests/unit/test_volumes.py -v
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add src/inoio_sandbox/volumes.py tests/unit/test_volumes.py
git commit -m "feat: msb volume management with host fallback"
```

---

## Task 6: Provider Config + Secrets

**Files:**
- Create: `src/inoio_sandbox/data/provider-config.json`
- Create: `src/inoio_sandbox/config.py`
- Create: `src/inoio_sandbox/secrets.py`
- Create: `tests/unit/test_config.py`
- Create: `tests/unit/test_secrets.py`

- [ ] **Step 1: Create provider fragment**

```json
{
  "provider": {
    "litellm": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "LiteLLM",
      "options": {
        "baseURL": "https://litellm.inoio.de",
        "apiKey": "{env:LITELLM_API_KEY}"
      },
      "models": [
        { "name": "openai/gpt-4o", "label": "GPT-4o" },
        { "name": "openai/gpt-4o-mini", "label": "GPT-4o Mini" },
        { "name": "anthropic/claude-3-5-sonnet", "label": "Claude 3.5 Sonnet" }
      ]
    }
  }
}
```

- [ ] **Step 2: Write failing tests for config**

```python
# tests/unit/test_config.py
import json
from pathlib import Path

from inoio_sandbox import config


def test_load_provider_config(tmp_path):
    data = {"provider": {"x": 1}}
    f = tmp_path / "provider-config.json"
    f.write_text(json.dumps(data))
    assert config.load_provider_config(f) == data


def test_build_config_content(tmp_path):
    provider = {"provider": {"litellm": {"apiKey": "{env:LITELLM_API_KEY}"}}}
    f = tmp_path / "provider-config.json"
    f.write_text(json.dumps(provider))
    content = config.build_config_content(f)
    assert content.startswith("OPENCODE_CONFIG_CONTENT=")
    assert "LITELLM_API_KEY" in content
```

- [ ] **Step 3: Implement `config.py`**

```python
# src/inoio_sandbox/config.py
import json
import urllib.parse
from pathlib import Path


def load_provider_config(path: Path) -> dict:
    return json.loads(path.read_text())


def build_config_content(path: Path) -> str:
    fragment = load_provider_config(path)
    return f"OPENCODE_CONFIG_CONTENT={urllib.parse.quote(json.dumps(fragment))}"
```

- [ ] **Step 4: Write failing tests for secrets**

```python
# tests/unit/test_secrets.py
from unittest.mock import patch

from inoio_sandbox import secrets


def test_secret_flags_all_present():
    env = {"LITELLM_API_KEY": "abc", "GITHUB_TOKEN": "def"}
    with patch("os.environ", env):
        flags = secrets.secret_flags()
        assert "--secret" in flags
        assert "LITELLM_API_KEY@litellm.inoio.de" in flags
        assert "GITHUB_TOKEN@github.com" in flags


def test_secret_flags_missing_warns():
    env = {}
    with patch("os.environ", env):
        with patch("click.echo") as echo:
            flags = secrets.secret_flags()
            assert flags == []
            echo.assert_called()
```

- [ ] **Step 5: Implement `secrets.py`**

```python
# src/inoio_sandbox/secrets.py
import os

import click


SECRET_MAP = {
    "LITELLM_API_KEY": "litellm.inoio.de",
    "GITHUB_TOKEN": "github.com",
}


def secret_flags() -> list[str]:
    flags = []
    for var, host in SECRET_MAP.items():
        value = os.environ.get(var)
        if value:
            flags.extend(["--secret", f"{var}@{host}"])
        else:
            click.echo(f"Warning: {var} not set; related provider/API may fail.", err=True)
    return flags
```

- [ ] **Step 6: Run tests**

```bash
pytest tests/unit/test_config.py tests/unit/test_secrets.py -v
```

Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add src/inoio_sandbox/data/provider-config.json src/inoio_sandbox/config.py src/inoio_sandbox/secrets.py tests/unit/test_config.py tests/unit/test_secrets.py
git commit -m "feat: provider config and secret flag generation"
```

---

## Task 7: Run Command Assembly

**Files:**
- Create: `src/inoio_sandbox/runner.py`
- Modify: `src/inoio_sandbox/cli.py`
- Create: `tests/unit/test_runner.py`

- [ ] **Step 1: Write failing tests**

```python
# tests/unit/test_runner.py
from pathlib import Path

from inoio_sandbox import runner


def test_build_command_uses_named_volumes():
    cmd = runner.build_msb_run_command(
        image_tag="inoio-sandbox/runner:abc",
        worktree=Path("/wt"),
        local="p-x-local",
        cache="p-x-cache",
        config_content="OPENCODE_CONFIG_CONTENT={}",
        secret_flags=["--secret", "LITELLM_API_KEY@litellm.inoio.de"],
        env_extra=["FOO=bar"],
    )
    assert cmd[0] == "msb"
    assert "run" in cmd
    assert "/wt:/home/dev/workspace" in cmd
    assert "p-x-local:/home/dev/.local" in cmd
    assert "p-x-cache:/home/dev/.cache" in cmd
    assert "--secret" in cmd
    assert "LITELLM_API_KEY@litellm.inoio.de" in cmd
    assert "FOO=bar" in cmd
    assert cmd[-2:] == ["inoio-sandbox/runner:abc", "-- opencode"]
```

- [ ] **Step 2: Run tests to verify failure**

```bash
pytest tests/unit/test_runner.py -v
```

Expected: failure.

- [ ] **Step 3: Implement `runner.py`**

```python
# src/inoio_sandbox/runner.py
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
```

- [ ] **Step 4: Wire `run` command in CLI**

Replace `run` in `src/inoio_sandbox/cli.py`:

```python
import os
from pathlib import Path

import click

from inoio_sandbox import config, doctor, image, runner, secrets, volumes, worktree


DATA_DIR = Path(__file__).parent / "data"
DEFAULT_DOCKERFILE = DATA_DIR / "Dockerfile"
DEFAULT_PROVIDER_CONFIG = DATA_DIR / "provider-config.json"
STATE_DIR = Path.home() / ".local/share/inoio-sandbox"


@cli.command()
@click.option("--worktree", default=None, help="Worktree name")
@click.option("--image-rebuild", is_flag=True, help="Force image rebuild")
@click.option("--volume-fallback", is_flag=True, help="Use host directories instead of msb volumes")
def run(worktree, image_rebuild, volume_fallback):
    """Run opencode in a microsandbox VM."""
    if not doctor.check_all():
        raise click.ClickException("preflight failed")

    cwd = Path.cwd()
    project = worktree_mod.project_slug()
    branch = worktree or worktree_mod.branch_name(cwd)
    wt = worktree_mod.ensure_worktree(cwd, STATE_DIR, project, branch)

    dockerfile = (
        Path(".sandbox/Dockerfile")
        if Path(".sandbox/Dockerfile").exists()
        else DEFAULT_DOCKERFILE
    )
    df_hash = image.dockerfile_hash(dockerfile)
    tag = image.image_tag(df_hash)
    image.build_and_load(dockerfile, tag, force=image_rebuild)

    local, cache = volumes.ensure_volumes(project, STATE_DIR, fallback=volume_fallback)
    config_content = config.build_config_content(DEFAULT_PROVIDER_CONFIG)
    secret_flags = secrets.secret_flags()

    env_extra = []
    env_file = Path(".sandbox/env")
    if env_file.exists():
        for line in env_file.read_text().splitlines():
            line = line.strip()
            if line and "=" in line and not line.startswith("#"):
                env_extra.append(line)

    cmd = runner.build_msb_run_command(
        image_tag=tag,
        worktree=wt,
        local=local,
        cache=cache,
        config_content=config_content,
        secret_flags=secret_flags,
        env_extra=env_extra,
    )
    os.execvp("msb", cmd)
```

- [ ] **Step 5: Run tests**

```bash
pytest tests/unit/test_runner.py -v
```

Expected: pass.

- [ ] **Step 6: Commit**

```bash
git add src/inoio_sandbox/runner.py tests/unit/test_runner.py src/inoio_sandbox/cli.py
git commit -m "feat: msb run command assembly and CLI wiring"
```

---

## Task 8: `.sandbox/env` + Per-Project Dockerfile Support

**Files:**
- Create: `tests/unit/test_sandbox_env.py`

- [ ] **Step 1: Write tests for env file parsing**

```python
# tests/unit/test_sandbox_env.py
from pathlib import Path
from unittest.mock import patch

from click.testing import CliRunner

from inoio_sandbox.cli import run


def test_run_reads_sandbox_env(tmp_path):
    (tmp_path / ".sandbox").mkdir()
    (tmp_path / ".sandbox" / "env").write_text("FOO=bar\n# comment\n\nBAZ=qux\n")
    with patch("inoio_sandbox.cli.doctor.check_all", return_value=True):
        with patch("inoio_sandbox.cli.worktree_mod"):
            with patch("inoio_sandbox.cli.image"):
                with patch("inoio_sandbox.cli.volumes"):
                    with patch("inoio_sandbox.cli.config"):
                        with patch("inoio_sandbox.cli.secrets"):
                            with patch("inoio_sandbox.cli.runner") as r:
                                with patch("os.execvp"):
                                    test_runner = CliRunner()
                                    with test_runner.isolated_filesystem(temp_dir=tmp_path):
                                        result = test_runner.invoke(run, [])
                                        assert result.exit_code == 0
                                        call = r.build_msb_run_command.call_args
                                        assert "FOO=bar" in call.kwargs["env_extra"]
                                        assert "BAZ=qux" in call.kwargs["env_extra"]
```

- [ ] **Step 2: Run tests**

```bash
pytest tests/unit/test_sandbox_env.py -v
```

Expected: pass (already implemented in Task 7).

- [ ] **Step 3: Commit**

```bash
git add tests/unit/test_sandbox_env.py
git commit -m "test: per-project .sandbox/env parsing"
```

---

## Task 9: Install Script

**Files:**
- Create: `install.sh`

- [ ] **Step 1: Write install script**

```bash
#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-$HOME/.local}"
BIN_DIR="$INSTALL_DIR/bin"
VENV_DIR="$INSTALL_DIR/share/inoio-sandbox"

echo "==> Installing inoio-sandbox into $VENV_DIR"

python3 -m venv "$VENV_DIR"
"$VENV_DIR/bin/pip" install --upgrade pip
"$VENV_DIR/bin/pip" install .

mkdir -p "$BIN_DIR"
ln -sf "$VENV_DIR/bin/inoio-sandbox" "$BIN_DIR/inoio-sandbox"

if ! command -v msb >/dev/null 2>&1; then
    echo "==> microsandbox (msb) not found"
    echo "    Install from https://github.com/microsandbox/microsandbox"
fi

if ! grep -q "inoio-sandbox" "$HOME/.bashrc" 2>/dev/null; then
    read -p "==> Add 'opencode' alias to ~/.bashrc? [y/N] " reply
    if [[ "$reply" =~ ^[Yy]$ ]]; then
        echo "alias opencode='inoio-sandbox run'" >> "$HOME/.bashrc"
        echo "    Added. Run: source ~/.bashrc"
    fi
fi

echo "==> Done. Add $BIN_DIR to PATH if needed."
```

- [ ] **Step 2: Make executable and verify syntax**

```bash
chmod +x install.sh
bash -n install.sh
```

- [ ] **Step 3: Commit**

```bash
git add install.sh
git commit -m "feat: bootstrap install script"
```

---

## Task 10: README + CI

**Files:**
- Create: `README.md`
- Create: `.gitlab-ci.yml`

- [ ] **Step 1: Write README**

```markdown
# inoio-sandbox

Run opencode inside an ephemeral microsandbox VM.

## Install

```bash
./install.sh
```

## Usage

```bash
opencode          # alias for inoio-sandbox run
inoio-sandbox doctor
inoio-sandbox run --worktree my-feature
```

## Project overrides

Create `.sandbox/Dockerfile` to override the runner image.
Create `.sandbox/env` to add environment variables.
```

- [ ] **Step 2: Write GitLab CI**

```yaml
# .gitlab-ci.yml
stages:
  - lint
  - test

variables:
  PIP_DISABLE_PIP_VERSION_CHECK: "1"

lint:
  stage: lint
  image: python:3.12-slim
  script:
    - pip install ruff
    - ruff check .

unit-tests:
  stage: test
  image: python:3.12-slim
  script:
    - pip install -e ".[test]"
    - pytest tests/unit
```

- [ ] **Step 3: Commit**

```bash
git add README.md .gitlab-ci.yml
git commit -m "docs/ci: README and GitLab CI config"
```

---

## Task 11: Final Lint + Test Pass

**Files:**
- Modify: any files needed to satisfy ruff/tests

- [ ] **Step 1: Install dev deps**

```bash
pip install -e ".[test]"
pip install ruff
```

- [ ] **Step 2: Run ruff**

```bash
ruff check .
```

Expected: clean.

- [ ] **Step 3: Run unit tests**

```bash
pytest tests/unit -v
```

Expected: all pass.

- [ ] **Step 4: Commit fixes**

```bash
git add -A
git commit -m "style: lint fixes and final test pass"
```

---

## Spec Coverage Check

| Spec Section | Task |
|---|---|
| Python 3 + click CLI | Task 1, 7 |
| `doctor.py` preflight | Task 2 |
| Project slug + worktree | Task 3 |
| `image.py` hash/build/load | Task 4 |
| Default Dockerfile | Task 4 |
| `volumes.py` named volumes + fallback | Task 5 |
| `config.py` provider fragment | Task 6 |
| `secrets.py` `--secret` flags | Task 6 |
| `.sandbox/{Dockerfile,env}` opt-in | Task 4, 7, 8 |
| `install.sh` + `opencode` alias | Task 9 |
| README + CI | Task 10 |
| Testing | Every task + Task 11 |

---

## Open Questions / Risks

Resolved in `docs/superpowers/notes/2026-07-20-inoio-sandbox-open-questions.md`.

1. **`OPENCODE_CONFIG_CONTENT` deep-merge behavior**: **Resolved — deep-merge.** opencode merges the inline fragment with the existing config stack; the inoio provider overrides only a same-key personal provider. Pass the fragment as-is.
2. **`--secret` with default egress**: **Resolved — works with default egress.** Default msb policy is `deny` with an implicit `allow@public` rule when no other rules are present; `litellm.inoio.de` is public. No `--net-rule` flags needed for MVP. The guest sees `LITELLM_API_KEY=$MSB_LITELLM_API_KEY`, matching the provider fragment's `{env:LITELLM_API_KEY}` token.
3. **Shell/env length**: **Resolved — safe.** Local `ARG_MAX` is 2,097,152 bytes. The spec's upper-bound estimate of ~15 KB is still negligible, and the actual example fragment is only ~590 bytes. No temp-file fallback needed.
4. **Stale VM detection (`--force`)**: Explicitly deferred. Not implemented in MVP. May be added later without changing the core launcher interface.
