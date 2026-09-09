#!/usr/bin/env python3
"""Validate and publish a matching certificate pair as one directory generation."""
import argparse
import hashlib
import json
import os
import pwd
import shutil
import socket
import ssl
import subprocess
import tempfile
import time
import uuid
from pathlib import Path


def run(*args, data=None):
    return subprocess.run(args, input=data, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True).stdout


def validate(cert, key, domains):
    run('openssl', 'x509', '-in', str(cert), '-noout', '-checkend', '86400')
    for host in domains:
        # Some OpenSSL releases print a non-match yet return success.
        result = run('openssl', 'x509', '-in', str(cert), '-noout', '-checkhost', host)
        if b'does NOT match' in result:
            raise ValueError('certificate does not cover the requested domain')
    pub = run('openssl', 'x509', '-in', str(cert), '-pubkey', '-noout')
    cert_pub = run('openssl', 'pkey', '-pubin', '-outform', 'DER', data=pub)
    key_pub = run('openssl', 'pkey', '-in', str(key), '-pubout', '-outform', 'DER')
    if cert_pub != key_pub:
        raise ValueError('certificate and private key do not match')


def publish(base, cert, key, uid=0, gid=0):
    base = Path(base)
    base.mkdir(parents=True, exist_ok=True, mode=0o700)
    generation = Path(tempfile.mkdtemp(prefix='cert-', dir=base))
    os.chown(generation, uid, gid)
    for name, source in [('fullchain.pem', cert), ('privkey.pem', key)]:
        dest = generation / name
        shutil.copyfile(source, dest)
        os.chmod(dest, 0o600)
        os.chown(dest, uid, gid)
    current = base / 'current'
    previous = os.readlink(current) if current.is_symlink() else None
    swap(base, generation.name)
    return previous


def swap(base, target):
    link = Path(base) / ('.pending-' + uuid.uuid4().hex)
    if target is None:
        (Path(base) / 'current').unlink(missing_ok=True)
        return
    link.symlink_to(target)
    os.replace(link, Path(base) / 'current')


def wait_served_certificate(cert, hostname, timeout=8):
    # Pin the entire expected leaf certificate on the local SNI entry. Trust-store
    # validation is not needed for this readiness check; an exact DER match is.
    expected = hashlib.sha256(run('openssl', 'x509', '-in', str(cert), '-outform', 'DER')).digest()
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
    context.check_hostname = False
    context.verify_mode = ssl.CERT_NONE
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with socket.create_connection(('127.0.0.1', 443), timeout=0.5) as connection:
                with context.wrap_socket(connection, server_hostname=hostname) as tls:
                    if hashlib.sha256(tls.getpeercert(binary_form=True)).digest() == expected:
                        return
        except (OSError, ssl.SSLError):
            pass
        time.sleep(0.1)
    raise ValueError('Nginx did not begin serving the new certificate')


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--initial', action='store_true')
    args = p.parse_args()
    meta = json.loads(Path('/etc/guangyue-certificate.json').read_text())
    cert, key = Path(meta['cert']), Path(meta['key'])
    lineage = os.environ.get('RENEWED_LINEAGE')
    if lineage and Path(lineage).resolve() != cert.parent.resolve():
        return
    validate(cert, key, meta['domains'])
    user = pwd.getpwnam('guangyue')
    old = []
    try:
        for base, uid, gid in [('/etc/nginx/guangyue-tls', 0, 0), ('/var/lib/guangyue/tls', user.pw_uid, user.pw_gid)]:
            old.append((base, publish(base, cert, key, uid, gid)))
        if not args.initial:
            run('nginx', '-t')
            run('systemctl', 'reload', 'nginx')
            wait_served_certificate(cert, meta['domains'][0])
            run('systemctl', 'restart', 'guangyue-hy2')
            run('systemctl', 'is-active', '--quiet', 'guangyue-hy2')
    except Exception:
        for base, target in reversed(old):
            swap(base, target)
        if not args.initial:
            subprocess.run(['systemctl', 'reload', 'nginx'], capture_output=True)
            subprocess.run(['systemctl', 'restart', 'guangyue-hy2'], capture_output=True)
        raise
    # Keep current and one prior generation; certificate archives contain private keys.
    for base, previous in old:
        current = os.readlink(Path(base) / 'current')
        for child in Path(base).glob('cert-*'):
            if child.is_dir() and not child.is_symlink() and child.name not in {current, previous}:
                shutil.rmtree(child)
    print('Certificate pair validated and deployed.')


if __name__ == '__main__':
    try:
        main()
    except Exception as exc:
        raise SystemExit('Certificate deployment failed: ' + type(exc).__name__)
