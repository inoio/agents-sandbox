from pathlib import Path
from unittest.mock import patch

from click.testing import CliRunner

from inoio_sandbox.cli import run


def _mock_modules(volumes_mock, config_mock, config_path=None):
    volumes_mock.ensure_home_volume.return_value = "p-abc-home"
    config_mock.build_merged_config.return_value = config_path or Path("/dev/null/cfg")


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
        _mock_modules(volumes_mock, config_mock, config_path=tmp_path / "cfg")
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
        _mock_modules(volumes_mock, config_mock, config_path=tmp_path / "cfg")
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
        _mock_modules(volumes_mock, config_mock, config_path=tmp_path / "cfg")
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
        _mock_modules(volumes_mock, config_mock, config_path=tmp_path / "cfg")
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
        _mock_modules(volumes_mock, config_mock, config_path=tmp_path / "cfg")
        test_runner = CliRunner()
        with test_runner.isolated_filesystem(temp_dir=tmp_path):
            result = test_runner.invoke(run, ["--timing"])
            assert result.exit_code == 0
            assert "[timing]" in result.output
            assert "preflight:" in result.output
            assert "total launcher overhead:" in result.output
