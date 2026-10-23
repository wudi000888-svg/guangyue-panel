#!/usr/bin/env python3
"""Check the publishable tree for versions, local links and common secret artifacts."""
import json
import re
import subprocess
from pathlib import Path

root = Path(__file__).resolve().parent.parent
excluded = {'.git', '.cache', 'node_modules', 'build', 'dist', '__pycache__'}
paths = [p for p in root.rglob('*') if p.is_file() and not excluded.intersection(p.relative_to(root).parts)]
errors = []
version = (root / 'VERSION').read_text().strip()
if (root / 'frontend/src/data/clients.json').read_bytes() != (root / 'backend/internal/clientcatalog/clients.json').read_bytes():
    errors.append('embedded client catalog is stale; run scripts/sync-client-catalog.py')
if not re.fullmatch(r'\d+\.\d+\.\d+', version):
    errors.append('invalid VERSION')
if 'const version = "' + version + '"' not in (root / 'backend/internal/controlplane/config.go').read_text():
    errors.append('Go version mismatch')
for name in ['frontend/package.json', 'frontend/package-lock.json']:
    doc = json.loads((root / name).read_text())
    if doc['version'] != version:
        errors.append(name + ': version mismatch')


def pinned_versions():
    """Parse scripts/versions.env strictly: KEY=value lines and comments only."""
    values = {}
    for number, line in enumerate((root / 'scripts/versions.env').read_text().splitlines(), 1):
        if not line.strip() or line.lstrip().startswith('#'):
            continue
        match = re.fullmatch(r'([A-Z][A-Z0-9_]*)=([^\s"\'$`]+)(?:\s+#.*)?', line)
        if not match or match.group(1) in values:
            errors.append(f'scripts/versions.env:{number}: expected one unquoted KEY=value')
            continue
        values[match.group(1)] = match.group(2)
    return values


def expect(relative, needle, label):
    if needle not in (root / relative).read_text():
        errors.append(f'{relative}: {label} must mention {needle} (scripts/versions.env / backend/go.mod)')


# Single sources of truth: backend/go.mod (Go), .nvmrc (Node), scripts/versions.env
# (proxy cores and database images). Verify every mention that cannot read them.
go_mod = (root / 'backend/go.mod').read_text()
go_match = re.search(r'^toolchain go(\d+\.\d+\.\d+)$', go_mod, re.M)
if not go_match:
    errors.append('backend/go.mod: missing exact "toolchain goX.Y.Z" directive')
else:
    go_version = go_match.group(1)
    for mention in sorted(set(re.findall(r'\bGo (\d+\.\d+\.\d+)', (root / 'CONTRIBUTING.md').read_text()))):
        if mention != go_version:
            errors.append(f'CONTRIBUTING.md: Go {mention} differs from backend/go.mod toolchain {go_version}')
    expect('CONTRIBUTING.md', 'Go ' + go_version, 'local development requirements')
    expect('docs/VALIDATION.md', '固定使用 Go ' + go_version, 'the Go build record')
node_version = (root / '.nvmrc').read_text().strip()
if not re.fullmatch(r'\d+', node_version):
    errors.append('.nvmrc: expected a Node major version')
expect('CONTRIBUTING.md', 'Node ' + node_version, 'local development requirements')
pins = pinned_versions()
required = ['MIHOMO_VERSION', 'MIHOMO_COMMIT', 'MIHOMO_BINARY_SHA256', 'XRAY_VERSION', 'XRAY_COMMIT', 'XRAY_RELEASE_ZIP_SHA256', 'HY2_VERSION', 'HY2_COMMIT', 'POSTGRES_IMAGE', 'REDIS_IMAGE']
missing = [key for key in required if key not in pins]
if missing:
    errors.append('scripts/versions.env: missing ' + ', '.join(missing))
