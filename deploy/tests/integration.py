#!/usr/bin/env python3
"""Destructive-to-this-test-install only: run on a disposable GitHub Ubuntu runner."""
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
import urllib.request
from pathlib import Path

if os.geteuid() != 0 or os.environ.get('GITHUB_ACTIONS') != 'true':
    raise SystemExit('This integration harness is restricted to disposable GitHub Actions runners.')
if os.environ.get('GY_INTEGRATION_EDITION')=='business':
    os.execv(sys.executable,[sys.executable,str(Path(__file__).with_name('business_integration.py')),*sys.argv[1:]])
sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import install
import infrastructure

bundle = Path(sys.argv[1]).resolve()
temp = Path(tempfile.mkdtemp(prefix='guangyue-integration-'))
os.chmod(temp, 0o700)
cert, key = temp / 'fullchain.pem', temp / 'privkey.pem'


def run(*args, success=True):
    p = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if success and p.returncode:
        # Never print a command's output: it may contain ephemeral credentials.
        safe=[line for line in p.stderr.decode(errors='replace').splitlines() if line.startswith(('Installation failed:','Upgrade failed:','Infrastructure setup failed:'))]
        detail=('; '+safe[-1][:300]) if safe else ''
        raise RuntimeError('command failed: ' + Path(args[0]).name + ' exit=' + str(p.returncode)+detail)
    return p


