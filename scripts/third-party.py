#!/usr/bin/env python3
"""Collect locked dependency license texts and a deterministic SPDX 2.3 inventory."""
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
    subprocess.run([go, 'mod', 'download'], cwd=directory, check=True)
    raw = subprocess.check_output([go, 'list', '-m', '-json', 'all'], cwd=directory).decode()
    decoder = json.JSONDecoder()
    while raw.strip():
        value, pos = decoder.raw_decode(raw.lstrip())
        raw = raw.lstrip()[pos:]
        entry = value.get('Replace', value)
        if value.get('Main') or not entry.get('Dir'):
            continue
        collect(value['Path'], value.get('Version', 'local'), entry['Dir'], 'golang')


go_modules(root / 'backend')
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
(out / 'DEPENDENCY-LICENSES.txt').write_text('Generated from locked runtime dependencies. SPDX NOASSERTION means not automatically classified.\n' + '\n'.join(texts))
version = (root / 'VERSION').read_text().strip()
try:
    epoch = int(os.environ.get('SOURCE_DATE_EPOCH') or subprocess.check_output(['git', 'show', '-s', '--format=%ct', 'HEAD'], cwd=root).decode().strip())
except (ValueError, subprocess.CalledProcessError):
    epoch = int(datetime.datetime.now(datetime.timezone.utc).timestamp())
created = datetime.datetime.fromtimestamp(epoch, datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ')
document = {'spdxVersion': 'SPDX-2.3', 'dataLicense': 'CC0-1.0', 'SPDXID': 'SPDXRef-DOCUMENT', 'name': 'Guangyue Panel ' + version + ' dependency inventory', 'documentNamespace': 'https://spdx.org/spdxdocs/guangyue-' + version + '-' + hashlib.sha256('\n'.join(sorted(packages)).encode()).hexdigest()[:16], 'creationInfo': {'creators': ['Tool: Guangyue third-party.py'], 'created': created}, 'packages': list(packages.values())}
(out / 'SBOM.spdx.json').write_text(json.dumps(document, ensure_ascii=False, indent=2) + '\n')
print('Collected', len(packages), 'dependency records and license texts; no local paths in generated output.')
