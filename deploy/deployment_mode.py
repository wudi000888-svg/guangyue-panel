"""Root-owned deployment identity shared by maintenance adapters."""
import json
import os
import stat
from pathlib import Path

MARKER = Path('/etc/guangyue-deployment.json')


def specification():
    try:
        descriptor = os.open(MARKER, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    except FileNotFoundError:
        return None
    try:
        info = os.fstat(descriptor)
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o077 or info.st_size > 65536:
            raise ValueError('部署身份文件权限或类型无效')
        with os.fdopen(descriptor, closefd=False) as source:
            value = json.load(source)
    finally:
        os.close(descriptor)
    if not isinstance(value, dict) or value.get('backend') not in {'native', 'docker'} or value.get('private_gateway') is not True:
        raise ValueError('部署身份文件无效')
    return value


def private_gateway():
    return specification() is not None


def docker():
    value = specification()
    return value is not None and value['backend'] == 'docker'
