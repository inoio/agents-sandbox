from unittest.mock import patch

from inoio_sandbox import worktree


def test_project_slug_from_git_common_dir(tmp_path):
    common_dir = tmp_path / "common.git"
    common_dir.mkdir()
    with patch("pathlib.Path.cwd", return_value=tmp_path):
        with patch(
            "subprocess.check_output", return_value=str(common_dir).encode() + b"\n"
        ):
            slug = worktree.project_slug()
            assert slug.startswith("p-")
            assert len(slug) == 10  # p- + 8 hex chars


def test_project_slug_fallback(tmp_path):
    non_git = tmp_path / "not-a-repo"
    non_git.mkdir()
    with patch("pathlib.Path.cwd", return_value=non_git):
        with patch("click.echo") as mock_echo:
            slug = worktree.project_slug()
            assert slug.startswith("p-")
            assert len(slug) == 10  # p- + 8 hex chars
            mock_echo.assert_called_once()
            message = mock_echo.call_args[0][0]
            assert "not inside a git repo" in message


def test_git_common_dir_absolute(tmp_path):
    common_dir = tmp_path / "common.git"
    common_dir.mkdir()
    with patch(
        "subprocess.check_output", return_value=str(common_dir).encode() + b"\n"
    ):
        result = worktree._git_common_dir(tmp_path)
        assert result == common_dir.resolve()


def test_git_common_dir_relative(tmp_path):
    with patch("subprocess.check_output", return_value=b".\n"):
        result = worktree._git_common_dir(tmp_path)
        assert result == tmp_path.resolve()


def test_branch_name(tmp_path):
    with patch("subprocess.check_output", return_value=b"feature-x\n"):
        assert worktree.branch_name(tmp_path) == "feature-x"


def test_branch_name_git_failure(tmp_path):
    from subprocess import CalledProcessError

    with patch("subprocess.check_output", side_effect=CalledProcessError(1, "git")):
        try:
            worktree.branch_name(tmp_path)
        except RuntimeError as exc:
            assert "Unable to determine current git branch" in str(exc)
        else:
            raise AssertionError("Expected RuntimeError")


def test_branch_name_git_not_found(tmp_path):
    with patch("subprocess.check_output", side_effect=FileNotFoundError("git")):
        try:
            worktree.branch_name(tmp_path)
        except RuntimeError as exc:
            assert "Unable to determine current git branch" in str(exc)
        else:
            raise AssertionError("Expected RuntimeError")


def test_current_worktree_path(tmp_path):
    top_level = tmp_path / "repo"
    top_level.mkdir()
    with patch(
        "subprocess.check_output", return_value=str(top_level).encode() + b"\n"
    ):
        result = worktree.current_worktree_path(tmp_path)
        assert result == top_level.resolve()


def test_current_worktree_path_git_failure(tmp_path):
    from subprocess import CalledProcessError

    with patch("subprocess.check_output", side_effect=CalledProcessError(1, "git")):
        assert worktree.current_worktree_path(tmp_path) is None


def test_current_worktree_path_git_not_found(tmp_path):
    with patch("subprocess.check_output", side_effect=FileNotFoundError("git")):
        assert worktree.current_worktree_path(tmp_path) is None


def test_worktree_path(tmp_path):
    state_dir = tmp_path / "state"
    slug = "p-deadbeef"
    branch = "feature-x"
    path = worktree.worktree_path(state_dir, slug, branch)
    assert path == state_dir / "worktrees" / "p-deadbeef" / "feature-x"


def test_ensure_worktree_creates_worktree(tmp_path):
    state_dir = tmp_path / "state"
    repo_root = tmp_path / "repo"
    repo_root.mkdir()
    target = worktree.worktree_path(state_dir, "p-deadbeef", "feature-x")
    with patch("subprocess.run") as mock_run:
        result = worktree.ensure_worktree(repo_root, state_dir, "p-deadbeef", "feature-x")
        assert result == target
        assert target.parent.exists()
        mock_run.assert_called_once_with(
            ["git", "worktree", "add", str(target), "feature-x"],
            cwd=repo_root,
            check=True,
        )


def test_ensure_worktree_idempotent(tmp_path):
    state_dir = tmp_path / "state"
    repo_root = tmp_path / "repo"
    repo_root.mkdir()
    target = worktree.worktree_path(state_dir, "p-deadbeef", "feature-x")
    target.parent.mkdir(parents=True, exist_ok=True)
    target.mkdir()
    with patch("subprocess.run") as mock_run:
        result = worktree.ensure_worktree(repo_root, state_dir, "p-deadbeef", "feature-x")
        assert result == target
        mock_run.assert_not_called()
