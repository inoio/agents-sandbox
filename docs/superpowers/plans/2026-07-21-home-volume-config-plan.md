# Home Volume + Config Injection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the per-project `.local`/`.cache` volume pair with a single
project-scoped home volume, and merge host user, project, and inoio-sandbox
config fragments into `/home/dev/.config/opencode` at VM boot.

**Architecture:** The launcher builds a merged config directory on the host,
copies it into the rootfs via `msb --copy-dir`, then uses an `msb --script` to
copy it into the persistent home volume after the volume is mounted. The home
volume is keyed by project slug and Dockerfile hash so image updates are safe.

**Tech Stack:** Python 3, click, pytest, json5, microsandbox CLI.

## Global Constraints

- Target platforms: Linux (KVM) and macOS (Apple Silicon).
- Launcher language: Python 3 + click.
- One ephemeral microsandbox VM per `opencode` invocation.
- API keys forwarded via `msb --secret`; real values never enter the VM.
- Project state stored in msb named volumes per project; host-directory fallback
  when msb volumes are not viable.
- No network rules for the MVP; default egress allowed.
- Tests run with `.venv/bin/python -m pytest tests/unit`.

---

## File Structure

| File | Responsibility |
|---|---|
| `src/inoio_sandbox/volumes.py` | Home volume naming, creation, prefill, reset, fallback path. |
| `src/inoio_sandbox/config.py` | Load provider config, deep-merge JSON/JSONC files, copy non-JSON files, write merged config to temp dir. |
| `src/inoio_sandbox/runner.py` | Assemble `msb run` command with home volume, `--copy-dir`, `--script`; remove old local/cache mounts and `OPENCODE_CONFIG_CONTENT`. |
| `src/inoio_sandbox/cli.py` | Add `--reset-home` flag, wire image hash into volume creation, wire merged config into runner. |
| `tests/unit/test_volumes.py` | Test home volume naming, prefill invocation, reset, fallback. |
| `tests/unit/test_config.py` | Test deep merge, JSON/JSONC merge, non-JSON copy, project override, provider injection. |
| `tests/unit/test_runner.py` | Test new command structure. |
| `tests/unit/test_sandbox_env.py` | Update CLI integration tests for new volume/config call signatures. |

---

### Task 1: Home volume naming and creation

**Files:**
- Modify: `src/inoio_sandbox/volumes.py`
- Test: `tests/unit/test_volumes.py`

**Interfaces:**
- Produces: `home_volume_name(project_slug: str, image_hash: str) -> str`
- Produces: `ensure_home_volume(project_slug: str, image_hash: str, state_dir: Path, image_tag: str, fallback: bool = False, reset: bool = False) -> str | Path`
- Produces: `prefill_home_volume(name_or_path: str, image_tag: str) -> None`
- Produces: `remove_home_volume(name: str) -> None`
- Produces: `fallback_home_path(state_dir: Path, project_slug: str, image_hash: str) -> Path`

- [ ] **Step 1: Write failing tests**

