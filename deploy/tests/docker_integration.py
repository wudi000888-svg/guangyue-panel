#!/usr/bin/env python3
"""Real Docker deployment, fixed 443 proxy traffic and receipt recovery in CI.

This harness is deliberately unavailable on an administrator's ordinary host.
It uses only generated credentials/certificates and local disposable echo data.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import socket
import socketserver
import ssl
import stat
import struct
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request
from urllib.parse import parse_qs, quote, unquote, urlencode, urlparse
import uuid
from unittest.mock import patch

sys.dont_write_bytecode = True


def run(*args, data=None, success=True, timeout=120):
    result = subprocess.run(args, input=data, capture_output=True, timeout=timeout)
    if success and result.returncode:
        # Configs, tokens and private keys must never enter workflow output.
        raise RuntimeError('Docker CI command failed: ' + Path(args[0]).name + ' exit=' + str(result.returncode))
    return result


def wait_for(check, timeout=75, interval=0.5):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            value = check()
            if value:
                return value
        except (OSError, ValueError, urllib.error.URLError):
            pass
        time.sleep(interval)
    raise AssertionError('Docker CI readiness timeout')


def fingerprint(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def synthetic_upgrade(source, work, target, manifest, *, fail_after_prepare=False):
    """Local CI-only binary/image fixture; never pushed, uploaded or tagged."""
    bundle = work / ('synthetic-bundle-' + target); shutil.copytree(source, bundle)
    sources = work / ('backend-' + target)
    repository = Path(__file__).resolve().parents[2]
    shutil.copytree(repository / 'backend', sources, ignore=shutil.ignore_patterns('__pycache__', '*.pyc', '.cache'))
    version_file = sources / 'internal/controlplane/config.go'
    code, count = re.subn(r'const version = "[0-9.]+"', 'const version = "' + target + '"', version_file.read_text())
    assert count == 1
    version_file.write_text(code)
    go = os.environ.get('GY_GO') or shutil.which('go')
    assert go, 'Disposable CI requires Go for real matching-version upgrade fixtures'
    environment = dict(os.environ, GOOS='linux', GOARCH='amd64', CGO_ENABLED='0')
    binary = bundle / 'bin/guangyue-linux-amd64'
    result = subprocess.run([go, 'build', '-trimpath', '-ldflags=-s -w', '-o', str(binary), '.'], cwd=sources, env=environment, capture_output=True, timeout=300)
    if result.returncode: raise AssertionError('Synthetic panel compilation failed')
    context = work / ('image-' + target); context.mkdir(mode=0o700)
    shutil.copyfile(binary, context / 'guangyue')
    (context / 'VERSION').write_text(target + '\n')
    extra = ''
    if fail_after_prepare:
        # The real -prepare executes first. A committed own-schema mutation
        # then forces recovery, proving PostgreSQL is restored rather than
        # only replacing the application image after a migration failure.
        helper = sources / 'cmd/ci-upgrade-fault'; helper.mkdir()
        (helper / 'main.go').write_text('''package main
import (
 "context"
 "encoding/json"
 "database/sql"
 "os"
 "os/exec"
 "path/filepath"
 "github.com/jackc/pgx/v5"
 _ "modernc.org/sqlite"
)
func main() {
 if len(os.Args)!=2 || os.Args[1]!="-prepare" { os.Exit(43) }
 if err:=exec.Command("/opt/guangyue-personal/bin/ci-good-panel","-prepare").Run(); err!=nil { os.Exit(44) }
 var c struct {StateDir string `json:"state_dir"`; SiteID string `json:"site_id"`; Database struct{Driver,DSN string} `json:"database"`}
 raw,err:=os.ReadFile("/etc/guangyue-personal.json");if err!=nil || json.Unmarshal(raw,&c)!=nil || c.SiteID!="default" {os.Exit(45)}
 if c.Database.Driver=="postgres" {
  conn,err:=pgx.Connect(context.Background(),c.Database.DSN);if err!=nil {os.Exit(46)}
  defer conn.Close(context.Background())
  _,err=conn.Exec(context.Background(),"CREATE TABLE IF NOT EXISTS gy_default.ci_after_prepare (user_id bigint PRIMARY KEY REFERENCES gy_default.users(id)); INSERT INTO gy_default.ci_after_prepare SELECT id FROM gy_default.users ON CONFLICT DO NOTHING; UPDATE gy_default.users SET username=username||'-ci-fault' WHERE username NOT LIKE '%-ci-fault'; INSERT INTO gy_default.schema_migrations VALUES ('999_ci_fault.sql','ci-fault',1) ON CONFLICT DO NOTHING;")
  if err!=nil {os.Exit(47)}
 } else {
  db,err:=sql.Open("sqlite",filepath.Join(c.StateDir,"panel.db"));if err!=nil {os.Exit(48)}
  defer db.Close();_,err=db.Exec("CREATE TABLE IF NOT EXISTS ci_after_prepare (marker TEXT); INSERT INTO ci_after_prepare VALUES ('fault');")
  if err!=nil {os.Exit(49)}
 }
 if os.WriteFile(filepath.Join(c.StateDir,"ci-prepare-fault.json"),[]byte(`{"prepare_completed":true,"own_schema_mutated":true}`),0600)!=nil {os.Exit(50)}
 os.Exit(42)
}
''')
        result = subprocess.run([go, 'build', '-trimpath', '-ldflags=-s -w', '-o', str(context / 'fault-panel'), './cmd/ci-upgrade-fault'], cwd=sources, env=environment, capture_output=True, timeout=300)
        if result.returncode: raise AssertionError('Committed prepare-fault helper compilation failed')
        shutil.copyfile(context / 'fault-panel', binary)
        extra = '\nCOPY --chmod=0755 guangyue /opt/guangyue-personal/bin/ci-good-panel\nCOPY --chmod=0755 fault-panel /opt/guangyue-personal/bin/guangyue\n'
    else:
        extra = '\nCOPY --chmod=0755 guangyue /opt/guangyue-personal/bin/guangyue\n'
    base_tag = 'guangyue-panel-ci-base:' + uuid.uuid4().hex
    tag = 'guangyue-panel-ci-only:' + uuid.uuid4().hex
    run('docker', 'tag', manifest['images']['app']['id'], base_tag)
    (context / 'Dockerfile').write_text('FROM ' + base_tag + '\nLABEL org.opencontainers.image.version="' + target + '"\nCOPY VERSION /opt/guangyue-personal/VERSION\n' + extra)
    run('docker', 'build', '--platform', 'linux/amd64', '--pull=false', '-t', tag, str(context), timeout=300)
    image = run('docker', 'image', 'inspect', '--format', '{{.Id}}', tag).stdout.decode().strip()
    assert re.fullmatch('sha256:[a-f0-9]{64}', image)
    assets = work / ('synthetic-assets-' + target); assets.mkdir(mode=0o700)
    value = json.loads(json.dumps(manifest)); value['version'] = target; value['images']['app'] = {'id': image}
    value['archive'] = 'guangyue-panel-docker-' + target + '-linux-amd64.tar.gz'
    archive = assets / value['archive']
    with (assets / 'images.tar').open('wb') as output:
        result = subprocess.run(['docker', 'save', *[v['id'] for v in value['images'].values()]], stdout=output, stderr=subprocess.PIPE, timeout=300)
    if result.returncode: raise AssertionError('Synthetic image archive failed')
    with archive.open('wb') as output:
        result = subprocess.run(['gzip', '-n', '-c', str(assets / 'images.tar')], stdout=output, stderr=subprocess.PIPE, timeout=300)
    if result.returncode: raise AssertionError('Synthetic image compression failed')
    (assets / 'images.tar').unlink()
    value['sha256'] = fingerprint(archive)
    (assets / 'DOCKER_IMAGES.json').write_text(json.dumps(value)); (assets / 'DOCKER_IMAGES.json').chmod(0o600)
    (bundle / 'VERSION').write_text(target + '\n')
    (bundle / 'CI_SYNTHETIC_VERSION').write_text('Never publish this disposable test fixture.\n')
    rows = [fingerprint(p) + '  ' + p.relative_to(bundle).as_posix() for p in sorted(bundle.rglob('*')) if p.is_file() and p.relative_to(bundle).as_posix() != 'SHA256SUMS']
    (bundle / 'SHA256SUMS').write_text('\n'.join(rows) + '\n')
    return bundle, assets


def main():
    if os.geteuid() != 0 or os.environ.get('GITHUB_ACTIONS') != 'true':
        raise SystemExit('Only disposable GitHub Actions runners are supported.')
    if len(sys.argv) != 3:
        raise SystemExit('Usage: docker_integration.py native-artifacts docker-artifacts')
    os.environ['GY_DOCKER_INTEGRATION'] = '1'
    edition = os.environ.get('GY_DOCKER_INTEGRATION_EDITION', '')
    if edition not in ('lite', 'pro'):
        raise SystemExit('Select an explicit Lite or Pro fixture.')
    native, docker_assets = map(lambda p: Path(p).resolve(), sys.argv[1:])
    version = (Path(__file__).resolve().parents[2] / 'VERSION').read_text().strip()
    # Execute administrative code only from the exact checked, root-owned
    # artifact; the checkout supplies this disposable test harness alone.
    sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
    import oneclick
    rows = oneclick.manifest_rows((native / 'SHA256SUMS').read_text())
    name = oneclick.asset_name(version, edition)
    if rows.get(name) != fingerprint(native / name):
        raise AssertionError('Native artifact checksum mismatch')
    temporary = Path(tempfile.mkdtemp(prefix='guangyue-docker-ci-', dir='/root'))
    temporary.chmod(0o700)
    bundle = temporary / 'release'
    oneclick.safe_extract(native / name, bundle, name.removesuffix('.tar.gz'))
    # Drop the checkout import before selecting its same-version checked module.
    sys.modules.pop('oneclick', None)
    sys.path.insert(0, str(bundle / 'deploy'))
    import oneclick
    import docker_deploy
    oneclick.verify_bundle(bundle, {'version': version, 'edition': edition})
    docker_rows = oneclick.manifest_rows((docker_assets / 'SHA256SUMS').read_text())
    for name, digest in docker_rows.items():
        if fingerprint(docker_assets / name) != digest:
            raise AssertionError('Docker artifact checksum mismatch')

    # Existing HTTP service remains usable and its configuration/identity stay
    # unchanged throughout install, receipt replay, recovery and core restarts.
    nginx_files = {str(p): fingerprint(p) for p in Path('/etc/nginx').rglob('*') if p.is_file()}
    nginx_pid = run('systemctl', 'show', 'nginx', '--property=MainPID', '--value').stdout.strip()
    no_proxy = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with no_proxy.open('http://127.0.0.1/', timeout=5) as response:
        nginx_body = response.read()
        assert response.status == 200
    # Refuse both occupied protocols without disturbing the occupying process.
    for kind in (socket.SOCK_STREAM, socket.SOCK_DGRAM):
        with socket.socket(socket.AF_INET, kind) as occupied:
            occupied.bind(('0.0.0.0', 443))
            if kind == socket.SOCK_STREAM:
                occupied.listen(1)
            try:
                oneclick.port_preflight()
            except ValueError:
                pass
            else:
                raise AssertionError('Occupied public 443 was accepted')
            assert occupied.fileno() >= 0
    print('PASS TCP/UDP 443 occupied: installer refuses without stopping the owner', flush=True)

    cert, key = temporary / 'fullchain.pem', temporary / 'privkey.pem'
    def new_certificate():
        run('openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '3', '-subj', '/CN=panel.example.com', '-addext', 'subjectAltName=DNS:panel.example.com,DNS:node.example.com', '-keyout', str(key), '-out', str(cert))
        cert.chmod(0o600); key.chmod(0o600)
    new_certificate()
    plan = {'schema': 1, 'id': str(uuid.uuid4()), 'version': version, 'edition': edition, 'backend': 'docker', 'panel_domain': 'panel.example.com', 'node_domain': 'node.example.com', 'reality_sni': 'www.cloudflare.com', 'site_id': 'default', 'acme_agreed': True, 'acme_email': '', 'public_ips': ['8.0.0.1'], 'created_at': int(time.time())}
    oneclick.validate_plan(plan)
    install_arguments = dict(test_assets_dir=docker_assets, cert_file=cert, key_file=key)
    original_health = docker_deploy.health
    original_image_check = docker_deploy.image_check
    parent_traversal_verified = False
    def verify_dynamic_uid_image_parent(manifest):
        nonlocal parent_traversal_verified
        original_image_check(manifest)
        if not parent_traversal_verified:
            # The gateway binds only child directories below this image-owned
            # parent. An arbitrary host UID must search it, without making the
            # placeholder writable or exposing bound private child contents.
            run('docker', 'run', '--rm', '--network', 'none', '--read-only',
                '--user', '12345:12345', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges',
                '--tmpfs', '/var/lib/postgresql/data:rw,noexec,nosuid,size=1m',
                '--entrypoint', '/bin/sh', manifest['images']['app']['id'], '-c',
                'test -x /var/lib/guangyue && ! test -w /var/lib/guangyue')
            parent_traversal_verified = True
    activation_failure_injected = False
    def fail_after_real_activation(*args, **kwargs):
        nonlocal activation_failure_injected
        original_health(*args, **kwargs)
        activation_failure_injected = True
        raise ValueError('CI activation reply interrupted')
    try:
        with patch.object(docker_deploy, 'image_check', side_effect=verify_dynamic_uid_image_parent), patch.object(docker_deploy, 'health', side_effect=fail_after_real_activation):
            docker_deploy.install(plan, bundle, **install_arguments)
    except ValueError:
        if not activation_failure_injected:
            raise
    else:
        raise AssertionError('Interrupted activation fixture did not reach failure recovery')
    state = Path('/var/lib/guangyue')
    seed_before, owner_before = fingerprint(state / 'master.key'), fingerprint(state / 'initial-owner.json')
    assert parent_traversal_verified
    result = docker_deploy.install(plan, bundle, **install_arguments)
    assert result['stage'] == 'installed' and result['version'] == version
    assert fingerprint(state / 'master.key') == seed_before and fingerprint(state / 'initial-owner.json') == owner_before
    # An identical replay is idempotent; it does not generate another wallet
    # seed, administrator, Compose project or database password.
    docker_deploy.install(plan, bundle, **install_arguments)
    marker = docker_deploy.load()
    assert marker['uid'] > 0 and marker['gid'] > 0
    for private in (state, state / 'gateway', state / 'tls', docker_deploy.ROOT / 'reality'):
        info = private.stat()
        assert info.st_uid == marker['uid'] and stat.S_IMODE(info.st_mode) == 0o700, 'bound secret directory lost its private host ownership/mode'
    assert not marker['public_udp_verified'], 'local-only health must not claim external reachability'
    print('PASS real ' + edition + ' Docker install and failed-activation resume preserve private identity', flush=True)

    hosts_marker = '\n# guangyue-docker-ci ' + plan['id'] + '\n127.0.0.1 panel.example.com node.example.com\n'
    with open('/etc/hosts', 'a') as output:
        output.write(hosts_marker)
    clients, client_logs, servers = [], [], []
    try:
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=str(cert))))
        def api(path, data=None, cookie='', method=None, bearer=''):
            headers = {'X-Requested-With': 'guangyue', 'Content-Type': 'application/json'}
            if cookie: headers['Cookie'] = cookie
            if bearer: headers['Authorization'] = 'Bearer ' + bearer
            request = urllib.request.Request('https://panel.example.com' + path, data=json.dumps(data).encode() if data is not None else None, headers=headers, method=method)
            with opener.open(request, timeout=65) as response:
                return json.load(response), response.headers.get('Set-Cookie', '').split(';')[0]
        health, _ = api('/api/health')
        assert health['version'] == version and health['edition'] == edition
        initial = json.loads((state / 'initial-owner.json').read_text())
        _, cookie = api('/api/login', initial)
        def call(path, body=None): return api('/api' + path, body, cookie)[0]
        wait_for(lambda: call('/operations')['system']['status'] == 'applied')
        assert call('/runtime-settings')['service_wrappers_verified']
        services = wait_for(lambda: next((s for s in call('/state')['system']['services'] if s['name'] == 'HTTPS入口' and s['active']), None))
        assert services['checked_at'] > 0
        catalog = call('/subscription')
        assert {n['protocol'] for n in catalog['nodes']} == {'vless', 'hy2'}
        assert all(urlparse(n['uri']).port == 443 for n in catalog['nodes'])
        panel_id = docker_deploy.compose('ps', '-q', 'panel').strip()
        gateway_id = docker_deploy.compose('ps', '-q', 'gateway').strip()
        assert re.fullmatch('[a-f0-9]{64}', panel_id) and re.fullmatch('[a-f0-9]{64}', gateway_id)
        items = json.loads(run('docker', 'inspect', panel_id, gateway_id).stdout)
        for item in items:
            assert item['Config']['User'] == str(marker['uid']) + ':' + str(marker['gid'])
            assert item['HostConfig']['Privileged'] is False and item['HostConfig']['ReadonlyRootfs'] is True
            assert not any(m['Source'] == '/var/run/docker.sock' or m['Destination'] == '/var/run/docker.sock' for m in item['Mounts'])
        assert not items[0]['HostConfig']['PortBindings']
        assert set(items[1]['HostConfig']['PortBindings']) == {'443/tcp', '443/udp'}
        networks = items[1]['NetworkSettings']['Networks']
        bridge_host = next(v['Gateway'] for v in networks.values() if v.get('Gateway'))
        if edition == 'pro':
            for service in ('postgres', 'redis'):
                own_id = docker_deploy.compose('ps', '-q', service).strip()
                item = json.loads(run('docker', 'inspect', own_id).stdout)[0]
                assert not item['HostConfig']['PortBindings'], 'database was exposed on the host'

        class Echo(socketserver.BaseRequestHandler):
            def handle(self):
                while data := self.request.recv(65536): self.request.sendall(data)
        class UDPEcho(socketserver.BaseRequestHandler):
            def handle(self):
                data, sock = self.request; sock.sendto(data, self.client_address)
        tcp = socketserver.ThreadingTCPServer(('0.0.0.0', 0), Echo); tcp.daemon_threads = True
        udp = socketserver.ThreadingUDPServer(('0.0.0.0', 0), UDPEcho); udp.daemon_threads = True
        for server in (tcp, udp):
            threading.Thread(target=server.serve_forever, daemon=True).start(); servers.append(server)
        def receive(sock, count):
            data = b''
            while len(data) < count:
                part = sock.recv(count - len(data))
                if not part: raise OSError('proxy stream closed')
                data += part
            return data
        def socks(port, command, target_port=0):
            sock = socket.create_connection(('127.0.0.1', port), 3); sock.settimeout(5)
            try:
                sock.sendall(b'\x05\x01\x00'); assert receive(sock, 2) == b'\x05\x00'
                sock.sendall(bytes([5, command, 0, 1]) + socket.inet_aton(bridge_host) + struct.pack('!H', target_port))
                head = receive(sock, 4)
                if head[1] != 0: raise OSError('proxy refused connection')
                if head[3] == 1: host = socket.inet_ntoa(receive(sock, 4))
                elif head[3] == 3: host = receive(sock, receive(sock, 1)[0]).decode()
                else: host = socket.inet_ntop(socket.AF_INET6, receive(sock, 16))
                relay = struct.unpack('!H', receive(sock, 2))[0]
                return sock, ('127.0.0.1' if host in ('0.0.0.0', '::') else host, relay)
            except BaseException:
                sock.close(); raise
        def roundtrip(port):
            payload = secrets.token_bytes(32768)
            stream, _ = socks(port, 1, tcp.server_address[1])
            with stream:
                stream.sendall(payload); assert receive(stream, len(payload)) == payload
            control, relay = socks(port, 3)
            with control, socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as datagram:
                datagram.settimeout(8)
                packet = b'\0\0\0\x01' + socket.inet_aton(bridge_host) + struct.pack('!H', udp.server_address[1]) + payload[:256]
                datagram.sendto(packet, relay); reply, _ = datagram.recvfrom(4096)
                assert reply.endswith(payload[:256]), 'real core UDP echo mismatch'
        def recovered(port):
            wait_for(lambda: (roundtrip(port), True)[1], timeout=65)
        def launch(nodes, base_port):
            mapping = {}
            for index, node in enumerate(nodes):
                uri = urlparse(node['uri']); query = parse_qs(uri.query); port = base_port + index
                mapping[node['protocol']] = port
                if node['protocol'] == 'vless':
                    config = {'log': {'loglevel': 'none'}, 'inbounds': [{'listen': '127.0.0.1', 'port': port, 'protocol': 'socks', 'settings': {'auth': 'noauth', 'udp': True}}], 'outbounds': [{'protocol': 'vless', 'settings': {'vnext': [{'address': '127.0.0.1', 'port': 443, 'users': [{'id': uri.username, 'encryption': 'none', 'flow': 'xtls-rprx-vision'}]}]}, 'streamSettings': {'network': 'tcp', 'security': 'reality', 'realitySettings': {'fingerprint': 'chrome', 'serverName': query['sni'][0], 'publicKey': query['pbk'][0], 'shortId': query['sid'][0]}}}]}
                    args = [str(bundle / 'bin/xray-linux-amd64'), 'run', '-c']
                else:
                    config = {'server': '127.0.0.1:443', 'auth': unquote(uri.username), 'tls': {'sni': query['sni'][0], 'ca': str(cert)}, 'socks5': {'listen': '127.0.0.1:' + str(port)}}
                    args = [str(bundle / 'bin/hysteria-node-linux-amd64'), 'client', '--disable-update-check', '-c']
                path = temporary / ('client-' + str(port) + '.json'); path.write_text(json.dumps(config)); path.chmod(0o600)
                log = (temporary / ('client-' + str(port) + '.log')).open('wb'); client_logs.append(log)
                clients.append(subprocess.Popen([*args, str(path)], stdout=log, stderr=log))
            return mapping
        ports = launch(catalog['nodes'], 19891)
        for port in ports.values(): recovered(port)
        print('PASS published TCP/UDP 443: HTTPS, real VLESS Reality and HY2 TCP+UDP echo', flush=True)

        def generations():
            return {name: json.loads(run('docker', 'exec', panel_id, 'cat', '/run/guangyue-runtime/' + name + '.json').stdout) for name in ('panel', 'xray', 'hy2')}
        before = generations()
        pid = before['xray']['pid']; assert type(pid) is int and pid > 1
        run('docker', 'exec', panel_id, '/bin/sh', '-c', 'kill -KILL "$1"', 'guangyue-ci', str(pid))
        after = wait_for(lambda: (v if v['xray'] != before['xray'] else None) if (v := generations()) else None)
        assert after['panel'] == before['panel'] and after['hy2'] == before['hy2']
        for port in ports.values(): recovered(port)
        print('PASS fixed core supervisor: crash gives new Xray generation without restarting panel/HY2', flush=True)

        # Mount sync uses the production restart/PID adapter and independent
        # delegated credentials, then proves both protocols are revoked.
        token = call('/fleet/tokens', {'name': 'CI mounted master', 'scope': 'manage', 'forever': True})['token']
        def fleet(method, path, body=None):
            deadline = time.monotonic() + 65
            while True:
                request = {'method': method, 'path': path}
                if body is not None: request['body'] = body
                result, _ = api('/api/fleet-gateway', request, bearer=token)
                if result['status'] == 200: return result['body']
                if result['status'] != 409 or time.monotonic() >= deadline: raise AssertionError('delegated mount policy did not apply')
                time.sleep(1)
        pool = fleet('GET', '/api/node-pool'); info = pool['info']
        mount = {'master_id': str(uuid.uuid4()), 'sequence': 1, 'catalog_revision': pool['revision'], 'users': [{'user_id': 10001, 'node_ids': ['hy2-main', 'vless-main'], 'generation': secrets.token_hex(32), 'period_id': 'ci-docker-mount', 'quota': 1 << 30, 'expires': 0, 'vless': True, 'hy2': True}]}
        credentials = fleet('POST', '/api/node-mounts/sync', mount)['accounts'][0]['credentials']
        mounted = [{'protocol': 'vless', 'uri': 'vless://' + credentials['vless']['vless-main'] + '@' + info['vless_host'] + ':443?' + urlencode({'sni': info['reality_sni'], 'pbk': info['reality_public'], 'sid': info['short_id']})}, {'protocol': 'hy2', 'uri': 'hysteria2://' + quote(credentials['hy2'], safe='') + '@' + info['hy2_host'] + ':443?' + urlencode({'sni': info['hy2_host']})}]
        mounted_ports = launch(mounted, 19901)
        for port in mounted_ports.values(): recovered(port)
        mount['sequence'] += 1; mount['users'] = []
        fleet('POST', '/api/node-mounts/sync', mount)
        for port in mounted_ports.values():
            try: roundtrip(port)
            except OSError: pass
            else: raise AssertionError('saved delegated link survived unmount')
        for port in ports.values(): recovered(port)
        print('PASS real token-mounted VLESS/HY2 allocation, revocation and local autonomy', flush=True)

        # Only local CI fixtures get synthetic versions. The same production
        # upgrade/rollback methods and real Docker images execute the changes.
        major, minor, patch_number = map(int, version.split('.'))
        next_version = f'{major}.{minor}.{patch_number + 1}'
        fault_version = f'{major}.{minor}.{patch_number + 2}'
        image_manifest = json.loads((docker_assets / 'DOCKER_IMAGES.json').read_text())
        print('CI ONLY: synthetic versions/images remain local; no registry push or Release is performed.', flush=True)
        next_bundle, next_assets = synthetic_upgrade(bundle, temporary, next_version, image_manifest)
        def verify_provisional_health(*args, **kwargs):
            original_health(*args, **kwargs)
            # Exercise the real persistent host flag mounted read-only into
            # both services. New financial/data workers must stay unstarted
            # until the updater commits, even though the new app is healthy.
            assert Path('/var/lib/guangyue-maintenance/pending').is_file()
            provisional = wait_for(lambda: (v if v.get('maintenance') and v.get('background_frozen') else None) if (v := api('/api/health')[0]) else None)
            assert provisional['background_frozen']
            try:
                api('/api/login', initial)
            except urllib.error.HTTPError as failure:
                assert failure.code == 503
            else:
                raise AssertionError('Provisional image accepted a mutating request before update commit')
        with patch.object(docker_deploy, 'health', side_effect=verify_provisional_health):
            docker_deploy.upgrade(next_bundle, test_assets_dir=next_assets)
        assert docker_deploy.load()['version'] == next_version and api('/api/health')[0]['version'] == next_version
        assert not Path('/var/lib/guangyue-maintenance/pending').exists()
        assert not api('/api/health')[0]['maintenance']
        # The old authenticated session and proxy credentials survive update.
        _, cookie = api('/api/login', initial)
        catalog_after = call('/subscription')
        assert sorted(n['uri'] for n in catalog_after['nodes']) == sorted(n['uri'] for n in catalog['nodes'])
        for port in ports.values(): recovered(port)
        assert fingerprint(state / 'master.key') == seed_before
        newly_created = call('/users', {'username': 'ci-after-docker-update', 'password': secrets.token_urlsafe(24), 'enabled': True, 'vless': True, 'hy2': True})
        signature = docker_deploy.schema(docker_deploy.read_config())
        # A fault after real prepare + committed own-schema changes must restore
        # database/state and keep the last healthy image, not acknowledge success.
        fault_bundle, fault_assets = synthetic_upgrade(bundle, temporary, fault_version, image_manifest, fail_after_prepare=True)
        try:
            docker_deploy.upgrade(fault_bundle, test_assets_dir=fault_assets)
        except (ValueError, OSError, subprocess.SubprocessError):
            pass
        else:
            raise AssertionError('Post-prepare image failure was incorrectly acknowledged')
        assert docker_deploy.load()['version'] == next_version
        assert docker_deploy.schema(docker_deploy.read_config()) == signature
        assert api('/api/health')[0]['version'] == next_version
        markers = list(Path('/root/guangyue-backups').glob('docker-upgrade-*/failed-state/ci-prepare-fault.json'))
        assert any(json.loads(path.read_text()) == {'prepare_completed': True, 'own_schema_mutated': True} for path in markers), 'fault did not execute after real database preparation'
        if edition == 'pro':
            assert docker_deploy.pg_sql(docker_deploy.load(), "SELECT to_regclass('gy_default.ci_after_prepare') IS NULL;").strip() == 't'
        else:
            import sqlite3
            connection = sqlite3.connect('file:' + str(state / 'panel.db') + '?mode=ro', uri=True)
            try: assert connection.execute("SELECT count(*) FROM sqlite_master WHERE name='ci_after_prepare'").fetchone()[0] == 0
            finally: connection.close()
        _, cookie = api('/api/login', initial)
        assert any(u['id'] == newly_created['id'] and u['username'] == 'ci-after-docker-update' for u in call('/state')['users'])
        print('PASS real Docker upgrade and post-prepare failure restore application, schema and private state', flush=True)
        available = next(row for row in docker_deploy.candidates() if row['version'] == version)
        assert available['compatible']
        docker_deploy.rollback(version)
        assert docker_deploy.load()['version'] == version and api('/api/health')[0]['version'] == version
        _, cookie = api('/api/login', initial)
        assert any(u['id'] == newly_created['id'] for u in call('/state')['users']), 'compatible rollback removed a later user'
        assert fingerprint(state / 'master.key') == seed_before
        assert sorted(n['uri'] for n in call('/subscription')['nodes']) == sorted(n['uri'] for n in catalog['nodes'])
        for port in ports.values(): recovered(port)
        assert docker_deploy.load()['floor'] == version
        print('PASS compatible Docker rollback preserves later users, subscriptions, seed, TLS and installation floor', flush=True)

        assert run('systemctl', 'show', 'nginx', '--property=MainPID', '--value').stdout.strip() == nginx_pid
        assert {str(p): fingerprint(p) for p in Path('/etc/nginx').rglob('*') if p.is_file()} == nginx_files
        with no_proxy.open('http://127.0.0.1/', timeout=5) as response: assert response.read() == nginx_body
        assert fingerprint(state / 'master.key') == seed_before
        print('PASS existing Nginx port 80 and private seeds preserved; databases remain internal', flush=True)
    finally:
        for process in clients:
            process.terminate()
            try: process.wait(timeout=5)
            except subprocess.TimeoutExpired: process.kill(); process.wait()
        for log in client_logs: log.close()
        for server in servers: server.shutdown(); server.server_close()
        # Only this receipt's exact project is stopped; no daemon, image,
        # unrelated container, shared package or Nginx configuration is removed.
        try: docker_deploy.compose('stop', '--timeout', '35')
        except ValueError: pass
        value = Path('/etc/hosts').read_text()
        if hosts_marker in value: Path('/etc/hosts').write_text(value.replace(hosts_marker, '', 1))
        shutil.rmtree(temporary)


if __name__ == '__main__':
    main()
