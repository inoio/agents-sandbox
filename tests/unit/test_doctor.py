from unittest.mock import patch

from click.testing import CliRunner

from inoio_sandbox import doctor
from inoio_sandbox.cli import cli


def test_check_msb_missing(capsys):
    with patch("shutil.which", return_value=None):
        result = doctor.check_msb()
        assert result is False
        captured = capsys.readouterr()
        assert "msb not found" in captured.err


def test_check_docker_missing(capsys):
    with patch("shutil.which", return_value=None):
        result = doctor.check_docker()
        assert result is False
        captured = capsys.readouterr()
        assert "docker not found" in captured.err


def test_check_kvm_missing(capsys):
    with patch("os.path.exists", return_value=False):
        result = doctor.check_kvm()
        assert result is False
        captured = capsys.readouterr()
        assert "/dev/kvm not found" in captured.err


@patch("shutil.which", return_value="/usr/bin/git")
def test_check_git_healthy(mock_which):
    assert doctor.check_git() is True


@patch("shutil.which", return_value=None)
def test_check_git_missing(mock_which, capsys):
    result = doctor.check_git()
    assert result is False
    captured = capsys.readouterr()
    assert "git not found" in captured.err
    assert "Install git" in captured.err


@patch("shutil.which", side_effect=lambda cmd: f"/usr/bin/{cmd}")
@patch("os.path.exists", return_value=True)
def test_check_all_healthy(mock_exists, mock_which, capsys):
    assert doctor.check_all() is True
    captured = capsys.readouterr()
    assert captured.err == ""


@patch("shutil.which", return_value=None)
@patch("os.path.exists", return_value=False)
def test_check_all_fails_when_msb_missing(mock_exists, mock_which, capsys):
    assert doctor.check_all() is False
    captured = capsys.readouterr()
    assert "msb not found" in captured.err


@patch("os.path.exists", return_value=True)
def test_check_all_fails_when_docker_missing(mock_exists, capsys):
    def _which(cmd):
        if cmd == "docker":
            return None
        return f"/usr/bin/{cmd}"

    with patch("shutil.which", side_effect=_which):
        assert doctor.check_all() is False
        captured = capsys.readouterr()
        assert "docker not found" in captured.err


@patch("shutil.which", side_effect=lambda cmd: f"/usr/bin/{cmd}")
def test_check_all_fails_when_kvm_missing(mock_which, capsys):
    with patch("os.path.exists", return_value=False):
        assert doctor.check_all() is False
        captured = capsys.readouterr()
        assert "/dev/kvm not found" in captured.err


@patch("shutil.which", side_effect=lambda cmd: f"/usr/bin/{cmd}")
@patch("os.path.exists", return_value=True)
def test_check_all_fails_when_git_missing(mock_exists, mock_which, capsys):
    def _which(cmd):
        if cmd == "git":
            return None
        return f"/usr/bin/{cmd}"

    with patch("shutil.which", side_effect=_which):
        assert doctor.check_all() is False
        captured = capsys.readouterr()
        assert "git not found" in captured.err


def test_cli_doctor_success():
    runner = CliRunner()
    with patch("inoio_sandbox.cli.doctor_checks.check_all", return_value=True):
        result = runner.invoke(cli, ["doctor"])
        assert result.exit_code == 0
        assert "doctor: all checks passed" in result.output


def test_cli_doctor_failure():
    runner = CliRunner()
    with patch("inoio_sandbox.cli.doctor_checks.check_all", return_value=False):
        result = runner.invoke(cli, ["doctor"])
        assert result.exit_code != 0
        assert "Error: preflight failed" in result.output