```python
import subprocess
from pathlib import Path
from unittest.mock import patch

from inoio_sandbox import volumes


def test_home_volume_name_includes_image_hash():
    assert volumes.home_volume_name("myproject", "abc123") == "myproject-opencode-home-abc123"


def test_fallback_home_path():
    path = volumes.fallback_home_path(Path("/state"), "myproject", "abc123")
    assert path == Path("/state/state/myproject/home/abc123")


def test_remove_home_volume_invokes_msb(monkeypatch):
    called = []

    def fake_run(cmd, **kwargs):
        called.append(cmd)
        return subprocess.CompletedProcess(args=cmd, returncode=0)

    monkeypatch.setattr(subprocess, "run", fake_run)
    volumes.remove_home_volume("myproject-opencode-home-abc123")
    assert called == [["msb", "volume", "remove", "myproject-opencode-home-abc123"]]


def test_prefill_home_volume_invokes_msb(monkeypatch):
    called = []

    def fake_run(cmd, **kwargs):
        called.append(cmd)
        return subprocess.CompletedProcess(args=cmd, returncode=0)

    monkeypatch.setattr(subprocess, "run", fake_run)
    volumes.prefill_home_volume("myproject-opencode-home-abc123", "inoio-sandbox/runner:abc123")
    assert called[0][:3] == ["msb", "run", "-v"]
    assert called[0][3] == "myproject-opencode-home-abc123:/mnt/home"
    assert called[0][4] == "inoio-sandbox/runner:abc123"


def test_ensure_home_volume_prefills_when_created(monkeypatch, tmp_path):
    created = []
    prefilled = []

    def fake_ensure(name):
        created.append(name)
        return True  # newly created

    def fake_prefill(name, tag):
        prefilled.append((name, tag))

    monkeypatch.setattr(volumes, "ensure_msb_volume", fake_ensure)
    monkeypatch.setattr(volumes, "prefill_home_volume", fake_prefill)

    result = volumes.ensure_home_volume("myproject", "abc123", tmp_path, "tag:x")
    assert result == "myproject-opencode-home-abc123"
    assert created == ["myproject-opencode-home-abc123"]
    assert prefilled == [("myproject-opencode-home-abc123", "tag:x")]


def test_ensure_home_volume_reset_removes_then_creates(monkeypatch, tmp_path):
    removed = []
    created = []

    def fake_remove(name):
        removed.append(name)

    def fake_ensure(name):
        created.append(name)
        return True

    monkeypatch.setattr(volumes, "remove_home_volume", fake_remove)
    monkeypatch.setattr(volumes, "prefill_home_volume", lambda *a: None)
    monkeypatch.setattr(volumes, "ensure_msb_volume", fake_ensure)

    volumes.ensure_home_volume("myproject", "abc123", tmp_path, "tag:x", reset=True)
    assert removed == ["myproject-opencode-home-abc123"]
    assert created == ["myproject-opencode-home-abc123"]
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `.venv/bin/python -m pytest tests/unit/test_volumes.py -v`

Expected: FAIL — functions not defined.

- [ ] **Step 3: Implement home volume functions**

Replace the contents of `src/inoio_sandbox/volumes.py` with:

```python
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `.venv/bin/python -m pytest tests/unit/test_volumes.py -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/inoio_sandbox/volumes.py tests/unit/test_volumes.py
git commit -m "feat: add project-scoped home volume"
```

---

### Task 2: Config merge logic

**Files:**
- Modify: `src/inoio_sandbox/config.py`
- Test: `tests/unit/test_config.py`

**Interfaces:**
- Consumes: `load_provider_config(path: Path) -> dict` (existing)
- Produces: `deep_merge(base: dict, override: dict) -> dict`
- Produces: `build_merged_config(user_dir: Path, project_dir: Path | None, provider_config_path: Path) -> Path`

- [ ] **Step 1: Write failing tests**

Replace `tests/unit/test_config.py` with:

