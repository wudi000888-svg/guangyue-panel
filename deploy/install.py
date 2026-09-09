#!/usr/bin/env python3
"""Fresh Debian/Ubuntu installation. Preflight is read-only unless --apply is given."""
import argparse
import hashlib
import json
import os
import platform
import pwd
import re
import shutil
import socket
import subprocess
import sys
sys.dont_write_bytecode = True
import tempfile
import time
import urllib.request
from pathlib import Path
from render import domain, render
from certificates import validate

APP = Path('/opt/guangyue-personal')
STATE = Path('/var/lib/guangyue')
CONFIG = Path('/etc/guangyue-personal.json')
UNITS = ['guangyue', 'guangyue-xray', 'guangyue-hy2']
DEPLOY = Path(__file__).resolve().parent
UPSTREAM_HASHES = {
    'xray': '8255dd939c34cf966cc91517b6324dd3c8d0bcf49ffac8beca049a38c46845ed',
    'mihomo': '8ad44e28fe72be4640254b96741b677f4074991b99186cc4486a1c28ded02b1a',  # gitleaks:allow public upstream SHA256
}


def run(*args):
    return subprocess.run(args, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout.decode()


def verify_bundle(bundle):
    bundle = Path(bundle).resolve()
    rows = (bundle / 'SHA256SUMS').read_text().splitlines()
    covered = set()
    for row in rows:
        digest, name = row.split('  ', 1)
        path = bundle / name
        if not re.fullmatch('[a-f0-9]{64}', digest) or name.startswith('/') or '..' in Path(name).parts or path.is_symlink():
            raise ValueError('invalid bundle manifest entry')
        if not path.resolve().is_relative_to(bundle):
            raise ValueError('bundle path escapes root')
        if hashlib.sha256(path.read_bytes()).hexdigest() != digest:
            raise ValueError('bundle checksum mismatch: ' + name)
        covered.add(name)
    required = {'VERSION', 'bin/guangyue-linux-amd64', 'bin/hysteria-node-linux-amd64', 'web/index.html'}
    if not required <= covered:
        raise ValueError('incomplete release bundle')
    for path in bundle.rglob('*'):
        name = path.relative_to(bundle).as_posix()
        if path.is_symlink():
            raise ValueError('bundle contains symlink')
        if path.is_file() and name not in covered and name not in {'SHA256SUMS', 'bin/xray-linux-amd64', 'bin/mihomo-linux-amd64'}:
            raise ValueError('unverified release file: ' + name)
    # Downloaded upstream binaries have their own upstream checksums, verified by fetch scripts.
    for name in ['xray', 'mihomo']:
        if not (bundle / 'bin' / (name + '-linux-amd64')).is_file():
            raise ValueError('run scripts/fetch-' + name + '.sh with GY_BIN_DIR=' + str(bundle / 'bin'))
        if hashlib.sha256((bundle / 'bin' / (name + '-linux-amd64')).read_bytes()).hexdigest() != UPSTREAM_HASHES[name]:
            raise ValueError('upstream binary checksum mismatch: ' + name)


def check_free_ports():
    # Include IPv6 and every reserved internal port, not only TCP/UDP 443.
    ports = [(socket.SOCK_STREAM, p) for p in [443, 10443, 18443, 19100, 19101, 19185, 19186, 19199]]
    ports += [(socket.SOCK_DGRAM, p) for p in [443, 19186]]
    ports += [(socket.SOCK_STREAM, p) for p in range(21000, 21256)]
    for family, addr in [(socket.AF_INET, '0.0.0.0'), (socket.AF_INET6, '::')]:
        for kind, port in ports:
            with socket.socket(family, kind) as probe:
                if family == socket.AF_INET6:
                    probe.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 1)
                try:
                    probe.bind((addr, port))
                except OSError as exc:
                    raise ValueError(f'required port unavailable: {addr}:{port} ({"TCP" if kind == socket.SOCK_STREAM else "UDP"})') from exc


