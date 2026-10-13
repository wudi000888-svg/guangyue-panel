"""Root-owned, fixed-path write freeze for a not-yet-committed deployment."""
import grp
import os
import stat
from pathlib import Path

ROOT = Path('/var/lib/guangyue-maintenance')
PENDING = ROOT / 'pending'


def marker():
    try:
        info = PENDING.lstat()
    except FileNotFoundError:
        return None
    if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o027 or info.st_nlink != 1:
        raise ValueError('维护冻结标记不是本项目的受保护文件')
    return info


def sync_directory():
    fd = os.open(ROOT, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try: os.fsync(fd)
    finally: os.close(fd)


def begin():
    if os.geteuid() != 0: raise ValueError('维护冻结需要 root')
    if ROOT.exists() or ROOT.is_symlink():
        info = ROOT.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o027:
            raise ValueError('维护目录被重定向或可被其他身份写入')
    else:
        ROOT.mkdir(mode=0o750)
    os.chown(ROOT, 0, grp.getgrnam('guangyue').gr_gid); ROOT.chmod(0o750)
    if marker() is not None: raise ValueError('前一次版本操作尚未解除维护冻结，请先恢复')
    fd = os.open(PENDING, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o640)
    with os.fdopen(fd, 'wb') as output:
        os.fchown(output.fileno(), 0, grp.getgrnam('guangyue').gr_gid)
        output.write(b'Uncommitted deployment: external writes and workers are paused.\n')
        output.flush(); os.fsync(output.fileno())
    sync_directory()


def finish():
    if os.geteuid() != 0: raise ValueError('解除维护冻结需要 root')
    if marker() is not None:
        PENDING.unlink(); sync_directory()
