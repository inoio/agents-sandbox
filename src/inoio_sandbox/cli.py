import click

from inoio_sandbox import doctor as doctor_checks


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
def run(worktree, image_rebuild):
    """Run opencode in a microsandbox VM."""
    click.echo("run: not implemented yet")


def main():
    cli()
