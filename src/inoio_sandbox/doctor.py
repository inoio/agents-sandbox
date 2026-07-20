import os
import shutil

import click


def check_msb() -> bool:
    if shutil.which("msb") is None:
        click.echo(
            "msb not found. Install microsandbox: https://github.com/microsandbox/microsandbox",
            err=True,
        )
        return False
    return True


def check_docker() -> bool:
    if shutil.which("docker") is None:
        click.echo(
            "docker not found. Install Docker or Podman with docker-compatible CLI.",
            err=True,
        )
        return False
    return True


def check_kvm() -> bool:
    if not os.path.exists("/dev/kvm"):
        click.echo(
            "/dev/kvm not found. Load kvm module and ensure user is in the kvm group.",
            err=True,
        )
        return False
    return True


def check_git() -> bool:
    if shutil.which("git") is None:
        click.echo(
            "git not found. Install git via your system package manager.", err=True
        )
        return False
    return True


def check_all() -> bool:
    return all((check_msb(), check_docker(), check_kvm(), check_git()))
