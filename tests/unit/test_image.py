import hashlib
import subprocess
from pathlib import Path

import pytest

from inoio_sandbox import image


class _FakeResult:
    def __init__(self, stdout="", returncode=0):
        self.stdout = stdout
        self.returncode = returncode


def test_dockerfile_hash(tmp_path):
    df = tmp_path / "Dockerfile"
    df.write_text("FROM debian\nRUN echo hi\n")
    h = image.dockerfile_hash(df)
    expected = hashlib.sha256(df.read_bytes()).hexdigest()[:12]
    assert h == expected


def test_image_tag():
    h = "abc123"
    assert image.image_tag(h) == "inoio-sandbox/runner:abc123"


def test_image_exists_true(monkeypatch):
    def fake_run(cmd, **kwargs):
        assert kwargs.get("check") is True
        return _FakeResult(stdout="other\ninoio-sandbox/runner:abc123\n")

    monkeypatch.setattr("inoio_sandbox.image.subprocess.run", fake_run)
    assert image.image_exists("inoio-sandbox/runner:abc123") is True


def test_image_exists_false(monkeypatch):
    def fake_run(cmd, **kwargs):
        assert kwargs.get("check") is True
        return _FakeResult(stdout="other\nanother\n")

    monkeypatch.setattr("inoio_sandbox.image.subprocess.run", fake_run)
    assert image.image_exists("inoio-sandbox/runner:abc123") is False


def test_image_exists_propagates_error(monkeypatch):
    def fake_run(cmd, **kwargs):
        raise subprocess.CalledProcessError(1, cmd)

    monkeypatch.setattr("inoio_sandbox.image.subprocess.run", fake_run)
    with pytest.raises(subprocess.CalledProcessError):
        image.image_exists("inoio-sandbox/runner:abc123")


def test_build_and_load_skips_when_image_exists(monkeypatch, tmp_path):
    df = tmp_path / "Dockerfile"
    df.write_text("FROM debian\n")
    runs = []

    def fake_run(cmd, **kwargs):
        runs.append((cmd, kwargs))
        return _FakeResult()

    monkeypatch.setattr("inoio_sandbox.image.subprocess.run", fake_run)
    monkeypatch.setattr("inoio_sandbox.image.image_exists", lambda tag: True)

    image.build_and_load(df, "inoio-sandbox/runner:abc123", force=False)
    assert len(runs) == 0


def test_build_and_load_builds_when_image_absent(monkeypatch, tmp_path):
    df = tmp_path / "Dockerfile"
    df.write_text("FROM debian\n")
    runs = []

    def fake_run(cmd, **kwargs):
        runs.append((cmd, kwargs))
        return _FakeResult()

    monkeypatch.setattr("inoio_sandbox.image.subprocess.run", fake_run)
    monkeypatch.setattr("inoio_sandbox.image.image_exists", lambda tag: False)

    image.build_and_load(df, "inoio-sandbox/runner:abc123")
    assert len(runs) == 2
    assert runs[0][0] == [
        "docker",
        "build",
        "-f",
        str(df),
        "-t",
        "inoio-sandbox/runner:abc123",
        str(df.parent),
    ]
    assert runs[1][0] == (
        "set -o pipefail; docker save inoio-sandbox/runner:abc123 | "
        "msb load --tag inoio-sandbox/runner:abc123"
    )
    assert runs[1][1]["shell"] is True


def test_build_and_load_quotes_tag_to_prevent_injection(monkeypatch, tmp_path):
    df = tmp_path / "Dockerfile"
    df.write_text("FROM debian\n")
    runs = []

    def fake_run(cmd, **kwargs):
        runs.append((cmd, kwargs))
        return _FakeResult()

    monkeypatch.setattr("inoio_sandbox.image.subprocess.run", fake_run)
    monkeypatch.setattr("inoio_sandbox.image.image_exists", lambda tag: False)

    image.build_and_load(df, "inoio-sandbox/runner:abc 123")
    assert runs[1][0] == (
        "set -o pipefail; docker save 'inoio-sandbox/runner:abc 123' | "
        "msb load --tag 'inoio-sandbox/runner:abc 123'"
    )


def test_build_and_load_builds_when_force_true(monkeypatch, tmp_path):
    df = tmp_path / "Dockerfile"
    df.write_text("FROM debian\n")
    runs = []

    def fake_run(cmd, **kwargs):
        runs.append((cmd, kwargs))
        return _FakeResult()

    monkeypatch.setattr("inoio_sandbox.image.subprocess.run", fake_run)
    monkeypatch.setattr("inoio_sandbox.image.image_exists", lambda tag: True)

    image.build_and_load(df, "inoio-sandbox/runner:abc123", force=True)
    assert len(runs) == 2
