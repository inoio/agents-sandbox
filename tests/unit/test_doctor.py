from unittest.mock import patch

from inoio_sandbox import doctor


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


@patch("shutil.which", return_value="/usr/bin/msb")
@patch("os.path.exists", return_value=True)
def test_check_all_healthy(mock_exists, mock_which):
    assert doctor.check_all() is True


@patch("shutil.which", return_value=None)
def test_check_all_failing(mock_which):
    assert doctor.check_all() is False
