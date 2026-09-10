"""Root-owned installation floor and compatible local release history."""
import hashlib
import json
import os
import re
import shutil
import sqlite3
import subprocess
import time
from pathlib import Path

ROOT = Path('/var/lib/guangyue-updater')
HELPER = Path('/opt/guangyue-updater')
APP = Path('/opt/guangyue-personal')
CONFIG = Path('/etc/guangyue-personal.json')
SOCKET = '/run/guangyue-update.sock'
REPO = 'wudi000888-svg/guangyue-panel'


def version(value):
    if not isinstance(value, str) or not re.fullmatch(r'(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})', value):
        raise ValueError('版本号无效')
    return tuple(int(n) for n in value.split('.'))


def read(name, default=None):
    p = ROOT / name
    return json.loads(p.read_text()) if p.exists() else default


def write(name, value):
    ROOT.mkdir(mode=0o700, parents=True, exist_ok=True)
    if ROOT.is_symlink() or ROOT.stat().st_uid != os.geteuid():
        raise ValueError('更新状态目录权限无效')
    ROOT.chmod(0o700)
    p = ROOT / (name + '.new')
    fd = os.open(p, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as f:
        json.dump(value, f, ensure_ascii=False); f.flush(); os.fsync(f.fileno())
    os.replace(p, ROOT / name)


def current():
    v = (APP / 'VERSION').read_text().strip(); version(v); return v


def baseline(initial=None):
    value = read('installation.json')
    if value is None:
        v = initial or current(); version(v)
        value = {'version': v, 'recorded_at': int(time.time())}
        write('installation.json', value)
    version(value['version'])
    return value


def require_floor(target):
    if version(target) < version(baseline()['version']):
        raise ValueError('不能回退到安装基线之前的版本')


def schema(config):
    if config.get('database', {}).get('driver') == 'postgres':
        from infrastructure import postgres_env
        site = config.get('site_id', 'default')
        if not re.fullmatch(r'[a-z][a-z0-9_]{0,39}', site):
            raise ValueError('站点标识无效')
        sql = 'SELECT version,checksum FROM gy_' + site + '.schema_migrations ORDER BY version'
        output = subprocess.check_output(['psql', '-XAt', '-c', sql], env=postgres_env(config), stderr=subprocess.DEVNULL).decode()
        return hashlib.sha256(output.encode()).hexdigest()
    db = sqlite3.connect('file:' + str(Path(config['state_dir']) / 'panel.db') + '?mode=ro', uri=True)
    try:
        values = list(db.execute('SELECT version,checksum FROM schema_migrations ORDER BY version'))
        return hashlib.sha256(json.dumps(values).encode()).hexdigest()
    finally:
        db.close()


def identity(config):
    return {k: config.get(k, default) for k, default in [('edition', 'lite'), ('role', 'controller' if config.get('edition') == 'pro' else 'standalone'), ('site_id', 'default')]}


def remember(backup, config, signature):
    old = (Path(backup) / 'app/VERSION').read_text().strip(); version(old)
    values = read('history.json', [])
    record = dict(version=old, backup=str(backup), created=int(time.time()), schema=signature, **identity(config))
    values = [v for v in values if v['backup'] != str(backup)]
    # Backup directories are retained; the UI lists up to 30 recent compatible versions.
    write('history.json', (values + [record])[-100:])


def candidates():
    values = read('history.json', [])
    if not values: return []
    config = json.loads(CONFIG.read_text()); ident = identity(config)
    signature = schema(config); floor = version(baseline()['version']); now = version(current())
    result = {}
    for row in reversed(values):
        v = row['version']
        if v in result or not floor <= version(v) < now or any(row.get(k) != x for k, x in ident.items()):
            continue
        p = Path(row['backup'])
        if not p.is_relative_to('/root/guangyue-backups') or not (p / 'app/VERSION').is_file():
            continue
        result[v] = dict(row, compatible=row.get('schema') == signature)
    return sorted(result.values(), key=lambda x: version(x['version']), reverse=True)[:30]


def setup(initial, source):
    """Provision the helper separately so rolling back the panel cannot erase its floor."""
    baseline(initial)
    source = Path(source).resolve()
    HELPER.mkdir(mode=0o755, parents=True, exist_ok=True)
    helper_version = HELPER / 'VERSION'
    wanted = (APP / 'VERSION').read_text().strip()
    if not helper_version.exists() or version(wanted) >= version(helper_version.read_text().strip()):
        for path in source.glob('*.py'):
            if path.resolve() == (HELPER / path.name).resolve():
                continue
            temp = HELPER / (path.name + '.new'); shutil.copyfile(path, temp); temp.chmod(0o644); os.replace(temp, HELPER / path.name)
        helper_version.write_text(wanted + '\n'); helper_version.chmod(0o644)
    for name in ['guangyue-updater.socket', 'guangyue-updater.service']:
        dest = Path('/etc/systemd/system') / name
        if (source / name).exists():
            shutil.copyfile(source / name, dest); dest.chmod(0o644)
    subprocess.run(['systemctl', 'daemon-reload'], check=True, capture_output=True)
    subprocess.run(['systemctl', 'enable', '--now', 'guangyue-updater.socket'], check=True, capture_output=True)
    subprocess.run(['systemctl', 'enable', 'guangyue-updater.service'], check=True, capture_output=True)