```python
import json
from pathlib import Path

from inoio_sandbox import config


def test_load_provider_config(tmp_path):
    data = {"provider": {"x": 1}}
    f = tmp_path / "provider-config.json"
    f.write_text(json.dumps(data))
    assert config.load_provider_config(f) == data


def test_deep_merge_replaces_arrays_and_merges_dicts():
    base = {"a": [1], "b": {"x": 1, "y": 2}}
    override = {"a": [2], "b": {"y": 3, "z": 4}}
    assert config.deep_merge(base, override) == {"a": [2], "b": {"x": 1, "y": 3, "z": 4}}


def test_build_merged_config_creates_temp_dir_with_merged_json(tmp_path):
    user_dir = tmp_path / "user"
    project_dir = tmp_path / "project"
    user_dir.mkdir()
    project_dir.mkdir()

    (user_dir / "opencode.jsonc").write_text(json.dumps({"a": 1, "b": {"x": 1}}))
    (project_dir / "opencode.jsonc").write_text(json.dumps({"b": {"y": 2}}))

    provider = tmp_path / "provider-config.json"
    provider.write_text(json.dumps({"provider": {"litellm": {"models": {"m": {}}}}}))

    result_dir = config.build_merged_config(user_dir, project_dir, provider)
    merged = json.loads((result_dir / "opencode.jsonc").read_text())
    assert merged["a"] == 1
    assert merged["b"] == {"x": 1, "y": 2}
    assert merged["provider"]["litellm"]["models"] == {"m": {}}


def test_build_merged_config_project_overrides_user_non_json(tmp_path):
    user_dir = tmp_path / "user"
    project_dir = tmp_path / "project"
    user_dir.mkdir()
    project_dir.mkdir()

    (user_dir / "plugin.txt").write_text("user")
    (project_dir / "plugin.txt").write_text("project")

    provider = tmp_path / "provider-config.json"
    provider.write_text(json.dumps({"provider": {"litellm": {}}}))

    result_dir = config.build_merged_config(user_dir, project_dir, provider)
    assert (result_dir / "plugin.txt").read_text() == "project"


def test_build_merged_config_without_project_dir(tmp_path):
    user_dir = tmp_path / "user"
    user_dir.mkdir()
    (user_dir / "opencode.jsonc").write_text(json.dumps({"a": 1}))

    provider = tmp_path / "provider-config.json"
    provider.write_text(json.dumps({"provider": {"litellm": {}}}))

    result_dir = config.build_merged_config(user_dir, None, provider)
    merged = json.loads((result_dir / "opencode.jsonc").read_text())
    assert merged["a"] == 1
    assert merged["provider"]["litellm"] == {}


def test_build_merged_config_creates_opencode_jsonc_when_missing(tmp_path):
    provider = tmp_path / "provider-config.json"
    provider.write_text(json.dumps({"provider": {"litellm": {"models": {"m": {}}}}}))

    result_dir = config.build_merged_config(tmp_path / "user", None, provider)
    merged = json.loads((result_dir / "opencode.jsonc").read_text())
    assert merged == {"provider": {"litellm": {"models": {"m": {}}}}}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `.venv/bin/python -m pytest tests/unit/test_config.py -v`

Expected: FAIL — `deep_merge` and `build_merged_config` not defined.

- [ ] **Step 3: Implement config merge functions**

Replace the contents of `src/inoio_sandbox/config.py` with:

```python
import json
import json5
import tempfile
from pathlib import Path


def load_provider_config(path: Path) -> dict:
    return json5.loads(path.read_text())


def deep_merge(base: dict, override: dict) -> dict:
    result = dict(base)
    for key, value in override.items():
        if key in result and isinstance(result[key], dict) and isinstance(value, dict):
            result[key] = deep_merge(result[key], value)
        else:
            result[key] = value
    return result


def _load_jsonc(path: Path) -> dict:
    return json5.loads(path.read_text())


def _is_json_file(path: Path) -> bool:
    return path.suffix in (".json", ".jsonc")


def _merge_json_files(
    user_dir: Path | None,
    project_dir: Path | None,
    provider_config: dict,
) -> dict[str, dict]:
    files: dict[str, dict] = {}
    for source in [user_dir, project_dir]:
        if source is None or not source.exists():
            continue
        for path in sorted(source.iterdir()):
            if not _is_json_file(path):
                continue
            name = path.name
            data = _load_jsonc(path)
            files[name] = deep_merge(files.get(name, {}), data)

    provider_branch = {"provider": {"litellm": provider_config["provider"]["litellm"]}}
    for name in ["opencode.jsonc", "opencode.json"]:
        if name in files:
            files[name] = deep_merge(files[name], provider_branch)
            break
    else:
        files["opencode.jsonc"] = provider_branch

    return files


def _collect_other_files(
    user_dir: Path | None,
    project_dir: Path | None,
) -> dict[str, bytes]:
    files: dict[str, bytes] = {}
    for source in [user_dir, project_dir]:
        if source is None or not source.exists():
            continue
        for path in sorted(source.iterdir()):
            if _is_json_file(path):
                continue
            files[path.name] = path.read_bytes()
    return files


def build_merged_config(
    user_dir: Path,
    project_dir: Path | None,
    provider_config_path: Path,
) -> Path:
    provider_config = load_provider_config(provider_config_path)
    json_files = _merge_json_files(user_dir, project_dir, provider_config)
    other_files = _collect_other_files(user_dir, project_dir)

    tmp_dir = Path(tempfile.mkdtemp(prefix="inoio-sandbox-config-"))
    for name, data in json_files.items():
        (tmp_dir / name).write_text(json.dumps(data, indent=2))
    for name, data in other_files.items():
        (tmp_dir / name).write_bytes(data)

    return tmp_dir
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `.venv/bin/python -m pytest tests/unit/test_config.py -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/inoio_sandbox/config.py tests/unit/test_config.py
git commit -m "feat: merge user, project, and provider config fragments"
```

