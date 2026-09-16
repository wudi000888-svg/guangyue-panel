"""Verify a custom Xray survives installation and an older updater's core path."""
import hashlib
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import install
import update_agent
import update_state


class BundledXrayTests(unittest.TestCase):
    def test_verified_extension_is_not_replaced_and_tampering_is_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            bundle = root / 'bundle'
            for name in ['VERSION', 'EDITION', 'bin/guangyue-linux-amd64', 'bin/hysteria-node-linux-amd64', 'bin/xray-linux-amd64', 'bin/mihomo-linux-amd64', 'web/index.html', 'deploy/install.py', 'scripts/fetch-xray.sh', 'scripts/fetch-mihomo.sh']:
                path = bundle / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text('synthetic ' + name)
            hashes = {name: hashlib.sha256((bundle / ('bin/' + name + '-linux-amd64')).read_bytes()).hexdigest() for name in ['xray', 'mihomo']}
            (bundle / 'deploy/install.py').write_text('UPSTREAM_HASHES = ' + repr(hashes))
            script = Path(__file__).resolve().parents[2] / 'scripts/fetch-xray.sh'
            shutil.copyfile(script, bundle / 'scripts/fetch-xray.sh')
            (bundle / 'SHA256SUMS').write_text('\n'.join(hashlib.sha256(p.read_bytes()).hexdigest() + '  ' + p.relative_to(bundle).as_posix() for p in bundle.rglob('*') if p.is_file()) + '\n')
            old = root / 'old'
            (old / 'bin').mkdir(parents=True)
            (old / 'bin/xray').write_text('old upstream build')
            with patch.object(install, 'UPSTREAM_HASHES', hashes), patch.object(update_state, 'APP', old), patch.object(update_agent.subprocess, 'run') as fetch:
                install.verify_bundle(bundle)
                update_agent.verify_payload(bundle)
                update_agent.cores(bundle)
                fetch.assert_not_called()
                self.assertEqual(hashlib.sha256((bundle / 'bin/xray-linux-amd64').read_bytes()).hexdigest(), hashes['xray'])
            result = subprocess.run(['bash', str(bundle / 'scripts/fetch-xray.sh')], capture_output=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr.decode())
            import os
            result = subprocess.run(['bash', str(bundle / 'scripts/fetch-xray.sh')], env=dict(os.environ, GY_BIN_DIR=str(bundle / 'bin')), capture_output=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr.decode())
            (bundle / 'bin/xray-linux-amd64').write_text('tampered')
            with self.assertRaises(ValueError): update_agent.verify_payload(bundle)
            with patch.object(install, 'UPSTREAM_HASHES', hashes), self.assertRaises(ValueError): install.verify_bundle(bundle)
            result = subprocess.run(['bash', str(bundle / 'scripts/fetch-xray.sh')], env=dict(os.environ, GY_BIN_DIR=str(bundle / 'bin')), capture_output=True, timeout=10)
            self.assertNotEqual(result.returncode, 0)
