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
