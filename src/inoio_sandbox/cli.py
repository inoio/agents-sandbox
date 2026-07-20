import os
from pathlib import Path

import click

from inoio_sandbox import config, doctor as doctor_checks, image, runner, secrets, volumes
from inoio_sandbox import worktree as worktree_mod


DATA_DIR = Path(__file__).parent / "data"
DEFAULT_DOCKERFILE = DATA_DIR / "Dockerfile"
DEFAULT_PROVIDER_CONFIG = DATA_DIR / "provider-config.json"
STATE_DIR = Path.home() / ".local/share/inoio-sandbox"


@click.group()
def cli():
    """inoio-sandbox launcher."""
    pass


@cli.command()
def doctor():
    """Check prerequisites."""
    ok = doctor_checks.check_all()
    if not ok:
        raise click.ClickException("preflight failed")
    click.echo("doctor: all checks passed")


@cli.command()
@click.option("--worktree", default=None, help="Worktree name")
@click.option("--image-rebuild", is_flag=True, help="Force image rebuild")
@click.option("--volume-fallback", is_flag=True, help="Use host directories instead of msb volumes")
def run(worktree, image_rebuild, volume_fallback):
    """Run opencode in a microsandbox VM."""
    if not doctor_checks.check_all():
        raise click.ClickException("preflight failed")

    cwd = Path.cwd()
    project = worktree_mod.project_slug()
    branch = worktree or worktree_mod.branch_name(cwd)
    wt = worktree_mod.ensure_worktree(cwd, STATE_DIR, project, branch)

    dockerfile = (
        Path(".sandbox/Dockerfile")
        if Path(".sandbox/Dockerfile").exists()
        else DEFAULT_DOCKERFILE
    )
    df_hash = image.dockerfile_hash(dockerfile)
    tag = image.image_tag(df_hash)
    image.build_and_load(dockerfile, tag, force=image_rebuild)

    local, cache = volumes.ensure_volumes(project, STATE_DIR, fallback=volume_fallback)
    config_content = config.build_config_content(DEFAULT_PROVIDER_CONFIG)
    secret_flags = secrets.secret_flags()

    env_extra = []
    env_file = Path(".sandbox/env")
    if env_file.exists():
        for line in env_file.read_text().splitlines():
            line = line.strip()
            if line and "=" in line and not line.startswith("#"):
                env_extra.append(line)

    cmd = runner.build_msb_run_command(
        image_tag=tag,
        worktree=wt,
        local=local,
        cache=cache,
        config_content=config_content,
        secret_flags=secret_flags,
        env_extra=env_extra,
    )
    os.execvp("msb", cmd)


def main():
    cli()