def newcert():
    run('openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '3', '-subj', '/CN=panel.example.com', '-addext', 'subjectAltName=DNS:panel.example.com,DNS:node.example.com', '-keyout', str(key), '-out', str(cert))


def api(path, data=None, cookie=''):
    headers = {'X-Requested-With': 'guangyue', 'Content-Type': 'application/json'}
    if cookie:
        headers['Cookie'] = cookie
    request = urllib.request.Request('http://127.0.0.1:19100' + path, data=json.dumps(data).encode() if data is not None else None, headers=headers)
    with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(request, timeout=15) as response:
        return json.load(response), response.headers.get('Set-Cookie', '').split(';')[0]


try:
    newcert()
    command = [sys.executable, str(bundle / 'deploy/install.py'), '--bundle', str(bundle), '--panel-domain', 'panel.example.com', '--node-domain', 'node.example.com', '--cert', str(cert), '--key', str(key)]
    edition=os.environ.get('GY_INTEGRATION_EDITION','lite')
    if edition=='pro':
        run(sys.executable,str(bundle/'deploy/infrastructure.py'),'--apply')
        command+=['--edition','pro','--site-id','ci_pro','--infrastructure-file',str(infrastructure.PROFILE)]
    run(*command)
    assert not install.CONFIG.exists(), 'preflight wrote production config'
    run(*command, '--apply')
    install.health()
    assert install.STATE.stat().st_mode & 0o777 == 0o700
    assert Path('/run/guangyue-reality').stat().st_mode & 0o7777 == 0o2750
    run('curl', '-fsS', '--noproxy', '*', '--cacert', str(cert), '--resolve', 'panel.example.com:443:127.0.0.1', 'https://panel.example.com/api/health')
    initial = json.loads((install.STATE / 'initial-owner.json').read_text())
    _, cookie = api('/api/login', initial)
    state, _ = api('/api/subscription', cookie=cookie)
    assert {n['protocol'] for n in state['nodes']} == {'vless', 'hy2'}
    public, _ = api('/api/subscription?pool=public', cookie=cookie)
    assert not public['nodes'] and public['url'] != state['url']
    print('PASS fresh install, service health, real Nginx HTTPS, private permissions and subscription separation')

    # Occupied paths must refuse a second install, without touching current data.
    assert run(*command, '--apply', success=False).returncode != 0
    install.health()
    print('PASS existing deployment refusal')

    cert_script = str(install.APP / 'deploy/certificates.py')
    old_hash = hashlib.sha256(Path('/etc/nginx/guangyue-tls/current/fullchain.pem').read_bytes()).hexdigest()
    original_key = key.read_bytes()
    run('openssl', 'genrsa', '-out', str(key), '2048')
    assert run(sys.executable, cert_script, success=False).returncode != 0
    assert hashlib.sha256(Path('/etc/nginx/guangyue-tls/current/fullchain.pem').read_bytes()).hexdigest() == old_hash
    key.write_bytes(original_key)
    newcert()
    run(sys.executable, cert_script)
    assert hashlib.sha256(Path('/etc/nginx/guangyue-tls/current/fullchain.pem').read_bytes()).hexdigest() != old_hash
    run('curl', '-fsS', '--noproxy', '*', '--cacert', str(cert), '--resolve', 'panel.example.com:443:127.0.0.1', 'https://panel.example.com/api/health')
    print('PASS certificate mismatch rejection and real renewal/reload')

    run(sys.executable, str(bundle / 'deploy/upgrade.py'), '--bundle', str(bundle), '--apply')
    install.health()
    print('PASS offline backup and successful upgrade')

    # A checksum-consistent release whose executable fails must trigger rollback.
    bad = temp / 'fault-bundle'
    shutil.copytree(bundle, bad)
    (bad / 'bin/guangyue-linux-amd64').write_text('#!/bin/sh\nexit 42\n')
    lines = []
    for row in (bad / 'SHA256SUMS').read_text().splitlines():
        _, name = row.split('  ', 1)
        lines.append(hashlib.sha256((bad / name).read_bytes()).hexdigest() + '  ' + name)
    (bad / 'SHA256SUMS').write_text('\n'.join(lines) + '\n')
    assert run(sys.executable, str(bundle / 'deploy/upgrade.py'), '--bundle', str(bad), '--apply', success=False).returncode != 0
    assert any(p.read_bytes() == b'#!/bin/sh\nexit 42\n' for p in Path('/root/guangyue-backups').glob('upgrade-*/failed-app/bin/guangyue')), 'fault did not reach rollback'
    install.health()
    _, cookie = api('/api/login', initial)
    restored, _ = api('/api/subscription', cookie=cookie)
    assert sorted(n['uri'] for n in restored['nodes']) == sorted(n['uri'] for n in state['nodes']), 'rollback changed credentials'
    print('PASS failed executable rollback and credential preservation')
    if edition=='lite':
        run(sys.executable,str(bundle/'deploy/infrastructure.py'),'--apply')
        run(sys.executable,str(bundle/'deploy/upgrade.py'),'--bundle',str(bundle),'--edition','pro','--site-id','ci_migrated','--infrastructure-file',str(infrastructure.PROFILE),'--apply')
        install.health()
        _,cookie=api('/api/login',initial)
        migrated,_=api('/api/subscription',cookie=cookie)
        assert sorted(n['uri'] for n in migrated['nodes'])==sorted(n['uri'] for n in state['nodes'])
        print('PASS Lite to Pro migration preserves live subscription URIs')
    health,_=api('/api/health');assert health['edition']=='pro'
    run(sys.executable,str(bundle/'deploy/upgrade.py'),'--bundle',str(bundle),'--apply')
    assert run(sys.executable,str(bundle/'deploy/upgrade.py'),'--bundle',str(bad),'--apply',success=False).returncode!=0
    install.health()
    _,cookie=api('/api/login',initial)
    restored,_=api('/api/subscription',cookie=cookie)
    assert sorted(n['uri'] for n in restored['nodes'])==sorted(n['uri'] for n in state['nodes'])
    assert list(Path('/root/guangyue-backups').glob('upgrade-*/postgres.dump'))
    print('PASS Pro upgrade, PostgreSQL schema rollback and credential preservation')

finally:
    # Runner is ephemeral; stop only our services. Do not emit or upload state.
    subprocess.run(['systemctl', 'stop', *install.UNITS], capture_output=True)
    shutil.rmtree(temp)
