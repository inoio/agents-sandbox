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


def test_check_all_healthy():
    with patch("shutil.which", return_value="/usr/bin/msb"):
        with patch("inoio_sandbox.doctor.check_docker", return_value=True):
            with patch("inoio_sandbox.doctor.check_kvm", return_value=True):
                assert doctor.check_all() is True
