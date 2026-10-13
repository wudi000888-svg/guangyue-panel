#!/usr/bin/env python3
"""Disposable Ubuntu VM verification. Synthetic versions are NEVER published."""
import hashlib
import http.client
import json
import os
import re
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import traceback
import urllib.request
import uuid
from pathlib import Path
from unittest.mock import patch

sys.dont_write_bytecode = True
if os.geteuid() != 0 or os.environ.get('GITHUB_ACTIONS') != 'true' or os.environ.get('GY_ONECLICK_INTEGRATION') != '1':
    raise SystemExit('Disposable GitHub Actions VM fixture only')
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
sys.path.insert(0, str(Path(__file__).resolve().parent))
import native_gateway as native
import oneclick
import update_state as state
import update_agent as agent
import maintenance_gate
import upgrade


def run(*args, success=True, timeout=180, cwd=None, environment=None):
    result = subprocess.run(args, capture_output=True, timeout=timeout, cwd=cwd, env=environment)
    if success and result.returncode:
        raise AssertionError('CI command failed: ' + Path(args[0]).name + ' exit=' + str(result.returncode))
    return result


def fingerprint(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def api(path, data=None, cookie=''):
    headers = {'Content-Type': 'application/json', 'X-Requested-With': 'guangyue', 'Cookie': cookie}
    request = urllib.request.Request('http://127.0.0.1:19100' + path, data=None if data is None else json.dumps(data).encode(), headers=headers)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(request, timeout=20) as response:
        return json.load(response), response.headers.get('Set-Cookie', '').split(';')[0]


class UnixHTTP(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(30); self.sock.connect(state.SOCKET)


def updater_state():
    connection = UnixHTTP('localhost'); connection.request('GET', '/state')
    response = connection.getresponse(); result = json.load(response)
    assert response.status == 200
    connection.close(); return result


def new_cert(directory, suffix):
    cert, key = directory / ('cert-' + suffix + '.pem'), directory / ('key-' + suffix + '.pem')
    run('openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '3', '-subj', '/CN=panel.example.com', '-addext', 'subjectAltName=DNS:panel.example.com,DNS:node.example.com', '-keyout', str(key), '-out', str(cert))
    cert.chmod(0o600); key.chmod(0o600)
    return cert, key


def https_health(cert, version):
    result = run('curl', '-fsS', '--noproxy', '*', '--cacert', str(cert), '--resolve', 'panel.example.com:443:127.0.0.1', 'https://panel.example.com/api/health', timeout=25)
    result = json.loads(result.stdout)
    assert result['version'] == version
    return result


def frozen_window(cert, version):
    assert maintenance_gate.marker() is not None, 'uncommitted deployment lost its root-owned freeze'
    # A stopped-snapshot recovery can restart the gateway slightly after the
    # local health endpoint; wait for the real gateway before testing ingress.
    run(str(native.APP / 'bin/guangyue-gateway'), '-config', str(native.GATEWAY_CONFIG), '-wait-ready', '45s', timeout=55)
    deadline = time.monotonic() + 50
    while True:
        health = https_health(cert, version)
        assert health.get('maintenance') is True
        if health.get('background_frozen') is True:
            break
        assert time.monotonic() < deadline, 'maintenance advertised health before all data/external workers froze'
        time.sleep(0.2)
    # Logout is harmless and would mutate only a session. No financial or
    # credential-changing request is needed to verify the real write fence.
    for url, extra in (
        ('http://127.0.0.1:19100/api/logout', ()),
        ('https://panel.example.com/api/logout', ('--cacert', str(cert), '--resolve', 'panel.example.com:443:127.0.0.1')),
    ):
        response = run('curl', '-sS', '--noproxy', '*', *extra, '-o', '/dev/null', '-w', '%{http_code}', '-X', 'POST', '-H', 'Content-Type: application/json', '--data', '{}', url, timeout=20)
        assert response.stdout.strip() == b'503', 'uncommitted deployment accepted an external or loopback write'


def thawed_window(cert, version):
    assert maintenance_gate.marker() is None, 'completed or recovered transaction left a maintenance freeze'
    assert https_health(cert, version).get('maintenance') is False


def nginx_snapshot():
    values = {path.relative_to('/etc/nginx').as_posix(): fingerprint(path) for path in Path('/etc/nginx').rglob('*') if path.is_file()}
    result = run('curl', '-fsS', '--noproxy', '*', 'http://127.0.0.1/', timeout=10)
    return values, result.stdout


def same_nginx(before):
    assert nginx_snapshot() == before, 'private gateway modified host Nginx configuration or HTTP response'
    run('systemctl', 'is-active', '--quiet', 'nginx')


def default_database_services():
    active = set()
    if run('systemctl', 'is-active', '--quiet', 'redis-server', success=False).returncode == 0:
        active.add('redis-server')
    if shutil.which('pg_lsclusters'):
        for cluster in json.loads(run('pg_lsclusters', '--json').stdout):
            if cluster.get('cluster') != 'guangyue' and str(cluster.get('status', '')).startswith('online'):
                active.add('postgresql@' + str(cluster['version']) + '-' + cluster['cluster'])
    return active


def published_certificate_digest():
    base = native.STATE / 'tls/current'
    return hashlib.sha256((base / 'fullchain.pem').read_bytes() + b'\0' + (base / 'privkey.pem').read_bytes()).hexdigest()


def wait_certificate_reconciled(digest, previous_pid=None):
    # A timer's first initial reconciliation must not be mistaken for handling
    # the replacement certificate. Check both the exact generation digest and
    # service completion; the timer also makes deployment-lock races retryable.
    deadline = time.monotonic() + 100
    while time.monotonic() < deadline:
        record = state.read('gateway-certificate.json') or {}
        worker_idle = run('systemctl', 'is-active', '--quiet', 'guangyue-gateway-cert.service', success=False).returncode != 0
        pid = run('systemctl', 'show', 'guangyue-hy2', '--property=MainPID', '--value').stdout.strip()
        if record.get('digest') == digest and worker_idle and pid not in (b'', b'0') and (previous_pid is None or pid != previous_pid):
            return
        time.sleep(1)
    raise AssertionError('certificate generation was not reconciled by the fixed path/timer worker')


def rewrite_manifest(bundle):
    rows = [fingerprint(path) + '  ' + path.relative_to(bundle).as_posix() for path in sorted(bundle.rglob('*')) if path.is_file() and path.relative_to(bundle).as_posix() != 'SHA256SUMS']
    (bundle / 'SHA256SUMS').write_text('\n'.join(rows) + '\n')


def synthetic_bundle(source, work, version):
    """Build a real matching Go binary from an isolated CI-only source copy."""
    target = work / ('synthetic-' + version); shutil.copytree(source, target)
    repo = Path(__file__).resolve().parents[2]
    sources = work / ('backend-' + version)
    shutil.copytree(repo / 'backend', sources, ignore=shutil.ignore_patterns('__pycache__', '*.pyc', '.cache'))
    config = sources / 'internal/controlplane/config.go'
    code, count = re.subn(r'const version = "[0-9.]+"', 'const version = "' + version + '"', config.read_text())
    assert count == 1, 'CI fixture cannot find the version constant'
    config.write_text(code)
    environment = dict(os.environ, CGO_ENABLED='0', GOOS='linux', GOARCH='amd64')
    go = os.environ.get('GY_GO') or shutil.which('go')
    assert go, 'CI VM needs Go 1.26.9 for isolated synthetic binary fixtures'
    run(go, 'build', '-trimpath', '-ldflags=-s -w', '-o', str(target / 'bin/guangyue-linux-amd64'), '.', timeout=300, cwd=sources, environment=environment)
    (target / 'VERSION').write_text(version + '\n')
    (target / 'CI_SYNTHETIC_VERSION').write_text('Never upload this fixture or tag this version. Disposable VM only.\n')
    rewrite_manifest(target)
    return target


def connections(subscription):
    # Display-only node names can refresh asynchronously. Compare every real
    # protocol credential/address/parameter while ignoring URI fragments.
    return {node['id']: node['uri'].split('#', 1)[0] for node in subscription['nodes']}


def recovery_diagnostic(stage, error):
    # Transaction recovery deliberately hides command output and credentials.
    # Preserve only the failing fixture stage, exception type and code location
    # so a genuine restore error cannot be mistaken for a missing test hook.
    frames = ','.join(Path(frame.filename).name + ':' + str(frame.lineno) for frame in traceback.extract_tb(error.__traceback__))
    print('CI recovery failure during ' + stage + ': ' + type(error).__name__ + ' at ' + frames, file=sys.stderr, flush=True)


def execute(request, bundle=None, expect='succeeded', certificate=None):
    state.write('operation.json', dict(request, stage='queued', queued_at=int(time.time())))
    windows = []
    original_prepare, original_restore = upgrade.prepare_start, upgrade.restore_current
    def checked_prepare(version):
        original_prepare(version)
        frozen_window(certificate, version)
        windows.append('prepared')
    def checked_restore(backup):
        try:
            original_restore(backup)
        except Exception as error:
            recovery_diagnostic('restore_current', error)
            raise
        try:
            frozen_window(certificate, (Path(backup) / 'app/VERSION').read_text().strip())
        except Exception as error:
            recovery_diagnostic('restored_frozen_window', error)
            raise
        windows.append('recovered')
    assert certificate is not None
    with patch.object(upgrade, 'prepare_start', side_effect=checked_prepare), patch.object(upgrade, 'restore_current', side_effect=checked_restore):
        agent.execute(request, local_bundle=bundle)
    operation = state.read('operation.json')
    assert operation['stage'] == expect, 'CI updater operation ended at ' + operation['stage']
    if expect == 'succeeded':
        assert 'prepared' in windows, 'successful transaction did not exercise the uncommitted write fence'
    elif bundle is not None:
        assert 'recovered' in windows, 'faulty update did not exercise the restored-but-still-frozen recovery window'
    thawed_window(certificate, state.current())


def request(action, version, expected):
    return {'action': action, 'version': version, 'expected_version': expected, 'request_id': str(uuid.uuid4())}


def main():
    if len(sys.argv) != 2:
        raise SystemExit('Usage: native_oneclick_integration.py ROOT_OWNED_CHECKED_BUNDLE')
    source = Path(sys.argv[1]).resolve()
    edition = os.environ.get('GY_NATIVE_EDITION', os.environ.get('GY_INTEGRATION_EDITION', 'lite')).removeprefix('native-')
    assert edition in ('lite', 'pro')
    assert (source / 'EDITION').read_text().strip() == edition
    current = oneclick.checked_version((source / 'VERSION').read_text().strip())
    major, minor, patch_number = map(int, current.split('.'))
    base_version = f'{major}.{minor}.{patch_number + 1}'
    next_version = f'{major}.{minor}.{patch_number + 2}'
    fault_version = f'{major}.{minor}.{patch_number + 3}'
    work = Path(tempfile.mkdtemp(prefix='guangyue-native-ci-', dir='/root')); work.chmod(0o700)
    print('CI ONLY: isolated synthetic versions verify transactions; no Release or tag is created.', flush=True)
    try:
        baseline = nginx_snapshot()
        sysctl_keys = ('net.ipv4.tcp_congestion_control', 'net.core.default_qdisc', 'net.core.rmem_max', 'net.core.wmem_max')
        before_network = {key: run('sysctl', '-n', key).stdout for key in sysctl_keys}
        default_units = default_database_services()
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as blocker:
            blocker.bind(('0.0.0.0', 443))
            try:
                oneclick.port_preflight(True)
            except ValueError as error:
                assert 'UDP 443' in str(error)
            else:
                raise AssertionError('occupied UDP 443 was accepted')
        assert not native.CONFIG.exists() and not native.APP.exists()
        same_nginx(baseline)
        print('PASS read-only occupied UDP 443 detection preserves Nginx and creates no panel config', flush=True)

        base = synthetic_bundle(source, work, base_version)
        target = synthetic_bundle(source, work, next_version)
        cert, key = new_cert(work, 'initial')
        plan = {'schema': 1, 'id': str(uuid.uuid4()), 'version': base_version, 'edition': edition, 'backend': 'native', 'panel_domain': 'panel.example.com', 'node_domain': 'node.example.com', 'reality_sni': 'www.cloudflare.com', 'site_id': 'default', 'acme_agreed': True, 'acme_email': '', 'created_at': int(time.time())}
        real_run = native.run
        def after_prepare(*args, **kwargs):
            result = real_run(*args, **kwargs)
            if args[-1:] == ('-prepare',):
                raise ValueError('CI injected fault after real account/key preparation')
            return result
        with patch.object(native, 'run', side_effect=after_prepare):
            try:
                native.install(plan, base, cert_file=cert, key_file=key)
            except ValueError:
                pass
            else:
                raise AssertionError('CI post-prepare failure was not injected')
        assert json.loads(native.journal_path().read_text())['phase'] == 'failed'
        keys = {str(path): fingerprint(path) for path in (native.CONFIG, native.STATE / 'master.key', native.STATE / 'initial-owner.json')}
        for unit in native.UNITS:
            assert run('systemctl', 'is-active', '--quiet', unit, success=False).returncode != 0
        same_nginx(baseline)
        # An external edit must be refused rather than overwritten by resume.
        unit = native.unit_path('guangyue-gateway')
        original = unit.read_bytes(); unit.write_bytes(original + b'\n# CI external edit\n')
        edited = fingerprint(unit)
        try:
            native.install(plan, base, cert_file=cert, key_file=key)
        except ValueError:
            assert fingerprint(unit) == edited
        else:
            raise AssertionError('externally modified owned unit was overwritten')
        unit.write_bytes(original)
        result = native.install(plan, base, cert_file=cert, key_file=key)
        assert result['stage'] == 'installed'
        assert {str(path): fingerprint(path) for path in map(Path, keys)} == keys, 'resume regenerated account or master/config keys'
        # Completed installation is a health check, not a fresh initialization.
        native.install(plan, base, cert_file=cert, key_file=key)
        assert {str(path): fingerprint(path) for path in map(Path, keys)} == keys
        assert native.STATE.stat().st_mode & 0o777 == 0o700
        assert native.DEPLOYMENT.stat().st_mode & 0o777 == 0o600
        assert json.loads(native.CONFIG.read_text())['private_gateway'] is True
        assert not Path('/etc/nginx/guangyue-stream.conf').exists()
        assert not Path('/etc/systemd/system/guangyue-network.service').exists()
        assert {key: run('sysctl', '-n', key).stdout for key in sysctl_keys} == before_network
        assert default_database_services() == default_units
        https_health(cert, base_version); same_nginx(baseline)
        native.resume_preflight(plan)
        oneclick.port_preflight(True, native.resume_binding_owners(plan))
        print('PASS ' + edition + ' real native install, account/key-preserving recovery, owned listener inspection and HTTPS 443', flush=True)

        initial = json.loads((native.STATE / 'initial-owner.json').read_text())
        _, cookie = api('/api/login', initial)
        assert updater_state()['current_version'] == base_version
        wait_certificate_reconciled(published_certificate_digest())
        # Rotate the fixture source through the real private gateway and fixed
        # certificate path worker; no production CA flag is introduced.
        new_certificate, new_key = new_cert(work, 'rotation')
        old_pid = run('systemctl', 'show', 'guangyue-hy2', '--property=MainPID', '--value').stdout.strip()
        fixture = native.STATE / 'gateway-fixture'
        for source_file, name in ((new_certificate, 'fullchain.pem'), (new_key, 'privkey.pem')):
            shutil.copyfile(source_file, fixture / name); shutil.chown(fixture / name, user='guangyue', group='guangyue'); (fixture / name).chmod(0o600)
        run('systemctl', 'restart', 'guangyue-gateway')
        run(str(native.APP / 'bin/guangyue-gateway'), '-config', str(native.GATEWAY_CONFIG), '-wait-ready', '45s', timeout=55)
        wait_certificate_reconciled(published_certificate_digest(), old_pid)
        native.native_health(base_version); https_health(new_certificate, base_version)
        published_key = native.STATE / 'tls/current/privkey.pem'; saved_key = published_key.read_bytes()
        shutil.copyfile(key, published_key)
        mismatch_pid = run('systemctl', 'show', 'guangyue-hy2', '--property=MainPID', '--value').stdout
        invalid = run(sys.executable, '-I', str(state.HELPER / 'gateway_certificate.py'), success=False)
        assert invalid.returncode != 0
        import gateway_certificate
        try:
            gateway_certificate.reconcile()
        except ValueError as error:
            assert 'certificate and private key do not match' in str(error)
        else:
            raise AssertionError('mismatched pair was not rejected by the actual fixed reconciler')
        assert run('systemctl', 'show', 'guangyue-hy2', '--property=MainPID', '--value').stdout == mismatch_pid
        published_key.write_bytes(saved_key); published_key.chmod(0o600); shutil.chown(published_key, user='guangyue', group='guangyue')
        same_nginx(baseline)
        print('PASS real gateway certificate rotation, HY2 restart and mismatched pair rejection preserve host Nginx', flush=True)

        from entitlement_integration import verify_entitlements
        verify_entitlements(base, work, new_certificate, api, cookie, initial['password'])
        print('PASS actual VLESS/HY2 TCP/UDP echo and plan revocation through private 443 gateway', flush=True)
        subscription, _ = api('/api/subscription', cookie=cookie)
        vault_before = fingerprint(native.STATE / 'master.key')
        execute(request('update', next_version, base_version), target, certificate=new_certificate)
        assert state.current() == next_version
        https_health(new_certificate, next_version); same_nginx(baseline)
        after, _ = api('/api/subscription', cookie=cookie)
        assert connections(after) == connections(subscription)
        assert fingerprint(native.STATE / 'master.key') == vault_before
        api('/api/users', {'username': 'after-private-gateway-update', 'password': 'CI-only-ephemeral-password', 'enabled': True, 'vless': True, 'hy2': True}, cookie)

        # Simulate interruption after the durable commit but before cleanup.
        # The referenced backup predates this user, so any mistaken restoration
        # would lose post-commit data rather than merely change a version label.
        previous = next(row for row in state.candidates() if row['version'] == base_version)
        maintenance_gate.begin()
        state.write('transaction.json', {'phase': 'committed', 'backup': previous['backup'], 'target': next_version, 'schema': previous['schema']})
        frozen_window(new_certificate, next_version)
        with patch.object(upgrade, 'restore_current', side_effect=AssertionError('committed crash recovery restored old state')) as restore:
            agent.recover()
            restore.assert_not_called()
        assert state.read('transaction.json') is None
        thawed_window(new_certificate, next_version)
        resumed, _ = api('/api/state', cookie=cookie)
        assert any(user['username'] == 'after-private-gateway-update' for user in resumed['users'])
        assert fingerprint(native.STATE / 'master.key') == vault_before

        # The preparing journal represents interruption before any replacement
        # or backup. Recover the same installed version and clear its freeze.
        maintenance_gate.begin()
        state.write('transaction.json', {'phase': 'preparing', 'previous': next_version, 'target': fault_version})
        frozen_window(new_certificate, next_version)
        agent.recover()
        assert state.read('transaction.json') is None
        thawed_window(new_certificate, next_version)
        assert fingerprint(native.STATE / 'master.key') == vault_before
        print('PASS real maintenance windows: healthy GET, blocked external/loopback POST, workers frozen and preparing/committed crash recovery preserves new data', flush=True)

        # A checksum-consistent executable fault must reach recovery, rather
        # than being rejected only by the checksum check before the transaction.
        fault = work / 'synthetic-fault'; shutil.copytree(target, fault)
        (fault / 'VERSION').write_text(fault_version + '\n')
        (fault / 'bin/guangyue-linux-amd64').write_text('#!/bin/sh\nexit 42\n')
        (fault / 'bin/guangyue-linux-amd64').chmod(0o755); rewrite_manifest(fault)
        legacy_cache = state.HELPER / '__pycache__'; legacy_cache.mkdir(mode=0o700, exist_ok=True)
        (legacy_cache / 'common.cpython-312.pyc').write_bytes(b'CI-only invalid legacy cache; never executable source')
        original_backup = upgrade.backup_current
        def legacy_checked_backup(config):
            backup = original_backup(config)
            assert not (backup / 'helper/__pycache__').exists(), 'new backups included bytecode caches'
            # A historical backup already included these files. Keep its
            # checksums valid and prove actual recovery skips only this benign,
            # root-owned cache while clearing the active helper's stale cache.
            shutil.copytree(legacy_cache, backup / 'helper/__pycache__')
            (backup / 'helper-checksums.json').write_text(json.dumps(upgrade.app_hashes(backup / 'helper')))
            return backup
        with patch.object(upgrade, 'backup_current', side_effect=legacy_checked_backup):
            execute(request('update', fault_version, next_version), fault, expect='failed', certificate=new_certificate)
        assert not legacy_cache.exists(), 'restored helper retained stale Python bytecode'
        assert state.current() == next_version
        https_health(new_certificate, next_version); same_nginx(baseline)
        assert fingerprint(native.STATE / 'master.key') == vault_before
        assert any(path.read_bytes() == b'#!/bin/sh\nexit 42\n' for path in Path('/root/guangyue-backups').glob('upgrade-*/failed-app/bin/guangyue'))
        record = next(row for row in state.candidates() if row['version'] == base_version)
        assert record['compatible']
        execute(request('rollback', base_version, next_version), certificate=new_certificate)
        assert state.current() == base_version
        https_health(new_certificate, base_version); same_nginx(baseline)
        current_state, _ = api('/api/state', cookie=cookie)
        assert any(user['username'] == 'after-private-gateway-update' for user in current_state['users'])
        after, _ = api('/api/subscription', cookie=cookie)
        assert connections(after) == connections(subscription)
        assert fingerprint(native.STATE / 'master.key') == vault_before
        assert updater_state()['installed_version'] == base_version
        execute(request('rollback', current, base_version), expect='failed', certificate=new_certificate)
        assert state.current() == base_version
        assert {key: run('sysctl', '-n', key).stdout for key in sysctl_keys} == before_network
        print('PASS real local-bundle updater, executable failure recovery and compatible rollback preserve new users, credentials, TLS, Nginx and installation floor', flush=True)
    finally:
        for name in ('guangyue-gateway-cert.timer', 'guangyue-gateway-cert.path', 'guangyue-gateway-cert.service', 'guangyue-updater.socket', 'guangyue-updater.service', *native.UNITS):
            subprocess.run(['systemctl', 'stop', name], capture_output=True)
        if native.journal_path().exists():
            native.stop_owned(json.loads(native.journal_path().read_text()))
        shutil.rmtree(work)


if __name__ == '__main__':
    main()
