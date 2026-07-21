import hashlib
import shutil
import subprocess
from pathlib import Path

from inoio_sandbox import log


def project_slug() -> str:
    """Return a stable short slug for the current repo."""
    cwd = Path.cwd()
    git_common_dir = _git_common_dir(cwd)
    if git_common_dir:
        key = str(git_common_dir.resolve())
    else:
        log.warn("not inside a git repo; using CWD hash as project slug.")
        key = str(cwd.resolve())
    h = hashlib.sha256(key.encode()).hexdigest()[:8]
    return f"p-{h}"


def branch_slug(branch: str) -> str:
    """Sanitize a branch name for use in VM names (slashes are invalid)."""
    return branch.replace("/", "-")


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


def current_worktree_path(cwd: Path | None = None) -> Path | None:
    cwd = cwd or Path.cwd()
    try:
        output = subprocess.check_output(
            ["git", "rev-parse", "--show-toplevel"],
            cwd=cwd,
            stderr=subprocess.DEVNULL,
        )
    except (subprocess.CalledProcessError, FileNotFoundError):
        return None
    return Path(output.decode().strip()).resolve()


def worktree_path(state_dir: Path, project_slug: str, branch: str) -> Path:
    return state_dir / "worktrees" / project_slug / branch


def _is_git_worktree(path: Path) -> bool:
    try:
        subprocess.check_output(
            ["git", "rev-parse", "--is-inside-work-tree"],
            cwd=path,
            stderr=subprocess.DEVNULL,
        )
    except (subprocess.CalledProcessError, FileNotFoundError):
        return False
    return True


def ensure_worktree(repo_root: Path, state_dir: Path, project_slug: str, branch: str) -> Path:
    target = worktree_path(state_dir, project_slug, branch)
    if target.exists():
        if _is_git_worktree(target):
            return target
        shutil.rmtree(target)
    target.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(
        ["git", "worktree", "add", str(target), branch],
        cwd=repo_root,
        check=True,
    )
    return target
