from pathlib import Path
from unittest.mock import patch

from click.testing import CliRunner

from inoio_sandbox.cli import run


def test_run_reads_sandbox_env(tmp_path):
    with patch("inoio_sandbox.cli.doctor_checks.check_all", return_value=True):
        with patch("inoio_sandbox.cli.worktree_mod"):
            with patch("inoio_sandbox.cli.image"):
                with patch("inoio_sandbox.cli.volumes") as volumes_mock:
                    volumes_mock.ensure_volumes.return_value = (Path("/local"), Path("/cache"))
                    with patch("inoio_sandbox.cli.config"):
                        with patch("inoio_sandbox.cli.secrets"):
                            with patch("inoio_sandbox.cli.runner") as r:
                                with patch("os.execvp"):
                                    test_runner = CliRunner()
                                    with test_runner.isolated_filesystem(temp_dir=tmp_path):
                                        Path(".sandbox").mkdir()
                                        (Path(".sandbox") / "env").write_text("FOO=bar\n# comment\n\nBAZ=qux\n")
                                        result = test_runner.invoke(run, [])
                                        assert result.exit_code == 0
                                        call = r.build_msb_run_command.call_args
                                        assert "FOO=bar" in call.kwargs["env_extra"]
                                        assert "BAZ=qux" in call.kwargs["env_extra"]
