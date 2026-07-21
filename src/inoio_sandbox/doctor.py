import os
import shutil
import sys

from inoio_sandbox import log


def check_msb() -> bool:
    if shutil.which("msb") is None:
        log.error("msb not found. Install microsandbox: https://github.com/microsandbox/microsandbox")
        return False
    return True


def check_docker() -> bool:
    if shutil.which("docker") is None:
        log.error("docker not found. Install Docker or Podman with docker-compatible CLI.")
        return False
    return True


def check_kvm() -> bool:
    if sys.platform != "linux":
        return True
    if not os.path.exists("/dev/kvm"):
        log.error("/dev/kvm not found. Load kvm module and ensure user is in the kvm group.")
        return False
    return True


def check_git() -> bool:
    if shutil.which("git") is None:
        log.error("git not found. Install git via your system package manager.")
        return False
    return True


def check_all() -> bool:
    return all((check_msb(), check_docker(), check_kvm(), check_git()))
