import hashlib
from pathlib import Path

from inoio_sandbox import image


def test_dockerfile_hash(tmp_path):
    df = tmp_path / "Dockerfile"
    df.write_text("FROM debian\nRUN echo hi\n")
    h = image.dockerfile_hash(df)
    expected = hashlib.sha256(df.read_bytes()).hexdigest()[:12]
    assert h == expected


def test_image_tag():
    h = "abc123"
    assert image.image_tag(h) == "inoio-sandbox/runner:abc123"
