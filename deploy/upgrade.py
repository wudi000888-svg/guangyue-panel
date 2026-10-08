#!/usr/bin/env python3
"""Verified release upgrades and compatible code rollback with offline recovery."""
import argparse
import contextlib
import hashlib
import json
import os
import shutil
import stat
import subprocess
import sys
import tempfile
import time
from pathlib import Path
sys.dont_write_bytecode = True
from install import APP, STATE, CONFIG, UNITS, run, health, verify_bundle
from common import deployment_lock
from infrastructure import edition_config, service_text, dump_postgres, restore_postgres
from render import wallet_restore_location
import update_state as updates

NGINX_PANEL = Path('/etc/nginx/sites-enabled/guangyue-panel.conf')


def write_panel_proxy(content):
    """Atomically replace the root-owned managed fragment without a partial read."""
    fd, temp = tempfile.mkstemp(prefix='.guangyue-panel-', dir=NGINX_PANEL.parent)
    try:
        with os.fdopen(fd, 'w') as output:
            os.fchmod(output.fileno(), 0o644)
            output.write(content); output.flush(); os.fsync(output.fileno())
        os.replace(temp, NGINX_PANEL)
    finally:
        if os.path.exists(temp): os.unlink(temp)


def upgrade_panel_proxy():
    before = NGINX_PANEL.read_text()
    after = wallet_restore_location(before)
    if after == before: return
    write_panel_proxy(after)
    try:
        run('nginx', '-t'); run('systemctl', 'reload', 'nginx')
    except Exception:
        write_panel_proxy(before)
        run('nginx', '-t'); run('systemctl', 'reload', 'nginx')
        raise


def as_panel(*args):
    # Let PID 1 establish the service identity. Some hardened hosts prevent a
    # running updater from calling setresuid, even though it can manage units.
    # The application itself never receives root or the updater's privileges.
    command = ['systemd-run', '--quiet', '--wait', '--pipe', '--collect', '--service-type=exec',
               '--property=User=guangyue', '--property=Group=guangyue',
               '--property=WorkingDirectory=' + str(STATE), '--property=UMask=0077',
               '--property=NoNewPrivileges=true', '--property=PrivateTmp=true',
               '--property=ProtectSystem=strict', '--property=ProtectHome=true',
               '--property=ReadWritePaths=' + str(STATE), '--property=CapabilityBoundingSet=',
               '--property=MemoryMax=384M', '--property=TasksMax=128', '--property=RuntimeMaxSec=300']
    if os.environ.get('LISTEN_PID') == str(os.getpid()):
        command.append('--property=BindsTo=guangyue-updater.service')
    return run(*command, '--', *args)


def app_hashes(directory):
    result = {}
    for path in sorted(Path(directory).rglob('*')):
        if path.is_symlink(): raise ValueError('程序备份包含不安全的链接')
        if path.is_file():
            digest = hashlib.sha256()
            with path.open('rb') as source:
                for block in iter(lambda: source.read(1 << 20), b''): digest.update(block)
            result[path.relative_to(directory).as_posix()] = digest.hexdigest()
    return result


def verify_saved(backup):
    expected = json.loads((Path(backup) / 'app-checksums.json').read_text())
    if not expected or app_hashes(Path(backup) / 'app') != expected:
        raise ValueError('回退程序备份校验失败')


def backup_current(config):
    backup_root = Path('/root/guangyue-backups')
    backup_root.mkdir(mode=0o700, exist_ok=True); backup_root.chmod(0o700)
    backup = Path(tempfile.mkdtemp(prefix='upgrade-' + time.strftime('%Y%m%d-%H%M%S') + '-', dir=backup_root))
    shutil.copytree(STATE, backup / 'state', symlinks=True)
    shutil.copytree(APP, backup / 'app', symlinks=True)
    (backup / 'app-checksums.json').write_text(json.dumps(app_hashes(backup / 'app')))
    shutil.copy2(CONFIG, backup / 'config.json')
    if NGINX_PANEL.is_file(): shutil.copy2(NGINX_PANEL, backup / 'nginx-panel.conf')
    (backup / 'units').mkdir()
    for unit in UNITS:
        shutil.copy2('/etc/systemd/system/' + unit + '.service', backup / 'units')
    if config.get('database', {}).get('driver') == 'postgres':
        dump_postgres(config, backup / 'postgres.dump')
        as_panel(str(APP / 'bin/guangyue'), '-snapshot', str(STATE / 'upgrade-snapshot.tar.gz'))
        shutil.move(str(STATE / 'upgrade-snapshot.tar.gz'), backup / 'database.tar.gz')
    return backup


