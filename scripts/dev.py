#!/usr/bin/env python3
"""Launch an isolated loopback-only development backend; no real proxy cores."""
import json
import os
import subprocess
from pathlib import Path

root = Path(__file__).resolve().parent.parent
state = root / '.cache/dev/state'
state.mkdir(parents=True, exist_ok=True, mode=0o700)
config = state.parent / 'config.json'
binary = state.parent / 'guangyue'
go = os.environ.get('GY_GO', 'go')
subprocess.run([go, 'build', '-o', str(binary), '.'], cwd=root / 'backend', check=True)
if not config.exists():
    subprocess.run([str(binary), '-config', str(config), '-init'], check=True)
data = json.loads(config.read_text())
data.update(state_dir=str(state), listen='127.0.0.1:19200', internal_listen='127.0.0.1:19201', public_url='http://127.0.0.1:9191', web_dir=str(root / 'frontend/dist'), dev=True)
config.write_text(json.dumps(data))
config.chmod(0o600)
print('Development API: http://127.0.0.1:19200; Vite UI: http://127.0.0.1:9191', flush=True)
print('Read initial credentials locally at .cache/dev/state/initial-owner.json. Do not publish them.', flush=True)
os.execv(binary, [str(binary), '-config', str(config)])
