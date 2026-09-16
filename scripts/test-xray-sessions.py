#!/usr/bin/env python3
"""Real VLESS TCP sessions from the same loopback IP, using synthetic identities."""
import concurrent.futures
import json
import socket
import subprocess
import sys
import tempfile
import threading
import time
import uuid
from pathlib import Path

binary = str(Path(sys.argv[1]).resolve())

def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]

api_port, vless_port = port(), port()
alice, bob = str(uuid.uuid4()), str(uuid.uuid4())
server = socket.socket()
server.bind(('127.0.0.1', 0))
server.listen()
echo_port = server.getsockname()[1]

def echo(conn):
    with conn:
        while data := conn.recv(4096):
            conn.sendall(data)

def accept():
    while True:
        try: conn, _ = server.accept()
        except OSError: return
        threading.Thread(target=echo, args=(conn,), daemon=True).start()

threading.Thread(target=accept, daemon=True).start()
config = {
    'log': {'loglevel': 'none'}, 'stats': {},
    'api': {'tag': 'api', 'listen': f'127.0.0.1:{api_port}', 'services': ['StatsService']},
    'policy': {'levels': {'0': {'statsUserUplink': True, 'statsUserDownlink': True}}},
    'inbounds': [{'listen': '127.0.0.1', 'port': vless_port, 'protocol': 'vless',
                  'settings': {'decryption': 'none', 'clients': [
                      {'id': alice, 'email': 'alice'}, {'id': bob, 'email': 'bob'}]}}],
    'outbounds': [{'protocol': 'freedom'}],
}

def stats():
    raw = subprocess.check_output([binary, 'api', 'statsquery', f'--server=127.0.0.1:{api_port}', '--timeout=1'], stderr=subprocess.DEVNULL)
    return {s['name']: int(s.get('value', 0)) for s in json.loads(raw).get('stat', [])}

def expect(a, b):
    deadline = time.monotonic() + 8
    while time.monotonic() < deadline:
        try:
            values = stats()
            assert values['guangyue>>>monitor>>>version'] == 1
            assert values.get('user>>>alice>>>connections', 0) == a
            assert values.get('user>>>bob>>>connections', 0) == b
            assert all(v >= 0 for v in values.values())
            return values
        except (AssertionError, KeyError, subprocess.CalledProcessError):
            time.sleep(.05)
    raise AssertionError(f'Expected sessions alice={a}, bob={b}: {stats()}')

def connect(identity):
    s = socket.create_connection(('127.0.0.1', vless_port), timeout=4)
    # VLESS v0, no addons, TCP command, IPv4 target. Real target echo confirms dispatch.
    s.sendall(b'\0' + uuid.UUID(identity).bytes + b'\0\1' + echo_port.to_bytes(2, 'big') + b'\1\x7f\0\0\1' + b'guangyue-fixture')
    data = b''
    while not data.endswith(b'guangyue-fixture'):
        piece = s.recv(4096)
        if not piece: raise AssertionError('VLESS closed before echo')
        data += piece
    return s

def socks_connect(socks_port):
    s = socket.create_connection(('127.0.0.1', socks_port), timeout=4)
    s.sendall(b'\x05\x01\x00')
    assert s.recv(2) == b'\x05\x00'
    s.sendall(b'\x05\x01\x00\x01\x7f\x00\x00\x01' + echo_port.to_bytes(2, 'big'))
    response = b''
    while len(response) < 10: response += s.recv(10-len(response))
    assert response[1] == 0
    s.sendall(b'mux-fixture')
    assert s.recv(11) == b'mux-fixture'
    return s

connections = []
client = None
with tempfile.TemporaryDirectory() as tmp:
    path = Path(tmp) / 'server.json'
    path.write_text(json.dumps(config))
    process = subprocess.Popen([binary, 'run', '-c', str(path)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        expect(0, 0)
        with concurrent.futures.ThreadPoolExecutor(max_workers=12) as pool:
            connections = list(pool.map(connect, [alice] * 8 + [bob] * 4))
        before = expect(8, 4)
        # Non-reset queries must never alter traffic accounting.
        assert expect(8, 4)['user>>>alice>>>traffic>>>uplink'] == before['user>>>alice>>>traffic>>>uplink']
        for s in connections[:3]: s.close()
        expect(5, 4)
        for s in connections: s.close()
        expect(0, 0)
        connections = [connect(alice)]
        expect(1, 0)
        connections[0].close()
        expect(0, 0)
        socks_port = port()
        client_config = {'log': {'loglevel': 'none'}, 'inbounds': [{'listen':'127.0.0.1','port':socks_port,'protocol':'socks'}], 'outbounds': [{'protocol':'vless','settings':{'vnext':[{'address':'127.0.0.1','port':vless_port,'users':[{'id':alice,'encryption':'none'}]}]},'mux':{'enabled':True,'concurrency':8}}]}
        client_path = Path(tmp) / 'client.json'
        client_path.write_text(json.dumps(client_config))
        client = subprocess.Popen([binary,'run','-c',str(client_path)],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        deadline = time.monotonic()+5
        while True:
            try:
                connections = [socks_connect(socks_port)]
                break
            except ConnectionRefusedError:
                if time.monotonic()>deadline: raise
                time.sleep(.05)
        connections.append(socks_connect(socks_port))
        expect(1,0)  # One authenticated carrier, not carrier plus two inner flows.
        client.terminate(); client.wait(timeout=5); client = None
        expect(0,0)
    finally:
        for s in connections: s.close()
        if client is not None: client.terminate(); client.wait(timeout=5)
        process.terminate()
        process.wait(timeout=5)
        server.close()
print('PASS: real VLESS concurrent sessions, same-IP counts, isolation, close/reconnect, mux carrier count, traffic preservation')