def preflight(args):
    if os.geteuid() != 0:
        raise ValueError('run preflight as root to inspect Nginx and certificate permissions')
    if platform.system() != 'Linux' or platform.machine() not in {'x86_64', 'amd64'}:
        raise ValueError('release installer supports Linux amd64 only')
    release = platform.freedesktop_os_release()
    if (release.get('ID'), release.get('VERSION_ID')) not in {('debian', '12'), ('debian', '13'), ('ubuntu', '22.04'), ('ubuntu', '24.04')}:
        raise ValueError('supported hosts: Debian 12/13, Ubuntu 22.04/24.04')
    if not Path('/run/systemd/system').is_dir():
        raise ValueError('systemd must be PID 1 on the deployment host')
    for command in ['nginx', 'systemctl', 'systemd-tmpfiles', 'openssl', 'useradd', 'runuser']:
        if shutil.which(command) is None:
            raise ValueError('missing dependency: ' + command)
    for path in [APP, STATE, CONFIG, Path('/etc/guangyue-certificate.json'), Path('/etc/nginx/guangyue-stream.conf'), Path('/etc/nginx/guangyue-tls'), Path('/etc/nginx/sites-enabled/guangyue-panel.conf'), Path('/etc/tmpfiles.d/guangyue-reality.conf'), Path('/run/guangyue-reality')]:
        if path.exists() or path.is_symlink():
            raise ValueError('existing managed path; use upgrade or inspect first: ' + str(path))
    for unit in UNITS:
        if run('systemctl', 'show', unit, '--property=LoadState', '--value').strip() != 'not-found':
            raise ValueError('existing service: ' + unit)
    verify_bundle(args.bundle)
    render(args.panel_domain, args.node_domain, args.reality_sni, args.web_domain)
    validate(args.cert, args.key, {args.panel_domain, args.node_domain})
    nginx = run('nginx', '-T')
    if not re.search(r'^\s*user\s+www-data(?:\s+www-data)?\s*;', nginx, re.M):
        raise ValueError('Nginx workers must use www-data for protected Reality sockets')
    if re.search(r'^\s*stream\s*\{', nginx, re.M):
        raise ValueError('existing stream block: use documented manual merge, not automatic installation')
    if 'sites-enabled/' not in nginx:
        raise ValueError('Nginx must include /etc/nginx/sites-enabled/* inside http {}')
    # Existing HTTP virtual hosts with these names must be explicitly reconciled by the operator.
    for line in nginx.splitlines():
        if re.match(r'\s*server_name\s', line) and any(x in line.split()[1:] for x in [args.panel_domain, args.node_domain, args.panel_domain + ';', args.node_domain + ';']):
            raise ValueError('a virtual host already uses a requested domain; remove only its temporary ACME site first')
    check_free_ports()
    # Validate the actual stream module and all fragments without editing live Nginx.
    with tempfile.TemporaryDirectory(prefix='guangyue-preflight-') as temp:
        path = Path(temp)
        files = render(args.panel_domain, args.node_domain, args.reality_sni, args.web_domain)
        panel = files['nginx-panel.conf'].replace('/etc/nginx/guangyue-tls/current/fullchain.pem', str(args.cert)).replace('/etc/nginx/guangyue-tls/current/privkey.pem', str(args.key))
        (path / 'nginx.conf').write_text('include /etc/nginx/modules-enabled/*.conf;\nevents {}\nhttp {\n' + panel + '\n}\n' + files['nginx-stream.conf'])
        run('nginx', '-t', '-c', str(path / 'nginx.conf'))


def health():
    for _ in range(20):
        try:
            with urllib.request.build_opener(urllib.request.ProxyHandler({})).open('http://127.0.0.1:19100/api/health', timeout=2) as response:
                if response.status == 200:
                    run('systemctl', 'is-active', '--quiet', *UNITS, 'nginx')
                    return
        except Exception:
            time.sleep(1)
    raise ValueError('services did not become healthy')


