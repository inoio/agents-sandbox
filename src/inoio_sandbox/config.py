import json
import urllib.parse
from pathlib import Path


def load_provider_config(path: Path) -> dict:
    return json.loads(path.read_text())


def build_config_content(path: Path) -> str:
    fragment = load_provider_config(path)
    return f"OPENCODE_CONFIG_CONTENT={urllib.parse.quote(json.dumps(fragment))}"