---

### Task 3: Runner command assembly

**Files:**
- Modify: `src/inoio_sandbox/runner.py`
- Test: `tests/unit/test_runner.py`

**Interfaces:**
- Consumes: `home_volume` (str or Path), `config_tmp_dir` (Path)
- Produces: `build_msb_run_command(..., home_volume: str | Path, config_tmp_dir: Path, ...)`

- [ ] **Step 1: Write failing tests**

Replace `tests/unit/test_runner.py` with:

```python
from pathlib import Path

from inoio_sandbox import runner


def test_build_command_uses_home_volume():
    cmd = runner.build_msb_run_command(
        image_tag="inoio-sandbox/runner:abc",
        name="inoio-sandbox-p-branch",
        worktree=Path("/wt"),
        home_volume="p-abc-home",
        config_tmp_dir=Path("/cfg"),
        secret_flags=["--secret", "LITELLM_API_KEY@litellm.inoio.de"],
        env_extra=["FOO=bar"],
        cpus=2,
        memory="4G",
    )
    assert cmd[0] == "msb"
    assert "run" in cmd
    assert "p-abc-home:/home/dev" in cmd
    assert "/wt:/home/dev/workspace" in cmd
    assert "--copy-dir" in cmd
    assert "/cfg:/tmp/inject/opencode" in cmd
    assert "--script" in cmd
    assert "mkdir -p /home/dev/.config/opencode" in " ".join(cmd)
    assert "cp -r /tmp/inject/opencode/. /home/dev/.config/opencode/" in " ".join(cmd)
    assert "--secret" in cmd
    assert "LITELLM_API_KEY@litellm.inoio.de" in cmd
    assert "FOO=bar" in cmd
    assert "HOME=/home/dev" in cmd
    assert cmd[-3:] == ["inoio-sandbox/runner:abc", "--", "opencode"]


def test_build_command_does_not_include_old_local_or_cache_mounts():
    cmd = runner.build_msb_run_command(
        image_tag="inoio-sandbox/runner:abc",
        name="inoio-sandbox-p-branch",
        worktree=Path("/wt"),
        home_volume="p-abc-home",
        config_tmp_dir=Path("/cfg"),
        secret_flags=[],
        env_extra=[],
        cpus=1,
        memory="4G",
    )
    assert "/home/dev/.local" not in cmd
    assert "/home/dev/.cache" not in cmd
    assert "OPENCODE_CONFIG_CONTENT" not in " ".join(cmd)


def test_build_command_hides_envrc_files(tmp_path):
    (tmp_path / ".envrc").write_text("secret\n")
    (tmp_path / ".envrc.local").write_text("local secret\n")
    cmd = runner.build_msb_run_command(
        image_tag="inoio-sandbox/runner:abc",
        name="inoio-sandbox-p-branch",
        worktree=tmp_path,
        home_volume="p-abc-home",
        config_tmp_dir=Path("/cfg"),
        secret_flags=[],
        env_extra=[],
        cpus=1,
        memory="4G",
    )
    assert "--rm" in cmd
    assert "/home/dev/workspace/.envrc" in cmd
    assert "/home/dev/workspace/.envrc.local" in cmd


def test_available_memory_gib_returns_positive_integer():
    assert runner.available_memory_gib() > 0


def test_available_cpus_returns_positive_integer():
    assert runner.available_cpus() > 0
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `.venv/bin/python -m pytest tests/unit/test_runner.py -v`

Expected: FAIL — signature mismatch.

- [ ] **Step 3: Implement runner changes**

Replace the contents of `src/inoio_sandbox/runner.py` with:

```python
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
        "-v",
        f"{worktree}:/home/dev/workspace",
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `.venv/bin/python -m pytest tests/unit/test_runner.py -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/inoio_sandbox/runner.py tests/unit/test_runner.py
git commit -m "feat: assemble msb run with home volume and config injection"
```

