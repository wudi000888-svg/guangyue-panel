"""Real proxy accounting and revocation, called only by the disposable CI install."""
import json
import secrets
import socket
import socketserver
import struct
import subprocess
import threading
import time
from urllib.parse import urlparse, parse_qs, unquote


def verify_entitlements(bundle, temp, cert, api, cookie):
    def call(path, data=None):
        return api('/api' + path, data, cookie)[0]

    class Echo(socketserver.BaseRequestHandler):
        def handle(self):
            while True:
                data = self.request.recv(65536)
                if not data:
                    return
                self.request.sendall(data)

    class UDPEcho(socketserver.BaseRequestHandler):
        def handle(self):
            data, sock = self.request
            sock.sendto(data, self.client_address)

    tcp = socketserver.ThreadingTCPServer(('127.0.0.1', 0), Echo)
    tcp.daemon_threads = True
    udp = socketserver.ThreadingUDPServer(('127.0.0.1', 0), UDPEcho)
    udp.daemon_threads = True
    for server in [tcp, udp]:
        threading.Thread(target=server.serve_forever, daemon=True).start()
    processes, files, held = [], [], []

    def receive(sock, count):
        data = b''
        while len(data) < count:
            part = sock.recv(count - len(data))
            if not part:
                raise OSError('proxy stream closed')
            data += part
        return data

    def socks(port, command, target_port=0):
        sock = socket.create_connection(('127.0.0.1', port), 3)
        sock.settimeout(4)
        try:
            sock.sendall(b'\x05\x01\x00')
            assert receive(sock, 2) == b'\x05\x00'
            sock.sendall(bytes([5, command, 0, 1]) + socket.inet_aton('127.0.0.1') + struct.pack('!H', target_port))
            head = receive(sock, 4)
            if head[1] != 0:
                raise OSError('proxy denied connection')
            if head[3] == 1:
                host = socket.inet_ntoa(receive(sock, 4))
            elif head[3] == 3:
                host = receive(sock, receive(sock, 1)[0]).decode()
            else:
                host = socket.inet_ntop(socket.AF_INET6, receive(sock, 16))
            port = struct.unpack('!H', receive(sock, 2))[0]
            return sock, (host, port)
        except BaseException:
            sock.close()
            raise

    def roundtrip(port):
        payload = secrets.token_bytes(65536)
        sock, _ = socks(port, 1, tcp.server_address[1])
        with sock:
            sock.sendall(payload)
            assert receive(sock, len(payload)) == payload
        control, address = socks(port, 3)
        with control, socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as datagram:
            datagram.settimeout(5)
            packet = b'\0\0\0\x01' + socket.inet_aton('127.0.0.1') + struct.pack('!H', udp.server_address[1]) + payload[:256]
            datagram.sendto(packet, address)
            response, _ = datagram.recvfrom(4096)
            assert response.endswith(payload[:256]), 'UDP echo mismatch'

    def wait_applied():
        deadline = time.monotonic() + 45
        while time.monotonic() < deadline:
            status = call('/operations')['system']
            if status['status'] == 'applied' and status['configuration']['state'] == 'applied':
                return
            time.sleep(1)
        raise AssertionError('entitlement configuration did not apply')

    group = call('/node-groups', {'name': 'CI entitlement', 'scope': 'private', 'enabled': True})
    plan = call('/plans', {'name': 'CI plan', 'quota': 1 << 30, 'valid_days': 1, 'cycle': 'none', 'vless': True, 'hy2': True, 'group_ids': [group['id']]})
    user = call('/users', {'username': 'ci-entitlement', 'password': secrets.token_urlsafe(24), 'enabled': True, 'vless': True, 'hy2': True, 'plan_id': plan['id']})
    ids = ['vless-main', 'hy2-main']
    call('/nodes/policy', {'ids': ids, 'group_ids': ['legacy-private', group['id']]})
    wait_applied()
    catalog = call('/subscription?user_id=' + str(user['id']))
    assert len(catalog['nodes']) == 2, 'plan access did not reach subscription'
    try:
        for index, node in enumerate(catalog['nodes']):
            uri = urlparse(node['uri'])
            query = parse_qs(uri.query)
            port = 19891 + index
            if node['protocol'] == 'vless':
                config = {'log': {'loglevel': 'none'}, 'inbounds': [{'listen': '127.0.0.1', 'port': port, 'protocol': 'socks', 'settings': {'auth': 'noauth', 'udp': True}}], 'outbounds': [{'protocol': 'vless', 'settings': {'vnext': [{'address': '127.0.0.1', 'port': 443, 'users': [{'id': uri.username, 'encryption': 'none', 'flow': 'xtls-rprx-vision'}]}]}, 'streamSettings': {'network': 'tcp', 'security': 'reality', 'realitySettings': {'fingerprint': 'chrome', 'serverName': query['sni'][0], 'publicKey': query['pbk'][0], 'shortId': query['sid'][0]}}}]}
                args = [str(bundle / 'bin/xray-linux-amd64'), 'run', '-c']
            else:
                config = {'server': '127.0.0.1:443', 'auth': unquote(uri.username), 'tls': {'sni': query['sni'][0], 'ca': str(cert)}, 'socks5': {'listen': '127.0.0.1:' + str(port)}}
                args = [str(bundle / 'bin/hysteria-node-linux-amd64'), 'client', '--disable-update-check', '-c']
            path = temp / ('entitlement-client-' + str(index) + '.json')
            path.write_text(json.dumps(config))
            path.chmod(0o600)
            log = (temp / ('entitlement-client-' + str(index) + '.log')).open('wb')
            files.append(log)
            processes.append(subprocess.Popen([*args, str(path)], stdout=log, stderr=log))
        time.sleep(2)
        for rate in [0, 500, 1000, 1500, 2000]:
            call('/nodes/policy', {'ids': ids, 'rate_milli': rate})
            for port in [19891, 19892]:
                roundtrip(port)
            time.sleep(1)
            call('/nodes/policy', {'ids': ids, 'rate_milli': rate})  # settle without changing the revision
            report = call('/usage?user_id=' + str(user['id']))
            for direction in ['upload', 'download']:
                units = sum(row[direction] * row['rate_milli'] for row in report['nodes'])
                meter = report['user']['meter']
                assert units == meter[direction] * 1000 + meter[direction + '_remainder'], 'core weighted meter mismatch'
            for node in ids:
                assert any(row['node_id'] == node and row['rate_milli'] == rate and row['upload'] > 0 and row['download'] > 0 for row in report['nodes']), 'missing per-node/rate traffic'
        print('PASS real VLESS/HY2 TCP + UDP and 0/0.5/1/1.5/2 multipliers with exact residue accounting')

        for port in [19891, 19892]:
            sock, _ = socks(port, 1, tcp.server_address[1])
            sock.sendall(b'before')
            assert receive(sock, 6) == b'before'
            held.append(sock)
        group['enabled'] = False
        group = call('/node-groups', group)
        # Allow the regular 10-second reconciliation loop to revoke credentials.
        time.sleep(12)
        for sock in held:
            try:
                sock.sendall(b'after')
                survived = receive(sock, 5) == b'after'
            except OSError:
                survived = False
            assert not survived, 'revoked long connection remained usable'
        assert not call('/subscription?user_id=' + str(user['id']))['nodes']
        for port in [19891, 19892]:
            try:
                roundtrip(port)
                usable = True
            except (OSError, AssertionError):
                usable = False
            assert not usable, 'saved link bypassed revoked group'
        print('PASS group revocation closes existing VLESS/HY2 streams and denies saved links')

        for sock in held:
            sock.close()
        held.clear()
        group['enabled'] = True
        group = call('/node-groups', group)
        wait_applied()
        for port in [19891, 19892]:
            sock, _ = socks(port, 1, tcp.server_address[1])
            sock.sendall(b'old-period')
            assert receive(sock, 10) == b'old-period'
            held.append(sock)
        previous_period = call('/usage?user_id=' + str(user['id']))['user']['meter']['period_id']
        reset = {'ids': [user['id']], 'action': 'reset', 'operation_id': secrets.token_urlsafe(24)}
        preview = call('/entitlements/batch', dict(reset, preview=True))
        call('/entitlements/batch', dict(reset, expected=preview['expected']))
        deadline = time.monotonic() + 45
        while time.monotonic() < deadline:
            current = call('/usage?user_id=' + str(user['id']))
            if current['user']['meter']['period_id'] != previous_period and not current['user']['meter']['pending_reset']:
                break
            time.sleep(1)
        else:
            raise AssertionError('local quota reset did not settle')
        for sock in held:
            try:
                sock.sendall(b'new-period')
                survived = receive(sock, 10) == b'new-period'
            except OSError:
                survived = False
            assert not survived, 'old stream survived quota-period reset'
        assert current['quota_used'] == 0 and current['user']['upload'] > 0, 'reset erased lifetime usage'
        for port in [19891, 19892]:
            # A cached QUIC connection discovers an abrupt server restart on
            # its transport timeout. Keep the same client and verify recovery.
            deadline = time.monotonic() + 45
            while True:
                try:
                    roundtrip(port)
                    break
                except OSError:
                    if time.monotonic() >= deadline:
                        raise AssertionError('client did not reconnect after quota reset')
                    time.sleep(1)
        print('PASS local quota reset closes idle old VLESS/HY2 streams, preserves lifetime usage and admits new connections')

    finally:
        for sock in held:
            sock.close()
        for process in processes:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
        for log in files:
            log.close()
        for server in [tcp, udp]:
            server.shutdown()
            server.server_close()
        call('/nodes/policy', {'ids': ids, 'rate_milli': 1000, 'group_ids': ['legacy-private']})
