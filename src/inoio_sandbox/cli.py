import click


@click.group()
def cli():
    """inoio-sandbox launcher."""
    pass


@cli.command()
def doctor():
    """Check prerequisites."""
    click.echo("doctor: not implemented yet")


@cli.command()
@click.option("--worktree", default=None, help="Worktree name")
@click.option("--image-rebuild", is_flag=True, help="Force image rebuild")
def run(worktree, image_rebuild):
    """Run opencode in a microsandbox VM."""
    click.echo("run: not implemented yet")


def main():
    cli()
