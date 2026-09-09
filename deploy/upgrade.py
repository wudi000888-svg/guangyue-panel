#!/usr/bin/env python3
"""Upgrade an existing deployment from a verified release, with offline state backup."""
import argparse
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


def upgrade(bundle, edition=None, site_id=None, infrastructure_file=None):
    old_config=json.loads(CONFIG.read_text())
    new_config=edition_config(old_config,edition or old_config.get('edition','lite'),site_id or old_config.get('site_id','default'),infrastructure_file)
    if old_config.get('edition')=='pro' and (new_config['site_id']!=old_config.get('site_id','default') or new_config['database']!=old_config.get('database')):
        raise ValueError('changing an existing Pro site ID or database requires a separate site migration')
    migrating=old_config.get('edition','lite')=='lite' and new_config['edition']=='pro'

    backup_root = Path('/root/guangyue-backups')
    backup_root.mkdir(mode=0o700, exist_ok=True)
    os.chmod(backup_root, 0o700)
    backup = Path(tempfile.mkdtemp(prefix='upgrade-' + time.strftime('%Y%m%d-%H%M%S') + '-', dir=backup_root))
    ready = False
    db_changed = False
    run('systemctl', 'stop', *UNITS)
    try:
        shutil.copytree(STATE, backup / 'state', symlinks=True)
        shutil.copytree(APP, backup / 'app', symlinks=True)
        shutil.copy2(CONFIG, backup / 'config.json')
        (backup / 'units').mkdir()
        for unit in UNITS:
            shutil.copy2('/etc/systemd/system/' + unit + '.service', backup / 'units')
        if old_config.get('edition')=='pro':
            dump_postgres(old_config,backup/'postgres.dump')
            run('runuser','-u','guangyue','--',str(APP/'bin/guangyue'),'-snapshot',str(STATE/'upgrade-snapshot.tar.gz'))
            shutil.move(str(STATE/'upgrade-snapshot.tar.gz'),backup/'database.tar.gz')
        ready = True
        for src, name in [('guangyue-linux-amd64','guangyue'), ('hysteria-node-linux-amd64','hysteria'), ('xray-linux-amd64','xray'), ('mihomo-linux-amd64','mihomo')]:
            temp = APP / 'bin' / (name + '.new')
            shutil.copyfile(bundle / 'bin' / src, temp)
            temp.chmod(0o755)
            os.replace(temp, APP / 'bin' / name)
        for directory in ['web', 'licenses', 'deploy']:
            if (APP / directory).exists(): shutil.rmtree(APP / directory)
            shutil.copytree(bundle / directory, APP / directory)
        for path in APP.rglob('*'):
            if path.is_dir():
                path.chmod(0o755)
            elif 'bin' not in path.relative_to(APP).parts:
                path.chmod(0o644)
        shutil.copyfile(bundle / 'VERSION', APP / 'VERSION')
        CONFIG.write_text(json.dumps(new_config,indent=2)+'\n')
        os.chmod(CONFIG,0o640)
        for unit in UNITS:
            dest=Path('/etc/systemd/system')/(unit+'.service')
            source=bundle/'deploy'/dest.name
            dest.write_text(service_text(source,new_config['edition']) if unit=='guangyue' else source.read_text())
            dest.chmod(0o644)
        if migrating:
            run('runuser','-u','guangyue','--',str(APP/'bin/guangyue'),'-import-sqlite',str(STATE/'panel.db'))
        db_changed = new_config['edition']=='pro'

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
            if old_config.get('edition')=='pro' and db_changed:
                restore_postgres(old_config,backup/'postgres.dump')
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
    p.add_argument('--edition',choices=['lite','pro'])
    p.add_argument('--site-id')
    p.add_argument('--infrastructure-file',type=Path)
    p.add_argument('--apply', action='store_true')
    args = p.parse_args()
    if os.geteuid() != 0 or not CONFIG.is_file() or not STATE.is_dir():
        raise ValueError('root and an existing installation are required')
    edition_config(json.loads(CONFIG.read_text()),args.edition or json.loads(CONFIG.read_text()).get('edition','lite'),args.site_id or json.loads(CONFIG.read_text()).get('site_id','default'),args.infrastructure_file)
    verify_bundle(args.bundle)
    run('nginx', '-t')
    run('systemctl', 'is-active', '--quiet', *UNITS)
    # State and app backups + failed-state retention require headroom.
    needed = 3 * sum(p.stat().st_size for parent in [STATE, APP] for p in parent.rglob('*') if p.is_file())
    if shutil.disk_usage('/root').free < needed + (256 << 20):
        raise ValueError('insufficient backup space')
    if args.apply:
        upgrade(args.bundle.resolve(),args.edition,args.site_id,args.infrastructure_file)
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
