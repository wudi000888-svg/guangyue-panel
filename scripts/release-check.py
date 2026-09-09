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
if not re.fullmatch(r'\d+\.\d+\.\d+', version):
    errors.append('invalid VERSION')
if 'const version = "' + version + '"' not in (root / 'backend/config.go').read_text():
    errors.append('Go version mismatch')
for name in ['frontend/package.json', 'frontend/package-lock.json']:
    doc = json.loads((root / name).read_text())
    if doc['version'] != version:
        errors.append(name + ': version mismatch')
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
print('Release tree checks passed:', len(paths), 'files; versions, relative links and secret artifact patterns.')
