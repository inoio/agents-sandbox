from typing import Any

import click


def warn(*args: Any, **kwargs: Any) -> None:  # noqa: ANN401
    kwargs.setdefault("fg", "yellow")
    kwargs.setdefault("err", True)
    click.secho(*args, **kwargs)


def error(*args: Any, **kwargs: Any) -> None:  # noqa: ANN401
    kwargs.setdefault("fg", "red")
    kwargs.setdefault("err", True)
    click.secho(*args, **kwargs)


def info(*args: Any, **kwargs: Any) -> None:  # noqa: ANN401
    click.secho(*args, **kwargs)
