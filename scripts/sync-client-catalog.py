#!/usr/bin/env python3
"""Sync the frontend's reviewed client directory into the Go embedded catalog."""
import argparse
import json
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--check', action='store_true')
args = parser.parse_args()
root = Path(__file__).resolve().parent.parent
source = root / 'frontend/src/data/clients.json'
destination = root / 'backend/internal/clientcatalog/clients.json'
content = source.read_bytes()
clients = json.loads(content)
ids = [client['id'] for client in clients]
if len(ids) != len(set(ids)):
    raise SystemExit('Duplicate client IDs in client catalog')
for client in clients:
    if not isinstance(client.get('downloads'), list):
        raise SystemExit('Client downloads must be an ordered array')
    for entry in client['downloads']:
        if not all(isinstance(entry.get(key), str) and entry[key] for key in ('platform', 'arch', 'url')):
            raise SystemExit('Client download is missing platform, architecture or URL')
if args.check:
    if not destination.exists() or destination.read_bytes() != content:
        raise SystemExit('Embedded client catalog is stale; run python3 scripts/sync-client-catalog.py')
    print('Client catalog source and Go embed match')
else:
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_bytes(content)
    print('Synced client catalog without changing IDs or download ordering')
