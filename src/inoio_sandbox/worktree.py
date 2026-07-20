import hashlib
import subprocess
from pathlib import Path

import click


def project_slug() -> str:
    """Return a stable short slug for the current repo."""
    cwd = Path.cwd()
    git_common_dir = _git_common_dir(cwd)
    if git_common_dir:
        key = str(git_common_dir.resolve())
    else:
        click.echo(
            "Warning: not inside a git repo; using CWD hash as project slug.",
            err=True,
        )
        key = str(cwd.resolve())
    h = hashlib.sha256(key.encode()).hexdigest()[:8]
    return f"p-{h}"


def _git_common_dir(cwd: Path) -> Path | None:
    try:
        output = subprocess.check_output(
            ["git", "rev-parse", "--git-common-dir"],
            cwd=cwd,
            stderr=subprocess.DEVNULL,
        )
        path = Path(output.decode().strip())
        if not path.is_absolute():
            path = cwd / path
        return path.resolve()
    except (subprocess.CalledProcessError, FileNotFoundError):
        return None


def branch_name(cwd: Path | None = None) -> str:
    cwd = cwd or Path.cwd()
    try:
        output = subprocess.check_output(
            ["git", "rev-parse", "--abbrev-ref", "HEAD"],
            cwd=cwd,
            stderr=subprocess.DEVNULL,
        )
    except (subprocess.CalledProcessError, FileNotFoundError) as exc:
        raise RuntimeError(f"Unable to determine current git branch from {cwd}") from exc
    return output.decode().strip()


def worktree_path(state_dir: Path, project_slug: str, branch: str) -> Path:
    return state_dir / "worktrees" / project_slug / branch


def ensure_worktree(repo_root: Path, state_dir: Path, project_slug: str, branch: str) -> Path:
    target = worktree_path(state_dir, project_slug, branch)
    if target.exists():
        return target
    target.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(
        ["git", "worktree", "add", str(target), branch],
        cwd=repo_root,
        check=True,
    )
    return target
