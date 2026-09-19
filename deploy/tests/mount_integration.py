"""Token-mounted identities against the real installed Reality and QUIC cores."""
import json
import re
import secrets
import subprocess
import time
import urllib.request
import uuid
from pathlib import Path
from urllib.parse import quote, urlencode


def verify_mounts(call, cookie, launch, roundtrip, socks, receive, echo_port, master_user, local_vless_port):
    base = 'http://127.0.0.1:19100'
    token = call('/fleet/tokens', {'name': 'CI mounted master', 'scope': 'manage', 'forever': True})
    local_users = call('/state')['users']

    def diagnostics():
        # Disposable runner only. Remove every credential-bearing scalar from
        # fixture configuration before reporting core startup diagnostics.
        redact = set()
        def values(value):
            if isinstance(value, dict):
                for item in value.values(): values(item)
            elif isinstance(value, list):
                for item in value: values(item)
            elif isinstance(value, str) and len(value) >= 8:
                redact.add(value)
        values(token)
        for name in ['/etc/guangyue-personal.json', '/var/lib/guangyue/xray.json', '/var/lib/guangyue/hy2.json']:
            try: values(json.loads(Path(name).read_text()))
            except (OSError, ValueError): pass
        messages = []
        try: messages.append(json.dumps(call('/operations')['system']))
        except OSError: messages.append('operations unavailable')
        for unit in ['guangyue-xray.service', 'guangyue-hy2.service']:
            messages.append(subprocess.run(['systemctl', 'show', unit, '--property=ActiveState,SubState,ExecMainStatus,NRestarts'], capture_output=True, text=True, timeout=10).stdout)
            journal = subprocess.run(['journalctl', '-u', unit, '-n', '35', '--no-pager', '-o', 'cat'], capture_output=True, text=True, timeout=10).stdout
            messages.extend(line for line in journal.splitlines() if re.search(r'error|failed|panic|status=', line, re.I))
        output = '\n'.join(messages)
        for secret in sorted(redact, key=len, reverse=True): output = output.replace(secret, '[fixture]')
        output = re.sub(r'(?:gyp_|Bearer\s+)\S+|[a-fA-F0-9]{8}-[a-fA-F0-9-]{27,}|[A-Za-z0-9_+/=-]{32,}', '[redacted]', output)
        print('Mount failure diagnostics: ' + output[-6000:])

    def raw(path, method, data=None, bearer=False):
        headers = {'Content-Type': 'application/json', 'X-Requested-With': 'guangyue'}
        if bearer:
            headers['Authorization'] = 'Bearer ' + token['token']
        else:
            headers['Cookie'] = cookie
        request = urllib.request.Request(base + path, method=method, headers=headers,
                                         data=json.dumps(data).encode() if data is not None else None)
        with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(request, timeout=60) as response:
            return json.load(response)

    def gateway(method, path, body=None):
        request = {'method': method, 'path': path}
        if body is not None:
            request['body'] = body
        # A durable policy can still be pending while systemd replaces a core.
        # Retry the identical sequence, just as a master with a lost reply does;
        # this must neither create another identity nor extend its access lease.
        retryable = {'子站挂载授权尚未应用，请检查核心状态',
                     '旧挂载连接尚未撤销，请重试', '子站流量采样失败，请重试'}
        deadline = time.monotonic() + 60
        while True:
            out = raw('/api/fleet-gateway', 'POST', request, True)
            if out['status'] == 200:
                return out['body']
            error = out.get('body', {}).get('error', '')
            pending = method == 'POST' and path == '/api/node-mounts/sync' and out['status'] == 409 and error in retryable
            if not pending or time.monotonic() >= deadline:
                # Only fixed server diagnostics may reach CI logs; never print
                # response bodies, which can contain delegated credentials.
                detail = error if error in retryable else 'unexpected response'
                diagnostics()
                raise AssertionError('mounted gateway status=' + str(out['status']) + ': ' + detail)
            time.sleep(1)

    catalog = gateway('GET', '/api/node-pool')
    request = {'master_id': str(uuid.uuid4()), 'sequence': 1, 'catalog_revision': catalog['revision'],
               'users': [{'user_id': master_user, 'node_ids': ['hy2-main', 'vless-main'],
                          'generation': secrets.token_hex(32), 'period_id': 'ci-mounted-period',
                          'quota': 1 << 30, 'expires': 0, 'vless': True, 'hy2': True}]}
    result = gateway('POST', '/api/node-mounts/sync', request)
    credentials = result['accounts'][0]['credentials']
    users = call('/state')['users']
    delegated = next(u for u in users if u.get('mount_access', {}).get('user_id') == master_user)
    assert delegated['id'] not in {u['id'] for u in local_users}, 'mount overwrote a local identity'
    info = catalog['info']
    vless = 'vless://' + credentials['vless']['vless-main'] + '@' + info['vless_host'] + ':443?' + urlencode({
        'sni': info['reality_sni'], 'pbk': info['reality_public'], 'sid': info['short_id']})
    hy2 = 'hysteria2://' + quote(credentials['hy2'], safe='') + '@' + info['hy2_host'] + ':443?' + urlencode({'sni': info['hy2_host']})
    launch([{'protocol': 'vless', 'uri': vless}, {'protocol': 'hy2', 'uri': hy2}], 19901)

    def recovered(port):
        deadline = time.monotonic() + 45
        while True:
            try:
                roundtrip(port)
                return
            except OSError:
                if time.monotonic() >= deadline:
                    raise AssertionError('proxy did not recover')
                time.sleep(1)

    def denied(port):
        try:
            roundtrip(port)
        except (OSError, AssertionError):
            return
        raise AssertionError('saved mounted link retained access')

    def held_streams():
        out = []
        for port in [19901, 19902]:
            stream, _ = socks(port, 1, echo_port)
            stream.sendall(b'before')
            assert receive(stream, 6) == b'before'
            out.append(stream)
        return out

    def assert_closed(streams):
        for stream in streams:
            try:
                stream.sendall(b'after')
                survived = receive(stream, 5) == b'after'
            except OSError:
                survived = False
            finally:
                stream.close()
            assert not survived, 'mounted established stream survived revocation'

    time.sleep(2)
    for port in [19901, 19902]:
        recovered(port)
    request['sequence'] += 1
    result = gateway('POST', '/api/node-mounts/sync', request)
    usage = result['usage'][str(master_user)]
    assert usage['vless'] > 0 and usage['hy2'] > 0, 'mounted traffic missing from assigned user'
    streams = held_streams()
    saved_users = request['users']
    request['users'] = []
    request['sequence'] += 1
    gateway('POST', '/api/node-mounts/sync', request)
    assert_closed(streams)
    for port in [19901, 19902]:
        denied(port)
    for port in [19891, 19892]:
        recovered(port)
    print('PASS token mount: real Reality/HY2 TCP+UDP, independent IDs, accounting, unmount and local autonomy')

    request['users'] = saved_users
    request['sequence'] += 1
    gateway('POST', '/api/node-mounts/sync', request)
    for port in [19901, 19902]:
        recovered(port)
    streams = held_streams()
    # Exercise the expired-lease boundary without spending five minutes idle.
    # The production wrapper is untouched; only disposable fixture metadata ages.
    subprocess.run(['systemctl', 'stop', 'guangyue.service'], check=True)
    try:
        path = Path('/var/lib/guangyue/mount-leases.json')
        leases = json.loads(path.read_text())
        leases['users'][str(delegated['id'])] = int(time.time()) - 10
        path.write_text(json.dumps(leases))
        time.sleep(20)
        assert_closed(streams)
        for port in [19901, 19902]:
            denied(port)
        # Reality local accounts continue to work while the panel is stopped.
        recovered(local_vless_port)
    finally:
        subprocess.run(['systemctl', 'start', 'guangyue.service'], check=True)
    deadline = time.monotonic() + 45
    while True:
        try:
            call('/operations')
            break
        except OSError:
            if time.monotonic() >= deadline:
                raise
            time.sleep(1)
    request['sequence'] += 1
    gateway('POST', '/api/node-mounts/sync', request)
    for port in [19901, 19902, 19891, 19892]:
        recovered(port)
    streams = held_streams()
    deadline = time.monotonic() + 60
    while raw('/api/fleet/tokens/' + token['id'], 'DELETE').get('pending'):
        assert time.monotonic() < deadline, 'token revocation did not reach the cores'
        time.sleep(1)
    assert_closed(streams)
    for port in [19901, 19902]:
        denied(port)
    print('PASS independent core lease guard with stopped panel, local Reality continuity, token revocation and reconnect')
