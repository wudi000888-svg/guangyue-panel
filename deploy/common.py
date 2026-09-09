"""Serialize administrative mutations without trusting a pre-existing lock symlink."""
import contextlib
import fcntl
import os
from pathlib import Path


@contextlib.contextmanager
def deployment_lock(path=Path('/run/lock/guangyue-deploy.lock')):
    fd = os.open(path, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    try:
        if os.fstat(fd).st_uid != os.geteuid():
            raise ValueError('deployment lock has an unexpected owner')
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as exc:
            raise ValueError('another install, upgrade or certificate deployment is running') from exc
        yield
    finally:
        os.close(fd)
