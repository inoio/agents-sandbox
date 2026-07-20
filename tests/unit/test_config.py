import json

from inoio_sandbox import config


def test_load_provider_config(tmp_path):
    data = {"provider": {"x": 1}}
    f = tmp_path / "provider-config.json"
    f.write_text(json.dumps(data))
    assert config.load_provider_config(f) == data


def test_build_config_content(tmp_path):
    provider = {"provider": {"litellm": {"apiKey": "{env:LITELLM_API_KEY}"}}}
    f = tmp_path / "provider-config.json"
    f.write_text(json.dumps(provider))
    content = config.build_config_content(f)
    expected = f"OPENCODE_CONFIG_CONTENT={json.dumps(provider)}"
    assert content == expected
    decoded = json.loads(content.split("=", 1)[1])
    assert decoded == provider
