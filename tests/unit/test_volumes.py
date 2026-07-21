import subprocess
from pathlib import Path

from inoio_sandbox import volumes


def test_home_volume_name_includes_image_hash():
    assert volumes.home_volume_name("myproject", "abc123") == "myproject-opencode-home-abc123"


def test_fallback_home_path():
    path = volumes.fallback_home_path(Path("/state"), "myproject", "abc123")
    assert path == Path("/state/state/myproject/home/abc123")


def test_remove_home_volume_invokes_msb(monkeypatch):
    called = []

    def fake_run(cmd, **kwargs):
        called.append(cmd)
        return subprocess.CompletedProcess(args=cmd, returncode=0)

    monkeypatch.setattr(subprocess, "run", fake_run)
    volumes.remove_home_volume("myproject-opencode-home-abc123")
    assert called == [["msb", "volume", "remove", "myproject-opencode-home-abc123"]]


def test_prefill_home_volume_invokes_msb(monkeypatch):
    called = []

    def fake_run(cmd, **kwargs):
        called.append(cmd)
        return subprocess.CompletedProcess(args=cmd, returncode=0)

    monkeypatch.setattr(subprocess, "run", fake_run)
    volumes.prefill_home_volume("myproject-opencode-home-abc123", "inoio-sandbox/runner:abc123")
    assert called[0][:3] == ["msb", "run", "-v"]
    assert called[0][3] == "myproject-opencode-home-abc123:/mnt/home"
    assert called[0][4] == "inoio-sandbox/runner:abc123"


def test_ensure_home_volume_prefills_when_created(monkeypatch, tmp_path):
    created = []
    prefilled = []

    def fake_ensure(name):
        created.append(name)
        return True  # newly created

    def fake_prefill(name, tag):
        prefilled.append((name, tag))

    monkeypatch.setattr(volumes, "ensure_msb_volume", fake_ensure)
    monkeypatch.setattr(volumes, "prefill_home_volume", fake_prefill)

    result = volumes.ensure_home_volume("myproject", "abc123", tmp_path, "tag:x")
    assert result == "myproject-opencode-home-abc123"
    assert created == ["myproject-opencode-home-abc123"]
    assert prefilled == [("myproject-opencode-home-abc123", "tag:x")]


def test_ensure_home_volume_reset_removes_then_creates(monkeypatch, tmp_path):
    removed = []
    created = []

    def fake_remove(name):
        removed.append(name)

    def fake_ensure(name):
        created.append(name)
        return True

    monkeypatch.setattr(volumes, "remove_home_volume", fake_remove)
    monkeypatch.setattr(volumes, "prefill_home_volume", lambda *a: None)
    monkeypatch.setattr(volumes, "ensure_msb_volume", fake_ensure)

    volumes.ensure_home_volume("myproject", "abc123", tmp_path, "tag:x", reset=True)
    assert removed == ["myproject-opencode-home-abc123"]
    assert created == ["myproject-opencode-home-abc123"]
