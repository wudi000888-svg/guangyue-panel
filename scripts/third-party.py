#!/usr/bin/env python3
"""Collect locked dependency/data licenses and a deterministic SPDX 2.3 inventory."""
import base64
import datetime
import hashlib
import json
import os
import re
import subprocess
from pathlib import Path
from urllib.parse import quote

root = Path(__file__).resolve().parent.parent
out = root / 'build/notices'
out.mkdir(parents=True, exist_ok=True)
packages = {}
texts = []
relationships = []


def collect(name, version, directory, ecosystem, license_id='NOASSERTION'):
    key = ecosystem + ':' + name + '@' + version
    if key in packages:
        return
    package = {'name': name, 'SPDXID': 'SPDXRef-' + hashlib.sha256(key.encode()).hexdigest()[:20], 'versionInfo': version, 'downloadLocation': 'NOASSERTION', 'filesAnalyzed': False, 'licenseConcluded': 'NOASSERTION', 'licenseDeclared': license_id or 'NOASSERTION', 'copyrightText': 'NOASSERTION', 'externalRefs': [{'referenceCategory': 'PACKAGE-MANAGER', 'referenceType': 'purl', 'referenceLocator': 'pkg:' + ecosystem + '/' + quote(name, safe='/') + '@' + quote(version, safe='')}]}
    packages[key] = package
    directory = Path(directory)
    licenses = sorted(p for p in directory.iterdir() if p.is_file() and re.match(r'(licen[cs]e|copying|notice|copyright)', p.name, re.I))
    for p in licenses:
        texts.append('\n' + '=' * 72 + '\n' + key + ' / ' + p.name + '\n' + '=' * 72 + '\n' + p.read_text(errors='replace'))
    if not licenses:
        texts.append('\n' + key + '\nNo root license file found; see declared package license: ' + str(license_id) + '\n')


def go_modules(directory):
    go = os.environ.get('GY_GO', 'go')
    subprocess.run([go, 'mod', 'download', 'all'], cwd=directory, check=True)
    raw = subprocess.check_output([go, 'list', '-m', '-json', 'all'], cwd=directory).decode()
    decoder = json.JSONDecoder()
    while raw.strip():
        value, pos = decoder.raw_decode(raw.lstrip())
        raw = raw.lstrip()[pos:]
        entry = value.get('Replace', value)
        if value.get('Main') or not entry.get('Dir'):
            continue
        collect(value['Path'], value.get('Version', 'local'), entry['Dir'], 'golang')


def wallet_core(lock):
    """The npm license field alone does not describe its bundled upstream WASM."""
    source = json.loads((root / 'licenses/trust-wallet-core-source.json').read_text())
    name, version = source['npm_package'], source['version']
    package_path = 'node_modules/' + name
    locked = lock['packages'].get(package_path)
    if not locked or locked.get('version') != version or locked.get('integrity') != source['npm_integrity']:
        raise SystemExit('Wallet Core lock changed: update pinned source/license provenance before packaging.')
    directory = root / 'frontend' / package_path
    for name, field in [('dist/lib/wallet-core.wasm', 'wasm_sha256'), ('dist/lib/wallet-core.js', 'javascript_glue_sha256')]:
        if hashlib.sha256((directory / name).read_bytes()).hexdigest() != source[field]:
            raise SystemExit('Wallet Core installed payload checksum mismatch: ' + name)
        shipped = root / 'frontend/dist/assets/wallet-core' / version / Path(name).name
        if not shipped.is_file() or hashlib.sha256(shipped.read_bytes()).hexdigest() != source[field]:
            raise SystemExit('Wallet Core built asset missing or changed: ' + shipped.relative_to(root).as_posix())
    key = 'generic:trust-wallet-core@' + version
    collect('trust-wallet-core', version, directory, 'generic', source['license'])
    upstream = packages[key]
    upstream.update(
        downloadLocation=source['source'],
        homepage=source['repository'],
        supplier='Organization: Trust Wallet',
        sourceInfo=source['attribution'] + ' Source commit: ' + source['commit'] + '. Published WASM SHA256: ' + source['wasm_sha256'] + '. ' + source['modifications'],
    )
    npm = packages['npm:' + source['npm_package'] + '@' + version]
    if npm['licenseDeclared'] != source['npm_declared_license']:
        raise SystemExit('Wallet Core npm license declaration changed; review upstream provenance.')
    npm.update(
        downloadLocation=source['npm_tarball'],
        sourceInfo='npm metadata declares MIT; the contained upstream Wallet Core WASM is separately declared Apache-2.0, with bundled third-party notices. See the CONTAINS relationship and pinned license files.',
        checksums=[{'algorithm': 'SHA512', 'checksumValue': base64.b64decode(source['npm_integrity'].removeprefix('sha512-'), validate=True).hex()}],
    )
    relationships.append({'spdxElementId': npm['SPDXID'], 'relationshipType': 'CONTAINS', 'relatedSpdxElement': upstream['SPDXID']})
    for entry in source['licenses']:
        path = root / 'licenses' / entry['file']
        if hashlib.sha256(path.read_bytes()).hexdigest() != entry['sha256']:
            raise SystemExit('Wallet Core license checksum mismatch: ' + entry['file'])
        texts.append('\n' + '=' * 72 + '\n' + key + ' / ' + entry['file'] + '\nSource: ' + entry['source'] + '\n' + '=' * 72 + '\n' + path.read_text())