---

### Task 4: CLI wiring and script updates

**Files:**
- Modify: `src/inoio_sandbox/cli.py`
- Modify: `scripts/diagnose-startup.py`
- Modify: `scripts/inspect-lsp-location.py`
- Modify: `scripts/test-lsp-symlink.py`
- Test: `tests/unit/test_sandbox_env.py`

**Interfaces:**
- Consumes: `volumes.ensure_home_volume(...)`
- Consumes: `config.build_merged_config(...)`
- Consumes: `runner.build_msb_run_command(..., home_volume, config_tmp_dir, ...)`

- [ ] **Step 1: Write failing tests**

Replace `tests/unit/test_sandbox_env.py` with:

```python
from pathlib import Path
from unittest.mock import patch

from click.testing import CliRunner

from inoio_sandbox.cli import run


def _mock_modules(volumes_mock, config_mock):
    volumes_mock.ensure_home_volume.return_value = "p-abc-home"
    config_mock.build_merged_config.return_value = Path("/tmp/cfg")


def test_run_reads_sandbox_env(tmp_path):
    with (
        patch("inoio_sandbox.cli.doctor_checks.check_all", return_value=True),
        patch("inoio_sandbox.cli.worktree_mod"),
        patch("inoio_sandbox.cli.image") as image_mock,
        patch("inoio_sandbox.cli.volumes") as volumes_mock,
        patch("inoio_sandbox.cli.config") as config_mock,
        patch("inoio_sandbox.cli.secrets"),
        patch("inoio_sandbox.cli.runner") as r,
        patch("os.execvp"),
    ):
        image_mock.dockerfile_hash.return_value = "abc123"
        image_mock.image_tag.return_value = "inoio-sandbox/runner:abc123"
        _mock_modules(volumes_mock, config_mock)
        test_runner = CliRunner()
        with test_runner.isolated_filesystem(temp_dir=tmp_path):
            Path(".sandbox").mkdir()
            (Path(".sandbox") / "env").write_text("FOO=bar\n# comment\n\nBAZ=qux\n")
            result = test_runner.invoke(run, [])
            assert result.exit_code == 0
            env_extra = r.build_msb_run_command.call_args.kwargs["env_extra"]
            assert env_extra == ["FOO=bar", "BAZ=qux"]


def test_run_passes_reset_home_to_volumes(tmp_path):
    with (
        patch("inoio_sandbox.cli.doctor_checks.check_all", return_value=True),
        patch("inoio_sandbox.cli.worktree_mod"),
        patch("inoio_sandbox.cli.image") as image_mock,
        patch("inoio_sandbox.cli.volumes") as volumes_mock,
        patch("inoio_sandbox.cli.config") as config_mock,
        patch("inoio_sandbox.cli.secrets"),
        patch("inoio_sandbox.cli.runner"),
        patch("os.execvp"),
    ):
        image_mock.dockerfile_hash.return_value = "abc123"
        image_mock.image_tag.return_value = "inoio-sandbox/runner:abc123"
        _mock_modules(volumes_mock, config_mock)
        test_runner = CliRunner()
        with test_runner.isolated_filesystem(temp_dir=tmp_path):
            result = test_runner.invoke(run, ["--reset-home"])
            assert result.exit_code == 0
            kwargs = volumes_mock.ensure_home_volume.call_args.kwargs
            assert kwargs["reset"] is True


def test_run_uses_image_hash_for_volume(tmp_path):
    with (
        patch("inoio_sandbox.cli.doctor_checks.check_all", return_value=True),
        patch("inoio_sandbox.cli.worktree_mod"),
        patch("inoio_sandbox.cli.image") as image_mock,
        patch("inoio_sandbox.cli.volumes") as volumes_mock,
        patch("inoio_sandbox.cli.config") as config_mock,
        patch("inoio_sandbox.cli.secrets"),
        patch("inoio_sandbox.cli.runner"),
        patch("os.execvp"),
    ):
        image_mock.dockerfile_hash.return_value = "abc123"
        image_mock.image_tag.return_value = "inoio-sandbox/runner:abc123"
        _mock_modules(volumes_mock, config_mock)
        test_runner = CliRunner()
        with test_runner.isolated_filesystem(temp_dir=tmp_path):
            result = test_runner.invoke(run, [])
            assert result.exit_code == 0
            args = volumes_mock.ensure_home_volume.call_args.args
            assert args[1] == "abc123"


def test_run_builds_merged_config_with_project_dir(tmp_path):
    with (
        patch("inoio_sandbox.cli.doctor_checks.check_all", return_value=True),
        patch("inoio_sandbox.cli.worktree_mod"),
        patch("inoio_sandbox.cli.image") as image_mock,
        patch("inoio_sandbox.cli.volumes") as volumes_mock,
        patch("inoio_sandbox.cli.config") as config_mock,
        patch("inoio_sandbox.cli.secrets"),
        patch("inoio_sandbox.cli.runner"),
        patch("os.execvp"),
    ):
        image_mock.dockerfile_hash.return_value = "abc123"
        image_mock.image_tag.return_value = "inoio-sandbox/runner:abc123"
        _mock_modules(volumes_mock, config_mock)
        test_runner = CliRunner()
        with test_runner.isolated_filesystem(temp_dir=tmp_path):
            Path(".sandbox").mkdir()
            Path(".sandbox/opencode").mkdir()
            result = test_runner.invoke(run, [])
            assert result.exit_code == 0
            args = config_mock.build_merged_config.call_args.args
            assert args[0] == Path.home() / ".config/inoio-sandbox/opencode"
            assert args[1] == Path(".sandbox/opencode")


def test_run_timing_flag_prints_phase_durations(tmp_path):
    with (
        patch("inoio_sandbox.cli.doctor_checks.check_all", return_value=True),
        patch("inoio_sandbox.cli.worktree_mod"),
        patch("inoio_sandbox.cli.image") as image_mock,
        patch("inoio_sandbox.cli.volumes") as volumes_mock,
        patch("inoio_sandbox.cli.config") as config_mock,
        patch("inoio_sandbox.cli.secrets"),
        patch("inoio_sandbox.cli.runner"),
        patch("os.execvp"),
    ):
        image_mock.dockerfile_hash.return_value = "abc123"
        image_mock.image_tag.return_value = "inoio-sandbox/runner:abc123"
        _mock_modules(volumes_mock, config_mock)
        test_runner = CliRunner()
        with test_runner.isolated_filesystem(temp_dir=tmp_path):
            result = test_runner.invoke(run, ["--timing"])
            assert result.exit_code == 0
            assert "[timing]" in result.output
            assert "preflight:" in result.output
            assert "total launcher overhead:" in result.output
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `.venv/bin/python -m pytest tests/unit/test_sandbox_env.py -v`

Expected: FAIL — `--reset-home` not defined, call signatures changed.

- [ ] **Step 3: Implement CLI changes**

Modify `src/inoio_sandbox/cli.py`:

1. Update the `run` decorator and function signature:

```python
@cli.command()
@click.option("--worktree", default=None, help="Worktree name")
@click.option("--image-rebuild", is_flag=True, help="Force image rebuild")
@click.option("--volume-fallback", is_flag=True, help="Use host directories instead of msb volumes")
@click.option("--reset-home", is_flag=True, help="Recreate the project home volume")
@click.option("--cpus", default=None, type=int, help="Number of CPUs (default: all)")
@click.option("--memory", default="4G", help="Memory limit (default: 4G)")
@click.option("--timing", is_flag=True, help="Print per-phase launcher timing to stderr")
def run(worktree, image_rebuild, volume_fallback, reset_home, cpus, memory, timing):
```

2. Replace the volume/config section with:

```python
    dockerfile = Path(".sandbox/Dockerfile") if Path(".sandbox/Dockerfile").exists() else DEFAULT_DOCKERFILE
    if image.references_base(dockerfile):
        image.ensure_base_image(DEFAULT_DOCKERFILE, force=image_rebuild)
    df_hash = image.dockerfile_hash(dockerfile)
    tag = image.image_tag(df_hash)
    image.build_and_load(dockerfile, tag, force=image_rebuild)
    tick("image hash/check/build")

    home_volume = volumes.ensure_home_volume(
        project, df_hash, STATE_DIR, tag, fallback=volume_fallback, reset=reset_home
    )
    tick("volume ensure")

    user_config_dir = Path.home() / ".config/inoio-sandbox/opencode"
    project_config_dir = Path(".sandbox/opencode") if Path(".sandbox/opencode").exists() else None
    config_tmp_dir = config.build_merged_config(
        user_config_dir, project_config_dir, DEFAULT_PROVIDER_CONFIG
    )
    secret_flags = secrets.secret_flags()
    cpus = cpus or runner.available_cpus()
    name = f"inoio-sandbox-{project}-{worktree_mod.branch_slug(branch)}"[:128]
    tick("config/secrets")