def restore_current(backup):
    """Emergency rollback only: restore the immediately preceding stopped snapshot."""
    backup = Path(backup)
    verify_saved(backup)
    config = json.loads((backup / 'config.json').read_text())
    run('systemctl', 'stop', *UNITS)
    for source, name in [(STATE, 'state'), (APP, 'app')]:
        if source.exists():
            failed = backup / ('failed-' + name)
            if failed.exists(): shutil.rmtree(failed)
            shutil.move(str(source), failed)
        shutil.copytree(backup / name, source, symlinks=True)
    shutil.copy2(backup / 'config.json', CONFIG)
    for path in [STATE, *STATE.rglob('*')]:
        if not path.is_symlink(): shutil.chown(path, user='guangyue', group='guangyue')
    shutil.chown(CONFIG, user='root', group='guangyue')
    if (backup / 'postgres.dump').is_file(): restore_postgres(config, backup / 'postgres.dump')
    restore_units(backup)
    if (backup / 'nginx-panel.conf').is_file():
        write_panel_proxy((backup / 'nginx-panel.conf').read_text())
        run('nginx', '-t'); run('systemctl', 'reload', 'nginx')
    run('systemctl', 'daemon-reload'); run('systemctl', 'start', *UNITS)
    health((backup / 'app/VERSION').read_text().strip())


def restore_units(backup):
    for unit in UNITS:
        shutil.copy2(Path(backup) / 'units' / (unit + '.service'), '/etc/systemd/system/' + unit + '.service')


def prepare_start(expected):
    as_panel(str(APP / 'bin/guangyue'), '-prepare')
    as_panel(str(APP / 'bin/xray'), 'run', '-test', '-c', str(STATE / 'xray.json'))
    run('systemctl', 'daemon-reload'); run('systemctl', 'start', *UNITS)
    health(expected)


def transaction(change, target, new_config, progress=lambda stage: None, provision=None):
    old_config = json.loads(CONFIG.read_text())
    updates.baseline(updates.current()); updates.require_floor(target)
    signature = updates.schema(old_config)
    backup = None
    updates.write('transaction.json', {'phase': 'preparing', 'target': target, 'previous': updates.current()})
    try:
        run('systemctl', 'stop', *UNITS)
        progress('backing_up')
        backup = backup_current(old_config)
        updates.write('transaction.json', {'backup': str(backup), 'target': target, 'phase': 'changing', 'schema': signature})
        progress('installing'); change(backup)
        progress('restarting'); prepare_start(target)
        updates.write('transaction.json', {'backup': str(backup), 'target': target, 'phase': 'healthy', 'schema': signature})
        updates.remember(backup, old_config, signature)
        if provision: provision()
        updates.write('transaction.json', None)
    except Exception:
        progress('recovering')
        if backup:
            try:
                restore_current(backup)
                updates.write('transaction.json', None)
            except Exception:
                # Keep the recovery journal for the independent helper after a restart.
                raise ValueError('自动恢复未完成，请通过 SSH 检查更新服务与离线备份') from None
        else:
            subprocess.run(['systemctl', 'start', *UNITS], capture_output=True)
            updates.write('transaction.json', None)
        raise
    return backup


