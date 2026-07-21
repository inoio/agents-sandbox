from unittest.mock import patch

from inoio_sandbox import secrets


def test_secret_flags_all_present():
    env = {"LITELLM_API_KEY": "abc", "GITHUB_TOKEN": "def"}
    with patch("os.environ", env):
        flags = secrets.secret_flags()
        assert flags == [
            "--secret",
            "LITELLM_API_KEY@litellm.inoio.de",
            "--secret",
            "GITHUB_TOKEN@github.com",
        ]


def test_secret_flags_missing_warns():
    env = {}
    with patch("os.environ", env):
        with patch("inoio_sandbox.log.warn") as warn:
            flags = secrets.secret_flags()
            assert flags == []
            assert warn.call_count == len(secrets.SECRET_MAP)
            for var in secrets.SECRET_MAP:
                matching = [
                    call
                    for call in warn.call_args_list
                    if var in call.args[0] and "not set" in call.args[0]
                ]
                assert len(matching) == 1
