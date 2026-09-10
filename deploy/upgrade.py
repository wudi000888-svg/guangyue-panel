#!/usr/bin/env python3
"""Verified release upgrades and compatible code rollback with offline recovery."""
import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path
sys.dont_write_bytecode = True
from install import APP, STATE, CONFIG, UNITS, run, health, verify_bundle
from common import deployment_lock
from infrastructure import edition_config, service_text, dump_postgres, restore_postgres
import update_state as updates


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
    new_config = edition_config(old_config, edition or old_config.get('edition', 'lite'), site_id or old_config.get('site_id', 'default'), infrastructure_file)
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


def main():
    os.umask(0o077)
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--bundle', type=Path, required=True)
    p.add_argument('--edition', choices=['lite', 'pro']); p.add_argument('--site-id'); p.add_argument('--infrastructure-file', type=Path)
    p.add_argument('--apply', action='store_true'); args = p.parse_args()
    preflight(args.bundle)
    config = json.loads(CONFIG.read_text())
    edition_config(config, args.edition or config.get('edition', 'lite'), args.site_id or config.get('site_id', 'default'), args.infrastructure_file)
    # A read-only preflight does not establish or modify the installation baseline.
    floor = updates.read('installation.json', {'version': updates.current()})['version']
    if updates.version((args.bundle / 'VERSION').read_text().strip()) < updates.version(floor): raise ValueError('不能回退到安装基线之前的版本')
    if args.apply: upgrade(args.bundle, args.edition, args.site_id, args.infrastructure_file)
    else: print('Upgrade preflight passed. Add --apply for a brief service interruption.')


if __name__ == '__main__':
    try:
        if '--apply' in sys.argv:
            with deployment_lock(): main()
        else: main()
    except (ValueError, OSError, subprocess.CalledProcessError) as exc:
        raise SystemExit('Upgrade failed: ' + (str(exc) if isinstance(exc, ValueError) else type(exc).__name__))
