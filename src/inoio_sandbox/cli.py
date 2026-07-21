import os
import time
from pathlib import Path

import click

from inoio_sandbox import config, image, runner, secrets, volumes
from inoio_sandbox import doctor as doctor_checks
from inoio_sandbox import worktree as worktree_mod

DATA_DIR = Path(__file__).parent / "data"
DEFAULT_DOCKERFILE = DATA_DIR / "Dockerfile"
DEFAULT_PROVIDER_CONFIG = DATA_DIR / "provider-config.json"
STATE_DIR = Path.home() / ".local/share/inoio-sandbox"


def _timing(enabled: bool):
    start = time.perf_counter()
    phases = []

    def tick(label: str):
        nonlocal start
        now = time.perf_counter()
        elapsed = now - start
        start = now
        phases.append((label, elapsed))
        if enabled:
            click.echo(f"[timing] {label}: {elapsed:.3f}s", err=True)

    def summary():
        if enabled:
            total = sum(e for _, e in phases)
            click.echo(f"[timing] total launcher overhead: {total:.3f}s", err=True)

    return tick, summary


@click.group()
def cli():
    """inoio-sandbox launcher."""
    pass


@cli.command()
def doctor():
    """Check prerequisites."""
    if not doctor_checks.check_all():
        raise click.ClickException("preflight failed")
    click.echo("doctor: all checks passed")


@cli.command()
@click.option("--worktree", default=None, help="Worktree name")
@click.option("--image-rebuild", is_flag=True, help="Force image rebuild")
@click.option("--volume-fallback", is_flag=True, help="Use host directories instead of msb volumes")
@click.option("--cpus", default=None, type=int, help="Number of CPUs (default: all)")
@click.option("--memory", default="4G", help="Memory limit (default: 4G)")
@click.option("--timing", is_flag=True, help="Print per-phase launcher timing to stderr")
def run(worktree, image_rebuild, volume_fallback, cpus, memory, timing):
    """Run opencode in a microsandbox VM."""
    tick, summary = _timing(timing)

    if not doctor_checks.check_all():
        raise click.ClickException("preflight failed")
    tick("preflight")

    cwd = Path.cwd()
    try:
        project = worktree_mod.project_slug()
        branch = worktree or worktree_mod.branch_name(cwd)
    except RuntimeError as exc:
        raise click.ClickException("Unable to determine git branch. Run from inside a git repository.") from exc
    tick("project/branch resolution")

    current_wt = worktree_mod.current_worktree_path(cwd)
    if current_wt:
        wt = current_wt
    else:
        wt = worktree_mod.ensure_worktree(cwd, STATE_DIR, project, branch)
    tick("worktree resolution")

    dockerfile = Path(".sandbox/Dockerfile") if Path(".sandbox/Dockerfile").exists() else DEFAULT_DOCKERFILE
    if image.references_base(dockerfile):
        image.ensure_base_image(DEFAULT_DOCKERFILE, force=image_rebuild)
    df_hash = image.dockerfile_hash(dockerfile)
    tag = image.image_tag(df_hash)
    image.build_and_load(dockerfile, tag, force=image_rebuild)
    tick("image hash/check/build")

    local, cache = volumes.ensure_volumes(project, STATE_DIR, fallback=volume_fallback)
    tick("volume ensure")

    config_content = config.build_config_content(DEFAULT_PROVIDER_CONFIG)
    secret_flags = secrets.secret_flags()
    cpus = cpus or runner.available_cpus()
    name = f"inoio-sandbox-{project}-{worktree_mod.branch_slug(branch)}"[:128]
    tick("config/secrets")

    env_extra = []
    env_file = Path(".sandbox/env")
    if env_file.exists():
        for line in env_file.read_text().splitlines():
            line = line.strip()
            if line and "=" in line and not line.startswith("#"):
                env_extra.append(line)

    cmd = runner.build_msb_run_command(
        image_tag=tag,
        name=name,
        worktree=wt,
        local=local,
        cache=cache,
        config_content=config_content,
        secret_flags=secret_flags,
        env_extra=env_extra,
        cpus=cpus,
        memory=memory,
    )
    tick("command assembly")
    summary()
    os.execvp("msb", cmd)


def main():
    cli()
