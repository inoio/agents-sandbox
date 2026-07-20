from inoio_sandbox import volumes


def test_volume_names():
    assert volumes.local_volume_name("p-deadbeef") == "p-deadbeef-opencode-local"
    assert volumes.cache_volume_name("p-deadbeef") == "p-deadbeef-opencode-cache"


def test_volume_paths(tmp_path):
    local, cache = volumes.fallback_paths(tmp_path, "p-deadbeef")
    assert local == tmp_path / "state" / "p-deadbeef" / "local"
    assert cache == tmp_path / "state" / "p-deadbeef" / "cache"
