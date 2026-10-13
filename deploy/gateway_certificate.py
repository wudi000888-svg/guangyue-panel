"""Fixed native gateway certificate notification; no caller-selected paths/units."""
import hashlib
import json
import os
import subprocess
import sys
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parent))
import certificates
import deployment_mode
import update_state as state
from common import deployment_lock
TLS_ROOT = Path('/var/lib/guangyue/tls')


def reconcile():
    if not deployment_mode.private_gateway() or deployment_mode.docker():
        return
    config = json.loads(state.CONFIG.read_text())
    base = TLS_ROOT
    if base.is_symlink() or base.resolve() != base:
        raise ValueError('证书目录不允许重定向到本实例以外')
    cert, key = Path(config['cert']).resolve(), Path(config['cert_key']).resolve()
    if not cert.is_relative_to(base) or not key.is_relative_to(base) or cert.parent != key.parent:
        raise ValueError('证书路径不属于本实例')
    certificates.validate(cert, key, {config['hy2_host']})
    digest = hashlib.sha256(cert.read_bytes() + b'\0' + key.read_bytes()).hexdigest()
    if (state.read('gateway-certificate.json') or {}).get('digest') == digest:
        return
    # A notification is not authorization to start a deliberately stopped core.
    running = subprocess.run(['systemctl', 'is-active', '--quiet', 'guangyue-hy2'], capture_output=True).returncode == 0
    if running:
        subprocess.run(['systemctl', 'restart', 'guangyue-hy2'], check=True, capture_output=True, timeout=35)
        subprocess.run(['systemctl', 'is-active', '--quiet', 'guangyue-hy2'], check=True, capture_output=True, timeout=10)
    state.write('gateway-certificate.json', {'digest': digest})


if __name__ == '__main__':
    if os.geteuid() != 0:
        raise SystemExit('Certificate reconciliation requires root')
    os.umask(0o077)
    try:
        with deployment_lock():
            reconcile()
    except Exception as exc:
        raise SystemExit('Certificate reconciliation failed: ' + type(exc).__name__)