def upgrade(bundle, edition=None, site_id=None, infrastructure_file=None, progress=lambda stage: None):
    bundle = Path(bundle).resolve()
    old_config = json.loads(CONFIG.read_text())
    new_config = edition_config(old_config, edition or old_config.get('edition', 'lite'), site_id or old_config.get('site_id', 'default'), infrastructure_file, existing=True)
    if old_config.get('edition') == 'pro' and (new_config['site_id'] != old_config.get('site_id', 'default') or new_config['database'] != old_config.get('database')):
        raise ValueError('changing an existing Pro site ID or database requires a separate site migration')
    migrating = old_config.get('edition', 'lite') == 'lite' and new_config['edition'] == 'pro'
    target = (bundle / 'VERSION').read_text().strip()
    updates.require_floor(target)
    def change(backup):
        for src, name in [('guangyue-linux-amd64', 'guangyue'), ('hysteria-node-linux-amd64', 'hysteria'), ('xray-linux-amd64', 'xray'), ('mihomo-linux-amd64', 'mihomo')]:
            temp = APP / 'bin' / (name + '.new'); shutil.copyfile(bundle / 'bin' / src, temp); temp.chmod(0o755); os.replace(temp, APP / 'bin' / name)
        for directory in ['web', 'licenses', 'deploy']:
            if (APP / directory).exists(): shutil.rmtree(APP / directory)
            shutil.copytree(bundle / directory, APP / directory)
        for path in APP.rglob('*'):
            if path.is_dir(): path.chmod(0o755)
            elif 'bin' not in path.relative_to(APP).parts: path.chmod(0o644)
        shutil.copyfile(bundle / 'VERSION', APP / 'VERSION')
        CONFIG.write_text(json.dumps(new_config, indent=2) + '\n'); CONFIG.chmod(0o640)
        for unit in UNITS:
            dest = Path('/etc/systemd/system') / (unit + '.service'); source = bundle / 'deploy' / dest.name
            dest.write_text(service_text(source, new_config['edition'], new_config.get('role')) if unit == 'guangyue' else source.read_text()); dest.chmod(0o644)
        if migrating:
            as_panel(str(APP / 'bin/guangyue'), '-import-sqlite', str(STATE / 'panel.db'))
        upgrade_panel_proxy()
    backup = transaction(change, target, new_config, progress, lambda: updates.setup(target, bundle / 'deploy'))
    print('Upgrade complete. Private offline backup: ' + str(backup))
    return backup


def rollback(target, progress=lambda stage: None):
    updates.require_floor(target)
    chosen = next((x for x in updates.candidates() if x['version'] == target), None)
    if not chosen: raise ValueError('此版本没有可用的本机回退记录')
    if not chosen['compatible']: raise ValueError('目标版本不兼容当前数据库，已阻止回退')
    source = Path(chosen['backup'])
    verify_saved(source)
    if (source / 'app/VERSION').read_text().strip() != target: raise ValueError('回退备份版本不匹配')
    def change(backup):
        shutil.rmtree(APP); shutil.copytree(source / 'app', APP)
        restore_units(source)
    # Keep today's database, certificate state, credentials, counters and configuration.
    return transaction(change, target, json.loads(CONFIG.read_text()), progress)


def preflight(bundle=None):
    if os.geteuid() != 0 or not CONFIG.is_file() or not STATE.is_dir():
        raise ValueError('root and an existing installation are required')
    if bundle: verify_bundle(bundle)
    run('nginx', '-t'); run('systemctl', 'is-active', '--quiet', *UNITS)
    needed = 3 * sum(p.stat().st_size for parent in [STATE, APP] for p in parent.rglob('*') if p.is_file())
    if shutil.disk_usage('/root').free < needed + (256 << 20): raise ValueError('insufficient backup space')


def _helper_directory(path, private=False):
    """Reject redirects and writable parents before touching privileged code."""
    path = Path(path).absolute()
    for parent in reversed((path, *path.parents)):
        info = parent.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid not in {0, os.geteuid()}:
            raise ValueError('更新恢复目录存在不安全的链接或所有者')
        # A protected child of a sticky temporary directory is safe for tests;
        # the helper and recovery directories themselves must never be writable.
        if info.st_mode & 0o022 and (parent == path or not info.st_mode & stat.S_ISVTX):
            raise ValueError('更新恢复目录可被其他用户写入: ' + (parent.name or '/') + ' mode=' + format(stat.S_IMODE(info.st_mode), '04o'))
    if private and path.stat().st_mode & 0o077:
        raise ValueError('更新恢复状态目录必须为私有目录')


