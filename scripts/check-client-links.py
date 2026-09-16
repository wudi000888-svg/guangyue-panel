#!/usr/bin/env python3
"""Verify exact platform binaries exist in their official tagged GitHub release."""
import json
import time
import urllib.request
from pathlib import Path

clients = json.loads((Path(__file__).resolve().parents[1] / 'frontend/src/data/clients.json').read_text())
cache = {}

def read(url):
    for attempt in range(3):
        try:
            with urllib.request.urlopen(urllib.request.Request(url, headers={'User-Agent': 'Guangyue-Client-Catalog'}), timeout=25) as response:
                return response.read()
        except Exception:
            if attempt == 2: raise
            time.sleep(1)

count = 0
for client in clients:
    for download in client['downloads']:
        url = download['url']
        if '/releases/download/' in url:
            prefix, tail = url.split('/releases/download/')
            tag, _ = tail.split('/', 1)
            endpoint = 'https://api.github.com/repos/' + prefix.removeprefix('https://github.com/') + '/releases/tags/' + tag
            if endpoint not in cache:
                release = json.loads(read(endpoint))
                cache[endpoint] = {a['browser_download_url'] for a in release['assets'] if a.get('state') == 'uploaded'}
            if url not in cache[endpoint]: raise SystemExit('Official release asset missing: ' + url)
        else:
            read(url)
        count += 1
print('Verified', count, 'official client download links and release asset identities.')
