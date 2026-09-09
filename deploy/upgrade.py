#!/usr/bin/env python3
"""Upgrade an existing deployment from a verified release, with offline state backup."""
import argparse
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


def upgrade(bundle):
    backup_root = Path('/root/guangyue-backups')
    backup_root.mkdir(mode=0o700, exist_ok=True)
    os.chmod(backup_root, 0o700)
    backup = Path(tempfile.mkdtemp(prefix='upgrade-' + time.strftime('%Y%m%d-%H%M%S') + '-', dir=backup_root))
    ready = False
    run('systemctl', 'stop', *UNITS)
    try:
        shutil.copytree(STATE, backup / 'state', symlinks=True)
        shutil.copytree(APP, backup / 'app', symlinks=True)
        shutil.copy2(CONFIG, backup / 'config.json')
        (backup / 'units').mkdir()
        for unit in UNITS:
            shutil.copy2('/etc/systemd/system/' + unit + '.service', backup / 'units')
        ready = True
        for src, name in [('guangyue-linux-amd64','guangyue'), ('hysteria-node-linux-amd64','hysteria'), ('xray-linux-amd64','xray'), ('mihomo-linux-amd64','mihomo')]:
            temp = APP / 'bin' / (name + '.new')
            shutil.copyfile(bundle / 'bin' / src, temp)
            temp.chmod(0o755)
            os.replace(temp, APP / 'bin' / name)
        for directory in ['web', 'licenses', 'deploy']:
            shutil.rmtree(APP / directory)
            shutil.copytree(bundle / directory, APP / directory)
        for path in APP.rglob('*'):
            if path.is_dir():
                path.chmod(0o755)
            elif 'bin' not in path.relative_to(APP).parts:
                path.chmod(0o644)
        shutil.copyfile(bundle / 'VERSION', APP / 'VERSION')
        for unit in UNITS:
            shutil.copyfile(bundle / 'deploy' / (unit + '.service'), '/etc/systemd/system/' + unit + '.service')
        run('runuser', '-u', 'guangyue', '--', str(APP / 'bin/guangyue'), '-prepare')
        run('runuser', '-u', 'guangyue', '--', str(APP / 'bin/xray'), 'run', '-test', '-c', str(STATE / 'xray.json'))
        run('systemctl', 'daemon-reload')
        run('systemctl', 'start', *UNITS)
        health()
    except Exception:
        subprocess.run(['systemctl', 'stop', *UNITS], capture_output=True)
        if ready:
            shutil.move(str(STATE), backup / 'failed-state')
            shutil.move(str(APP), backup / 'failed-app')
            shutil.copytree(backup / 'state', STATE, symlinks=True)
            shutil.copytree(backup / 'app', APP, symlinks=True)
            shutil.copy2(backup / 'config.json', CONFIG)
            # copytree preserves modes but not ownership; restore the service account.
            for path in [STATE, *STATE.rglob('*')]:
                if not path.is_symlink():
                    shutil.chown(path, user='guangyue', group='guangyue')
            shutil.chown(CONFIG, user='root', group='guangyue')
            for unit in UNITS:
                shutil.copy2(backup / 'units' / (unit + '.service'), '/etc/systemd/system/' + unit + '.service')
        subprocess.run(['systemctl', 'daemon-reload'], capture_output=True)
        subprocess.run(['systemctl', 'start', *UNITS], capture_output=True)
        print('Upgrade failed; rollback attempted. Inspect services. Backup: ' + str(backup), file=sys.stderr)
        raise
    print('Upgrade complete. Private offline backup: ' + str(backup))


def main():
    os.umask(0o077)
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--bundle', type=Path, required=True)
    p.add_argument('--apply', action='store_true')
    args = p.parse_args()
    if os.geteuid() != 0 or not CONFIG.is_file() or not STATE.is_dir():
        raise ValueError('root and an existing installation are required')
    verify_bundle(args.bundle)
    run('nginx', '-t')
    run('systemctl', 'is-active', '--quiet', *UNITS)
    # State and app backups + failed-state retention require headroom.
    needed = 3 * sum(p.stat().st_size for parent in [STATE, APP] for p in parent.rglob('*') if p.is_file())
    if shutil.disk_usage('/root').free < needed + (256 << 20):
        raise ValueError('insufficient backup space')
    if args.apply:
        upgrade(args.bundle.resolve())
    else:
        print('Upgrade preflight passed. Add --apply for a brief service interruption.')


if __name__ == '__main__':
    try:
        if '--apply' in sys.argv:
            with deployment_lock():
                main()
        else:
            main()
    except (ValueError, OSError, subprocess.CalledProcessError) as exc:
        raise SystemExit('Upgrade failed: ' + (str(exc) if isinstance(exc, ValueError) else type(exc).__name__))
