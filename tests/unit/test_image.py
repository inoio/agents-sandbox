import hashlib
import subprocess
from pathlib import Path

import pytest

from inoio_sandbox import image


class _FakeResult:
    def __init__(self, stdout="", returncode=0):
        self.stdout = stdout
        self.returncode = returncode


class _FakePipe:
    def close(self):
        pass


class _FakePopen:
    def __init__(self, cmd, **kwargs):
        self.args = cmd
        self.stdout = _FakePipe() if kwargs.get("stdout") == subprocess.PIPE else None
        self.returncode = 0

    def wait(self):
        return self.returncode


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
        return _FakeResult(stdout='[{"reference": "other"}, {"reference": "inoio-sandbox/runner:abc123"}]')

    monkeypatch.setattr("inoio_sandbox.image.subprocess.run", fake_run)
    assert image.image_exists("inoio-sandbox/runner:abc123") is True


def test_image_exists_false(monkeypatch):
    def fake_run(cmd, **kwargs):
        assert kwargs.get("check") is True
        return _FakeResult(stdout='[{"reference": "other"}, {"reference": "another"}]')

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
    popens = []

    def fake_run(cmd, **kwargs):
        runs.append((cmd, kwargs))
        return _FakeResult()

    def fake_popen(cmd, **kwargs):
        popens.append((cmd, kwargs))
        return _FakePopen(cmd, **kwargs)

    monkeypatch.setattr("inoio_sandbox.image.subprocess.run", fake_run)
    monkeypatch.setattr("inoio_sandbox.image.subprocess.Popen", fake_popen)
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
        str(Path.cwd()),
    ]
    assert runs[0][1]["check"] is True
    assert runs[1][0] == ["msb", "load", "--tag", "inoio-sandbox/runner:abc123"]
    assert runs[1][1]["check"] is True
    assert len(popens) == 1
    assert popens[0][0] == ["docker", "save", "inoio-sandbox/runner:abc123"]
    assert popens[0][1]["stdout"] == subprocess.PIPE


def test_build_and_load_handles_tag_with_spaces(monkeypatch, tmp_path):
    df = tmp_path / "Dockerfile"
    df.write_text("FROM debian\n")
    runs = []
    popens = []

    def fake_run(cmd, **kwargs):
        runs.append((cmd, kwargs))
        return _FakeResult()

    def fake_popen(cmd, **kwargs):
        popens.append((cmd, kwargs))
        return _FakePopen(cmd, **kwargs)

    monkeypatch.setattr("inoio_sandbox.image.subprocess.run", fake_run)
    monkeypatch.setattr("inoio_sandbox.image.subprocess.Popen", fake_popen)
    monkeypatch.setattr("inoio_sandbox.image.image_exists", lambda tag: False)

    image.build_and_load(df, "inoio-sandbox/runner:abc 123")
    assert popens[0][0] == ["docker", "save", "inoio-sandbox/runner:abc 123"]
    assert runs[1][0] == ["msb", "load", "--tag", "inoio-sandbox/runner:abc 123"]


def test_build_and_load_builds_when_force_true(monkeypatch, tmp_path):
    df = tmp_path / "Dockerfile"
    df.write_text("FROM debian\n")
    runs = []
    popens = []

    def fake_run(cmd, **kwargs):
        runs.append((cmd, kwargs))
        return _FakeResult()

    def fake_popen(cmd, **kwargs):
        popens.append((cmd, kwargs))
        return _FakePopen(cmd, **kwargs)

    monkeypatch.setattr("inoio_sandbox.image.subprocess.run", fake_run)
    monkeypatch.setattr("inoio_sandbox.image.subprocess.Popen", fake_popen)
    monkeypatch.setattr("inoio_sandbox.image.image_exists", lambda tag: True)

    image.build_and_load(df, "inoio-sandbox/runner:abc123", force=True)
    assert len(runs) == 2
    assert len(popens) == 1


def test_base_tag_constant():
    assert image.BASE_TAG == "inoio-sandbox/runner:base"


def test_references_base_true_when_from_references_base(tmp_path):
    df = tmp_path / "Dockerfile"
    df.write_text("FROM inoio-sandbox/runner:base\nRUN apt-get install shellcheck\n")
    assert image.references_base(df) is True


def test_references_base_true_ignores_leading_whitespace(tmp_path):
    df = tmp_path / "Dockerfile"
    df.write_text("  FROM inoio-sandbox/runner:base\n")
    assert image.references_base(df) is True


def test_references_base_false_for_unrelated_from(tmp_path):
    df = tmp_path / "Dockerfile"
    df.write_text("FROM debian:trixie-slim\nRUN echo hi\n")
    assert image.references_base(df) is False


def test_references_base_false_when_base_appears_only_in_a_comment(tmp_path):
    df = tmp_path / "Dockerfile"
    df.write_text("# builds on inoio-sandbox/runner:base\nFROM debian:trixie-slim\n")
    assert image.references_base(df) is False


def test_ensure_base_image_delegates_to_build_and_load(monkeypatch, tmp_path):
    df = tmp_path / "Dockerfile"
    df.write_text("FROM debian\n")
    calls = []

    def fake_build_and_load(dockerfile, tag, force=False):
        calls.append((dockerfile, tag, force))

    monkeypatch.setattr("inoio_sandbox.image.build_and_load", fake_build_and_load)

    image.ensure_base_image(df, force=False)
    assert calls == [(df, image.BASE_TAG, False)]


def test_ensure_base_image_passes_force_through(monkeypatch, tmp_path):
    df = tmp_path / "Dockerfile"
    df.write_text("FROM debian\n")
    calls = []

    def fake_build_and_load(dockerfile, tag, force=False):
        calls.append((dockerfile, tag, force))

    monkeypatch.setattr("inoio_sandbox.image.build_and_load", fake_build_and_load)

    image.ensure_base_image(df, force=True)
    assert calls == [(df, image.BASE_TAG, True)]
