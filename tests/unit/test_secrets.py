from unittest.mock import patch

from inoio_sandbox import secrets


def test_secret_flags_all_present():
    env = {"LITELLM_API_KEY": "abc", "GITHUB_TOKEN": "def"}
    with patch("os.environ", env):
        flags = secrets.secret_flags()
        assert "--secret" in flags
        assert "LITELLM_API_KEY@litellm.inoio.de" in flags
        assert "GITHUB_TOKEN@github.com" in flags


def test_secret_flags_missing_warns():
    env = {}
    with patch("os.environ", env):
        with patch("click.echo") as echo:
            flags = secrets.secret_flags()
            assert flags == []
            echo.assert_called()
