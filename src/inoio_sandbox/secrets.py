import os

import click

SECRET_MAP = {
    "LITELLM_API_KEY": "litellm.inoio.de",
    "GITHUB_TOKEN": "github.com",
}


def secret_flags() -> list[str]:
    flags = []
    for var, host in SECRET_MAP.items():
        value = os.environ.get(var)
        if value:
            flags.extend(["--secret", f"{var}@{host}"])
        else:
            click.echo(f"Warning: {var} not set; related provider/API may fail.", err=True)
    return flags