else:
    notices = {row.split('|')[1].strip(): row for row in (root / 'THIRD_PARTY_NOTICES.md').read_text().splitlines() if row.startswith('|') and row.count('|') > 2}
    for name, needles in [
        ('Xray-core', ['[' + pins['XRAY_VERSION'] + ']', 'tree/' + pins['XRAY_VERSION']]),
        ('Hysteria', ['app/' + pins['HY2_VERSION'], pins['HY2_COMMIT']]),
        ('Mihomo', ['[' + pins['MIHOMO_VERSION'] + ' / ' + pins['MIHOMO_COMMIT'][:8], 'tree/' + pins['MIHOMO_COMMIT']]),
    ]:
        for needle in needles:
            if needle not in notices.get(name, ''):
                errors.append(f'THIRD_PARTY_NOTICES.md: {name} row must mention {needle} (scripts/versions.env)')
    expect('core-patches/README.md', 'tag `app/' + pins['HY2_VERSION'] + '`, commit `' + pins['HY2_COMMIT'] + '`', 'the Hysteria base')
    expect('core-patches/README.md', 'XTLS/Xray-core ' + pins['XRAY_VERSION'] + '](https://github.com/XTLS/Xray-core/tree/' + pins['XRAY_COMMIT'] + ')', 'the Xray base')
    for unit, label in [('guangyue-hy2.service', 'Hysteria2 ' + pins['HY2_VERSION'][1:]), ('guangyue-xray.service', 'Xray ' + pins['XRAY_VERSION'][1:])]:
        if re.search(r'^Description=.*$', (root / 'deploy' / unit).read_text(), re.M).group(0) != 'Description=Guangyue Panel ' + label:
            errors.append(f'deploy/{unit}: Description must name {label} (scripts/versions.env)')
    if not re.search(r"'mihomo': '" + pins['MIHOMO_BINARY_SHA256'] + "'", (root / 'deploy/install.py').read_text()):
        errors.append('deploy/install.py: UPSTREAM_HASHES mihomo must equal MIHOMO_BINARY_SHA256 (scripts/versions.env)')
    if re.findall(r'^FROM (\S+)', (root / 'deploy/docker/Dockerfile').read_text(), re.M) != [pins['POSTGRES_IMAGE']]:
        errors.append('deploy/docker/Dockerfile: FROM must equal POSTGRES_IMAGE (scripts/versions.env)')
    docker_build = (root / 'scripts/build-docker.sh').read_text()
    for variable, key in [('postgres', 'POSTGRES_IMAGE'), ('redis', 'REDIS_IMAGE')]:
        if re.findall(r"^" + variable + r"='([^']+)'$", docker_build, re.M) != [pins[key]]:
            errors.append(f'scripts/build-docker.sh: {variable}= must equal {key} (scripts/versions.env)')
    approved = re.search(r"re\.fullmatch\(r'(\(postgres:[^']+)'", (root / 'scripts/docker-provenance.py').read_text())
    for key in ('POSTGRES_IMAGE', 'REDIS_IMAGE'):
        if not approved or not re.fullmatch(approved.group(1), pins[key]):
            errors.append(f'scripts/docker-provenance.py: approved image pattern must accept {key}')

patterns = [
    ('private key', re.compile(r'-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----')),
    ('GitHub token', re.compile(r'\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,})\b')),
    ('AWS key', re.compile(r'\bAKIA[0-9A-Z]{16}\b')),
    ('local user path', re.compile(r'/Users/[A-Za-z][^\s"<>]*')),
]
for path in paths:
    relative = path.relative_to(root).as_posix()
    if path.suffix.lower() in {'.pem', '.key', '.db', '.sqlite', '.log'} or path.name in {'initial-owner.json', 'master.key', '.env'}:
        errors.append(relative + ': forbidden artifact')
    try:
        text = path.read_text()
    except UnicodeDecodeError:
        continue
    for label, pattern in patterns:
        if pattern.search(text):
            errors.append(relative + ': ' + label + ' (content redacted)')
    if path.suffix == '.md':
        for link in re.findall(r'\]\(([^)]+)\)', text):
            target = link.split('#')[0].split(' "')[0]
            if not target or re.match(r'[a-z]+:', target):
                continue
            if not (path.parent / target).exists():
                errors.append(relative + ': missing relative link ' + target)
if errors:
    raise SystemExit('\n'.join(errors))
print('Release tree checks passed:', len(paths), 'files; versions, pinned toolchain/core/image mentions, relative links and secret artifact patterns.')