go = os.environ.get('GY_GO', 'go')
goroot = subprocess.check_output([go, 'env', 'GOROOT'], cwd=root / 'backend').decode().strip()
go_version = subprocess.check_output([go, 'env', 'GOVERSION'], cwd=root / 'backend').decode().strip().removeprefix('go')
collect('golang-go', go_version, goroot, 'generic', 'BSD-3-Clause')
(out / 'GO-LICENSE.txt').write_text((Path(goroot) / 'LICENSE').read_text())
go_modules(root / 'backend')
xray_source = root / '.cache/xray-monitor-source'
collect('github.com/xtls/xray-core', 'v26.3.27-guangyue-sessions1', xray_source, 'golang', 'MPL-2.0')
go_modules(xray_source)
for source in sorted((root / '.cache').glob('hysteria-node-*')):
    if (source / 'app/go.mod').is_file():
        go_modules(source / 'app')
        go_modules(source / 'core')
        break
else:
    raise SystemExit('Build the pinned Hysteria core first so its dependency licenses can be collected.')
lock = json.loads((root / 'frontend/package-lock.json').read_text())
for name, value in lock['packages'].items():
    if not name or value.get('dev'):
        continue
    path = root / 'frontend' / name
    meta = json.loads((path / 'package.json').read_text())
    license_id = meta.get('license', 'NOASSERTION')
    if not isinstance(license_id, str):
        license_id = 'NOASSERTION'
    collect(meta['name'], value['version'], path, 'npm', license_id)
wallet_core(lock)
geoip = root / 'backend/internal/geoip'
dataset = json.loads((geoip / 'source.json').read_text())
for name, field in [('country.mmdb', 'sha256_mmdb'), ('LICENSE.txt', 'sha256_license')]:
    if hashlib.sha256((geoip / name).read_bytes()).hexdigest() != dataset[field]:
        raise SystemExit('DB-IP dataset or license checksum mismatch: ' + name)
collect('db-ip-country-lite', dataset['release'], geoip, 'generic', 'CC-BY-4.0')
packages['generic:db-ip-country-lite@' + dataset['release']].update(
    downloadLocation=dataset['download'],
    supplier='Organization: DB-IP.com',
    originator='Organization: DB-IP.com',
    sourceInfo=dataset['attribution'] + '; ' + dataset['source'] + '; ' + dataset['modifications'],
    checksums=[{'algorithm': 'SHA256', 'checksumValue': dataset['sha256_gzip']}],
)
for source, destination in [('LICENSE.txt', 'DB-IP-COUNTRY-LICENSE.txt'), ('NOTICE.txt', 'DB-IP-COUNTRY-NOTICE.txt'), ('source.json', 'DB-IP-COUNTRY-SOURCE.json')]:
    (out / destination).write_bytes((geoip / source).read_bytes())
(out / 'DEPENDENCY-LICENSES.txt').write_text('Generated from locked runtime dependencies. SPDX NOASSERTION means not automatically classified.\n' + '\n'.join(texts))
version = (root / 'VERSION').read_text().strip()
try:
    epoch = int(os.environ.get('SOURCE_DATE_EPOCH') or subprocess.check_output(['git', 'show', '-s', '--format=%ct', 'HEAD'], cwd=root).decode().strip())
except (ValueError, subprocess.CalledProcessError):
    epoch = int(datetime.datetime.now(datetime.timezone.utc).timestamp())
created = datetime.datetime.fromtimestamp(epoch, datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ')
document = {'spdxVersion': 'SPDX-2.3', 'dataLicense': 'CC0-1.0', 'SPDXID': 'SPDXRef-DOCUMENT', 'name': 'Guangyue Panel ' + version + ' dependency inventory', 'documentNamespace': 'https://spdx.org/spdxdocs/guangyue-' + version + '-' + hashlib.sha256('\n'.join(sorted(packages)).encode()).hexdigest()[:16], 'creationInfo': {'creators': ['Tool: Guangyue third-party.py'], 'created': created}, 'packages': list(packages.values())}
document['relationships'] = relationships
(out / 'SBOM.spdx.json').write_text(json.dumps(document, ensure_ascii=False, indent=2) + '\n')
print('Collected', len(packages), 'dependency records and license texts; no local paths in generated output.')
