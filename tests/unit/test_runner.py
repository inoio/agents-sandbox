from pathlib import Path

from inoio_sandbox import runner


def test_build_command_uses_named_volumes():
    cmd = runner.build_msb_run_command(
        image_tag="inoio-sandbox/runner:abc",
        name="inoio-sandbox-p-branch",
        worktree=Path("/wt"),
        local="p-x-local",
        cache="p-x-cache",
        config_content="OPENCODE_CONFIG_CONTENT={}",
        secret_flags=["--secret", "LITELLM_API_KEY@litellm.inoio.de"],
        env_extra=["FOO=bar"],
        cpus=2,
        memory="4G",
    )
    assert cmd[0] == "msb"
    assert "run" in cmd
    assert "--name" in cmd
    assert "inoio-sandbox-p-branch" in cmd
    assert "--replace" in cmd
    assert "-c" in cmd
    assert "2" in cmd
    assert "--max-cpus" in cmd
    assert "-m" in cmd
    assert "4G" in cmd
    assert "--max-memory" in cmd
    assert "-u" in cmd
    assert "dev" in cmd
    assert "/wt:/home/dev/workspace" in cmd
    assert "p-x-local:/home/dev/.local" in cmd
    assert "p-x-cache:/home/dev/.cache" in cmd
    assert "--secret" in cmd
    assert "LITELLM_API_KEY@litellm.inoio.de" in cmd
    assert "FOO=bar" in cmd
    assert "HOME=/home/dev" in cmd
    assert cmd[-3:] == ["inoio-sandbox/runner:abc", "--", "opencode"]


def test_build_command_hides_envrc_files(tmp_path):
    (tmp_path / ".envrc").write_text("secret\n")
    (tmp_path / ".envrc.local").write_text("local secret\n")
    cmd = runner.build_msb_run_command(
        image_tag="inoio-sandbox/runner:abc",
        name="inoio-sandbox-p-branch",
        worktree=tmp_path,
        local="p-x-local",
        cache="p-x-cache",
        config_content="OPENCODE_CONFIG_CONTENT={}",
        secret_flags=[],
        env_extra=[],
        cpus=1,
        memory="4G",
    )
    assert "--rm" in cmd
    assert f"{tmp_path}/.envrc" not in cmd
    assert "/home/dev/workspace/.envrc" in cmd
    assert "/home/dev/workspace/.envrc.local" in cmd


def test_available_memory_gib_returns_positive_integer():
    assert runner.available_memory_gib() > 0


def test_available_cpus_returns_positive_integer():
    assert runner.available_cpus() > 0
