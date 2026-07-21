import json
import json5
import tempfile
from pathlib import Path


def load_provider_config(path: Path) -> dict:
    return json5.loads(path.read_text())


def deep_merge(base: dict, override: dict) -> dict:
    result = dict(base)
    for key, value in override.items():
        if key in result and isinstance(result[key], dict) and isinstance(value, dict):
            result[key] = deep_merge(result[key], value)
        else:
            result[key] = value
    return result


def _load_jsonc(path: Path) -> dict:
    return json5.loads(path.read_text())


def _is_json_file(path: Path) -> bool:
    return path.suffix in (".json", ".jsonc")


def _merge_json_files(
    user_dir: Path | None,
    project_dir: Path | None,
    provider_config: dict,
) -> dict[str, dict]:
    files: dict[str, dict] = {}
    for source in [user_dir, project_dir]:
        if source is None or not source.exists():
            continue
        for path in sorted(source.iterdir()):
            if not _is_json_file(path):
                continue
            name = path.name
            data = _load_jsonc(path)
            files[name] = deep_merge(files.get(name, {}), data)

    provider_branch = {"provider": {"litellm": provider_config["provider"]["litellm"]}}
    for name in ["opencode.jsonc", "opencode.json"]:
        if name in files:
            files[name] = deep_merge(files[name], provider_branch)
            break
    else:
        files["opencode.jsonc"] = provider_branch

    return files


def _collect_other_files(
    user_dir: Path | None,
    project_dir: Path | None,
) -> dict[str, bytes]:
    files: dict[str, bytes] = {}
    for source in [user_dir, project_dir]:
        if source is None or not source.exists():
            continue
        for path in sorted(source.iterdir()):
            if _is_json_file(path):
                continue
            files[path.name] = path.read_bytes()
    return files


def build_merged_config(
    user_dir: Path,
    project_dir: Path | None,
    provider_config_path: Path,
) -> Path:
    provider_config = load_provider_config(provider_config_path)
    json_files = _merge_json_files(user_dir, project_dir, provider_config)
    other_files = _collect_other_files(user_dir, project_dir)

    tmp_dir = Path(tempfile.mkdtemp(prefix="inoio-sandbox-config-"))
    for name, data in json_files.items():
        (tmp_dir / name).write_text(json.dumps(data, indent=2))
    for name, data in other_files.items():
        (tmp_dir / name).write_bytes(data)

    return tmp_dir
