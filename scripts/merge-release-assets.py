#!/usr/bin/env python3
"""Merge separately tested native/Docker artifacts, retaining verified hashes."""
import argparse
import hashlib
import json
import re
import shutil
from pathlib import Path


def names(version):
    if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+', version):
        raise ValueError('Invalid release version')
    native = {f'guangyue-panel-{e}-{version}-linux-amd64.tar.gz' for e in ('lite', 'pro')}
    native |= {f'guangyue-installer-{version}.tar.gz', 'install.sh', 'SBOM.spdx.json'}
    docker = {f'guangyue-panel-docker-{version}-linux-amd64.tar.gz', 'DOCKER_IMAGES.json'}
    return native, docker


def verify(directory, expected):
    directory = Path(directory)
    found = {}
    for line in (directory / 'SHA256SUMS').read_text().splitlines():
        digest, name = line.split('  ', 1)
        if name not in expected or name in found or not re.fullmatch('[a-f0-9]{64}', digest):
            raise ValueError('Unexpected or duplicate release manifest entry')
        path = directory / name
        if path.is_symlink() or not path.is_file():
            raise ValueError('Release asset is missing or not a regular file')
        with path.open('rb') as source:
            digest_state = hashlib.sha256()
            for block in iter(lambda: source.read(1 << 20), b''):
                digest_state.update(block)
            actual = digest_state.hexdigest()
        if actual != digest:
            raise ValueError('Release asset checksum failed: ' + name)
        found[name] = digest
    if set(found) != expected:
        raise ValueError('Release artifact is incomplete')
    return found


def merge(version, native, docker, destination):
    n, d = names(version)
    sums = {**verify(native, n), **verify(docker, d)}
    manifest = json.loads((Path(docker) / 'DOCKER_IMAGES.json').read_text())
    archive = f'guangyue-panel-docker-{version}-linux-amd64.tar.gz'
    if manifest.get('schema') != 1 or manifest.get('version') != version or manifest.get('platform') != 'linux/amd64' or manifest.get('archive') != archive or manifest.get('sha256') != sums[archive]:
        raise ValueError('Docker metadata disagrees with checked release archive')
    destination = Path(destination)
    destination.mkdir(parents=True, exist_ok=True)
    if any(destination.iterdir()):
        raise ValueError('Release destination must be empty')
    for directory, expected in ((native, n), (docker, d)):
        for name in expected:
            shutil.copyfile(Path(directory) / name, destination / name)
    (destination / 'SHA256SUMS').write_text(''.join(sums[name] + '  ' + name + '\n' for name in sorted(sums)))


if __name__ == '__main__':
    p = argparse.ArgumentParser()
    p.add_argument('version'); p.add_argument('native'); p.add_argument('docker'); p.add_argument('destination')
    a = p.parse_args()
    merge(a.version, a.native, a.docker, a.destination)
