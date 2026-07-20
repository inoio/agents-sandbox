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
    assert cmd[-3:] == ["inoio-sandbox/runner:abc", "--", "opencode"]