```

3. Update the `runner.build_msb_run_command` call:

```python
    cmd = runner.build_msb_run_command(
        image_tag=tag,
        name=name,
        worktree=wt,
        home_volume=home_volume,
        config_tmp_dir=config_tmp_dir,
        secret_flags=secret_flags,
        env_extra=env_extra,
        cpus=cpus,
        memory=memory,
    )
```

4. Update the three scripts (`scripts/diagnose-startup.py`,
   `scripts/inspect-lsp-location.py`, `scripts/test-lsp-symlink.py`) to use
   the new API:
   - Replace `volumes.ensure_volumes(...)` with `volumes.ensure_home_volume(...)`.
   - Replace `config.build_config_content(...)` with `config.build_merged_config(...)`.
   - Update `runner.build_msb_run_command(...)` to pass `home_volume` and
     `config_tmp_dir` instead of `local`, `cache`, and `config_content`.
   - Each script already duplicates the CLI assembly logic; mirror the CLI
     changes above.

- [ ] **Step 4: Run tests to verify they pass**

Run: `.venv/bin/python -m pytest tests/unit/test_sandbox_env.py -v`

Expected: PASS.

- [ ] **Step 5: Smoke-check scripts**

Run a syntax check on each updated script to ensure imports resolve:

```bash
.venv/bin/python -m py_compile scripts/diagnose-startup.py
.venv/bin/python -m py_compile scripts/inspect-lsp-location.py
.venv/bin/python -m py_compile scripts/test-lsp-symlink.py
```

Expected: no output (success).

- [ ] **Step 6: Commit**

```bash
git add src/inoio_sandbox/cli.py tests/unit/test_sandbox_env.py scripts/
git commit -m "feat: wire home volume, reset flag, and merged config through CLI and scripts"
```

---

### Task 5: Full test suite and cleanup

**Files:**
- All test files above plus any newly broken tests.

- [ ] **Step 1: Run full unit test suite**

Run: `.venv/bin/python -m pytest tests/unit -v`

Expected: PASS. If any tests fail, fix the underlying issue (do not change test
assertions to match broken behavior).

- [ ] **Step 2: Lint / type check (if configured)**

Run any existing lint command from the project. If none, skip.

- [ ] **Step 3: Commit any remaining fixes**

```bash
git add -A
git commit -m "test: update unit tests for home volume and config injection"
```

---

## Self-review

**Spec coverage:**
- Home volume per project + image hash: Task 1.
- `.local` / `.cache` folded into home volume: Task 1 (no separate mounts) and Task 3.
- Config injection from `~/.config/inoio-sandbox/opencode/` and `.sandbox/opencode/`: Task 2 and Task 4.
- Deep merge all JSON/JSONC, copy non-JSON, project overrides user, inoio-sandbox wins: Task 2.
- `--reset-home` flag: Task 1 and Task 4.
- No auto-pruning: not implemented (out of scope, documented in spec).
- No `OPENCODE_CONFIG_CONTENT`: Task 3 removes it.

**Placeholder scan:** No TBD/TODO/"implement later"/"similar to Task N" patterns found.

**Type consistency:**
- `home_volume` is `str | Path` throughout.
- `config_tmp_dir` is `Path` throughout.
- `ensure_home_volume` signature matches all call sites.

**Gaps:** None identified.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-21-home-volume-config-plan.md`.

Two execution options:

1. **Subagent-Driven (recommended)** — dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — execute tasks in this session using `executing-plans`, batch execution with checkpoints.

Which approach do you want?