def install(args):
    bundle = args.bundle.resolve()
    backup = Path('/root/guangyue-backups') / ('install-' + time.strftime('%Y%m%d-%H%M%S'))
    backup.mkdir(parents=True, mode=0o700)
    os.chmod(backup.parent, 0o700)
    shutil.copy2('/etc/nginx/nginx.conf', backup / 'nginx.conf')
    created = []
    try:
        try:
            user = pwd.getpwnam('guangyue')
        except KeyError:
            run('useradd', '--system', '--user-group', '--home-dir', str(STATE), '--shell', '/usr/sbin/nologin', 'guangyue')
            user = pwd.getpwnam('guangyue')
        for path in [APP, STATE]:
            path.mkdir(mode=0o755 if path == APP else 0o700)
            os.chmod(path, 0o755 if path == APP else 0o700)
            created.append(path)
        os.chown(STATE, user.pw_uid, user.pw_gid)
        (STATE / 'tls').mkdir(mode=0o700)
        os.chown(STATE / 'tls', user.pw_uid, user.pw_gid)
        (APP / 'bin').mkdir()
        os.chmod(APP / 'bin', 0o755)
        for src, dest in [('guangyue-linux-amd64', 'guangyue'), ('hysteria-node-linux-amd64', 'hysteria'), ('xray-linux-amd64', 'xray'), ('mihomo-linux-amd64', 'mihomo')]:
            shutil.copyfile(bundle / 'bin' / src, APP / 'bin' / dest)
            os.chmod(APP / 'bin' / dest, 0o755)
        for name in ['web', 'licenses', 'deploy']:
            shutil.copytree(bundle / name, APP / name)
        for path in APP.rglob('*'):
            if path.is_dir():
                path.chmod(0o755)
            elif 'bin' not in path.relative_to(APP).parts:
                path.chmod(0o644)
        shutil.copyfile(bundle / 'VERSION', APP / 'VERSION')
        run(str(APP / 'bin/guangyue'), '-init')
        created.append(CONFIG)
        config = json.loads(CONFIG.read_text())
        config.update(public_url='https://' + args.panel_domain, vless_host=args.node_domain, hy2_host=args.node_domain, reality_sni=args.reality_sni, reality_target=args.reality_sni + ':443', cert=str(STATE / 'tls/current/fullchain.pem'), cert_key=str(STATE / 'tls/current/privkey.pem'))
        CONFIG.write_text(json.dumps(config, indent=2) + '\n')
        os.chown(CONFIG, 0, user.pw_gid)
        os.chmod(CONFIG, 0o640)
        metadata = Path('/etc/guangyue-certificate.json')
        metadata.write_text(json.dumps({'cert': str(args.cert.absolute()), 'key': str(args.key.absolute()), 'domains': list(dict.fromkeys([args.panel_domain, args.node_domain]))}))
        os.chmod(metadata, 0o600)
        created += [metadata, Path('/etc/nginx/guangyue-tls')]
        run(sys.executable, str(APP / 'deploy/certificates.py'), '--initial')
        tmpfiles = Path('/etc/tmpfiles.d/guangyue-reality.conf')
        shutil.copyfile(DEPLOY / 'guangyue-reality.tmpfiles.conf', tmpfiles)
        created += [tmpfiles, Path('/run/guangyue-reality')]
        run('systemd-tmpfiles', '--create', str(tmpfiles))
        run('runuser', '-u', 'guangyue', '--', str(APP / 'bin/guangyue'), '-prepare')
        run('runuser', '-u', 'guangyue', '--', str(APP / 'bin/xray'), 'run', '-test', '-c', str(STATE / 'xray.json'))
        for name, content in render(args.panel_domain, args.node_domain, args.reality_sni, args.web_domain).items():
            dest = Path('/etc/nginx/guangyue-stream.conf') if name == 'nginx-stream.conf' else Path('/etc/nginx/sites-enabled/guangyue-panel.conf')
            dest.write_text(content)
            created.append(dest)
        with open('/etc/nginx/nginx.conf', 'a') as output:
            output.write('\n# Guangyue Panel managed TCP 443 entry\ninclude /etc/nginx/guangyue-stream.conf;\n')
        run('nginx', '-t')
        for unit in UNITS:
            path = Path('/etc/systemd/system') / (unit + '.service')
            shutil.copyfile(DEPLOY / path.name, path)
            created.append(path)
        hook = Path('/etc/letsencrypt/renewal-hooks/deploy/guangyue-panel')
        if hook.exists():
            raise ValueError('renewal hook already exists')
        hook.parent.mkdir(parents=True, exist_ok=True)
        hook.write_text('#!/bin/sh\nexec /usr/bin/python3 /opt/guangyue-personal/deploy/certificates.py\n')
        hook.chmod(0o750)
        created.append(hook)
        run('systemctl', 'daemon-reload')
        run('systemctl', 'enable', '--now', *UNITS)
        run('systemctl', 'reload', 'nginx')
        health()
    except Exception:
        subprocess.run(['systemctl', 'disable', '--now', *UNITS], capture_output=True)
        shutil.copy2(backup / 'nginx.conf', '/etc/nginx/nginx.conf')
        # Preserve failed state and credentials privately for diagnosis/retry.
        if STATE.exists():
            shutil.move(str(STATE), backup / 'failed-state')
        if CONFIG.exists():
            shutil.copy2(CONFIG, backup / 'failed-config.json')
        for path in reversed(created):
            if path.is_dir() and not path.is_symlink():
                shutil.rmtree(path)
            else:
                path.unlink(missing_ok=True)
        subprocess.run(['systemctl', 'daemon-reload'], capture_output=True)
        subprocess.run(['systemctl', 'reload', 'nginx'], capture_output=True)
        print('Installation rolled back. Private recovery directory: ' + str(backup), file=sys.stderr)
        raise
    print('Installed Guangyue Panel at https://' + args.panel_domain)
    print('Initial owner credentials: /var/lib/guangyue/initial-owner.json (read locally; change password immediately).')
    print('Backup: ' + str(backup))


def main():
    os.umask(0o077)
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--bundle', required=True, type=Path)
    p.add_argument('--panel-domain', required=True, type=domain)
    p.add_argument('--node-domain', required=True, type=domain)
    p.add_argument('--reality-sni', default='www.cloudflare.com', type=domain)
    p.add_argument('--web-domain', action='append', default=[], type=domain)
    p.add_argument('--cert', required=True, type=Path)
    p.add_argument('--key', required=True, type=Path)
    p.add_argument('--apply', action='store_true')
    args = p.parse_args()
    preflight(args)
    print('Preflight passed. Ports, bundle, certificate, Nginx modules and worker identity checked.')
    if args.apply:
        install(args)
    else:
        print('Read-only check. Add --apply to install.')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, subprocess.CalledProcessError) as exc:
        # Commands can process secrets; do not echo captured stdout/stderr.
        message = str(exc) if isinstance(exc, ValueError) else type(exc).__name__
        raise SystemExit('Installation failed: ' + message)
