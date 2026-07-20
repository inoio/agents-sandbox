import subprocess

import pytest

from inoio_sandbox import volumes


def test_volume_names():
    assert volumes.local_volume_name("p-deadbeef") == "p-deadbeef-opencode-local"
    assert volumes.cache_volume_name("p-deadbeef") == "p-deadbeef-opencode-cache"


def test_volume_paths(tmp_path):
    local, cache = volumes.fallback_paths(tmp_path, "p-deadbeef")
    assert local == tmp_path / "state" / "p-deadbeef" / "local"
    assert cache == tmp_path / "state" / "p-deadbeef" / "cache"


def test_ensure_msb_volume_missing_binary_raises_runtime_error(monkeypatch):
    def raise_not_found(*args, **kwargs):
        raise FileNotFoundError("msb")

    monkeypatch.setattr(subprocess, "run", raise_not_found)

    with pytest.raises(
        RuntimeError,
        match="msb not found. Install microsandbox: https://github.com/microsandbox/microsandbox",
    ):
        volumes.ensure_msb_volume("foo")


def test_ensure_volumes_fallback_true_returns_host_paths_and_skips_msb(
    monkeypatch, tmp_path
):
    called = False

    def fake_run(*args, **kwargs):
        nonlocal called
        called = True
        return subprocess.CompletedProcess(args=args, returncode=0)

    monkeypatch.setattr(subprocess, "run", fake_run)

    local, cache = volumes.ensure_volumes("p-deadbeef", tmp_path, fallback=True)

    assert not called
    assert local == tmp_path / "state" / "p-deadbeef" / "local"
    assert cache == tmp_path / "state" / "p-deadbeef" / "cache"


def test_ensure_volumes_both_created_returns_volume_names(monkeypatch, tmp_path):
    def fake_run(*args, **kwargs):
        return subprocess.CompletedProcess(args=args, returncode=0)

    monkeypatch.setattr(subprocess, "run", fake_run)

    local, cache = volumes.ensure_volumes("p-deadbeef", tmp_path)

    assert local == "p-deadbeef-opencode-local"
    assert cache == "p-deadbeef-opencode-cache"


def test_ensure_volumes_creation_fails_returns_fallback_paths_and_warns(
    monkeypatch, tmp_path, capsys
):
    def fake_run(*args, **kwargs):
        return subprocess.CompletedProcess(args=args, returncode=1, stderr=b"error")

    monkeypatch.setattr(subprocess, "run", fake_run)

    local, cache = volumes.ensure_volumes("p-deadbeef", tmp_path)

    assert local == tmp_path / "state" / "p-deadbeef" / "local"
    assert cache == tmp_path / "state" / "p-deadbeef" / "cache"
    assert "msb volume creation failed" in capsys.readouterr().err
