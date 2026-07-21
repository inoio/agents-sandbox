import json
from pathlib import Path

from inoio_sandbox import config


def test_load_provider_config(tmp_path):
    data = {"provider": {"x": 1}}
    f = tmp_path / "provider-config.json"
    f.write_text(json.dumps(data))
    assert config.load_provider_config(f) == data


def test_deep_merge_replaces_arrays_and_merges_dicts():
    base = {"a": [1], "b": {"x": 1, "y": 2}}
    override = {"a": [2], "b": {"y": 3, "z": 4}}
    assert config.deep_merge(base, override) == {"a": [2], "b": {"x": 1, "y": 3, "z": 4}}


def test_build_merged_config_creates_temp_dir_with_merged_json(tmp_path):
    user_dir = tmp_path / "user"
    project_dir = tmp_path / "project"
    user_dir.mkdir()
    project_dir.mkdir()

    (user_dir / "opencode.jsonc").write_text(json.dumps({"a": 1, "b": {"x": 1}}))
    (project_dir / "opencode.jsonc").write_text(json.dumps({"b": {"y": 2}}))

    provider = tmp_path / "provider-config.json"
    provider.write_text(json.dumps({"provider": {"litellm": {"models": {"m": {}}}}}))

    result_dir = config.build_merged_config(user_dir, project_dir, provider)
    merged = json.loads((result_dir / "opencode.jsonc").read_text())
    assert merged["a"] == 1
    assert merged["b"] == {"x": 1, "y": 2}
    assert merged["provider"]["litellm"]["models"] == {"m": {}}


def test_build_merged_config_project_overrides_user_non_json(tmp_path):
    user_dir = tmp_path / "user"
    project_dir = tmp_path / "project"
    user_dir.mkdir()
    project_dir.mkdir()

    (user_dir / "plugin.txt").write_text("user")
    (project_dir / "plugin.txt").write_text("project")

    provider = tmp_path / "provider-config.json"
    provider.write_text(json.dumps({"provider": {"litellm": {}}}))

    result_dir = config.build_merged_config(user_dir, project_dir, provider)
    assert (result_dir / "plugin.txt").read_text() == "project"


def test_build_merged_config_without_project_dir(tmp_path):
    user_dir = tmp_path / "user"
    user_dir.mkdir()
    (user_dir / "opencode.jsonc").write_text(json.dumps({"a": 1}))

    provider = tmp_path / "provider-config.json"
    provider.write_text(json.dumps({"provider": {"litellm": {}}}))

    result_dir = config.build_merged_config(user_dir, None, provider)
    merged = json.loads((result_dir / "opencode.jsonc").read_text())
    assert merged["a"] == 1
    assert merged["provider"]["litellm"] == {}


def test_build_merged_config_creates_opencode_jsonc_when_missing(tmp_path):
    provider = tmp_path / "provider-config.json"
    provider.write_text(json.dumps({"provider": {"litellm": {"models": {"m": {}}}}}))

    result_dir = config.build_merged_config(tmp_path / "user", None, provider)
    merged = json.loads((result_dir / "opencode.jsonc").read_text())
    assert merged == {"provider": {"litellm": {"models": {"m": {}}}}}
