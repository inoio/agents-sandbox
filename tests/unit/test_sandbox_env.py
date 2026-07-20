from pathlib import Path
from unittest.mock import patch

from click.testing import CliRunner

from inoio_sandbox.cli import run


def test_run_reads_sandbox_env(tmp_path):
    with (
        patch("inoio_sandbox.cli.doctor_checks.check_all", return_value=True),
        patch("inoio_sandbox.cli.worktree_mod"),
        patch("inoio_sandbox.cli.image"),
        patch("inoio_sandbox.cli.volumes") as volumes_mock,
        patch("inoio_sandbox.cli.config"),
        patch("inoio_sandbox.cli.secrets"),
        patch("inoio_sandbox.cli.runner") as r,
        patch("os.execvp"),
    ):
        volumes_mock.ensure_volumes.return_value = (Path("/local"), Path("/cache"))
        test_runner = CliRunner()
        with test_runner.isolated_filesystem(temp_dir=tmp_path):
            Path(".sandbox").mkdir()
            (Path(".sandbox") / "env").write_text("FOO=bar\n# comment\n\nBAZ=qux\n")
            result = test_runner.invoke(run, [])
            assert result.exit_code == 0
            env_extra = r.build_msb_run_command.call_args.kwargs["env_extra"]
            assert env_extra == ["FOO=bar", "BAZ=qux"]