def _helper_file(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_uid not in {0, os.geteuid()} or info.st_mode & 0o022:
            raise ValueError('更新恢复文件权限无效')
        with os.fdopen(fd, 'rb', closefd=False) as source:
            return source.read()
    finally:
        os.close(fd)


def _helper_sync_directory(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try: os.fsync(fd)
    finally: os.close(fd)


def _helper_atomic_write(path, content, mode):
    fd, temporary = tempfile.mkstemp(prefix='.' + path.name + '-', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as output:
            os.fchmod(output.fileno(), mode)
            output.write(content); output.flush(); os.fsync(output.fileno())
        os.replace(temporary, path)
        _helper_sync_directory(path.parent)
    finally:
        if os.path.exists(temporary): os.unlink(temporary)


def _require_idle_helper():
    # Unknown stages fail closed too: a newer helper may introduce new actions.
    for name in ['transaction.json', 'operation.json', 'network-operation.json']:
        path = updates.ROOT / name
        try: value = json.loads(_helper_file(path))
        except FileNotFoundError: continue
        if value is None: continue
        if name == 'transaction.json' or not isinstance(value, dict) or value.get('stage') not in {'succeeded', 'failed'}:
            raise ValueError('已有更新、恢复或网络操作，请等待其完成后重试')


def prime_helper_recovery(bundle):
    """Prime only infrastructure.py from a verified bundle under deployment_lock.

    CLI callers must hold the deployment lock throughout this call and upgrade.
    Never call from the helper itself: only an idle helper may be stopped here.
    Keep this module after a failed upgrade so restart recovery uses the fix.
    """
    bundle = Path(bundle).resolve()
    verify_bundle(bundle)
    helper = updates.HELPER
    if not helper.exists() and not helper.is_symlink(): return None
    _helper_directory(helper)
    source = bundle / 'deploy/infrastructure.py'
    # Verification above covers every bundled deploy file; retain exactly those
    # bytes and recheck their manifest entry before installing privileged code.
    _helper_directory(source.parent)
    content = _helper_file(source)
    expected = hashlib.sha256(content).hexdigest() + '  deploy/infrastructure.py'
    if expected not in (bundle / 'SHA256SUMS').read_text().splitlines():
        raise ValueError('恢复工具未通过发布包校验')
    target = helper / 'infrastructure.py'
    previous = _helper_file(target)
    helper_version = helper / 'VERSION'
    if helper_version.exists() or helper_version.is_symlink():
        if updates.version(_helper_file(helper_version).decode().strip()) > updates.version((bundle / 'VERSION').read_text().strip()):
            raise ValueError('恢复工具版本高于安装包，请使用最新已验证发布包')
    _helper_directory(updates.ROOT.parent)
    updates.ROOT.mkdir(mode=0o700, exist_ok=True)
    _helper_directory(updates.ROOT, private=True)
    _helper_sync_directory(updates.ROOT.parent)
    _require_idle_helper()
    try:
        # Holding the deployment lock prevents executing mutations. A request
        # queued just before shutdown remains recorded; reject this CLI run.
        run('systemctl', 'stop', 'guangyue-updater.socket', 'guangyue-updater.service')
        _require_idle_helper()
        if content == previous: return None
        recovery = updates.ROOT / 'recovery'
        recovery.mkdir(mode=0o700, exist_ok=True)
        _helper_directory(recovery, private=True)
        backup = Path(tempfile.mkdtemp(prefix='helper-infrastructure-', dir=recovery))
        _helper_atomic_write(backup / 'infrastructure.py', previous, 0o600)
        _helper_atomic_write(backup / 'manifest.json', json.dumps({
            'bundle_version': (bundle / 'VERSION').read_text().strip(),
            'previous_sha256': hashlib.sha256(previous).hexdigest(),
            'installed_sha256': hashlib.sha256(content).hexdigest(),
            'created_at': int(time.time()),
        }, indent=2).encode() + b'\n', 0o600)
        _helper_sync_directory(recovery); _helper_sync_directory(updates.ROOT)
        _helper_atomic_write(target, content, 0o644)
        return backup
    finally:
        run('systemctl', 'start', 'guangyue-updater.socket')


def main():
    os.umask(0o077)
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--bundle', type=Path, required=True)
    p.add_argument('--edition', choices=['lite', 'pro']); p.add_argument('--site-id'); p.add_argument('--infrastructure-file', type=Path)
    p.add_argument('--apply', action='store_true'); args = p.parse_args()
    with deployment_lock() if args.apply else contextlib.nullcontext():
        preflight(args.bundle)
        config = json.loads(CONFIG.read_text())
        edition_config(config, args.edition or config.get('edition', 'lite'), args.site_id or config.get('site_id', 'default'), args.infrastructure_file, existing=True)
        # A read-only preflight does not establish or modify the installation baseline.
        floor = updates.read('installation.json', {'version': updates.current()})['version']
        if updates.version((args.bundle / 'VERSION').read_text().strip()) < updates.version(floor): raise ValueError('不能回退到安装基线之前的版本')
        if args.apply:
            prime_helper_recovery(args.bundle)
            upgrade(args.bundle, args.edition, args.site_id, args.infrastructure_file)
        else: print('Upgrade preflight passed. Add --apply for a brief service interruption.')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, subprocess.CalledProcessError) as exc:
        raise SystemExit('Upgrade failed: ' + (str(exc) if isinstance(exc, ValueError) else type(exc).__name__))
