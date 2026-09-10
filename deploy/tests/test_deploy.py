import argparse
import hashlib
import importlib.util
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import render
import certificates
import install
import infrastructure
import json
from common import deployment_lock


class LockTests(unittest.TestCase):
    def test_concurrent_mutation_and_symlink_refused(self):
        with tempfile.TemporaryDirectory() as temp:
            lock = Path(temp) / 'lock'
            with deployment_lock(lock):
                with self.assertRaisesRegex(ValueError, 'another install'):
                    with deployment_lock(lock):
                        self.fail('a concurrent mutation acquired the lock')
            with deployment_lock(lock):
                pass
            lock.unlink()
            lock.symlink_to(Path(temp) / 'victim')
            with self.assertRaises(OSError):
                with deployment_lock(lock):
                    self.fail('followed an untrusted lock symlink')


class RenderTests(unittest.TestCase):
    def test_same_domain_and_existing_sites_route_explicitly(self):
        result = render.render('panel.example.com', 'panel.example.com', 'www.cloudflare.com', ['shop.example.com', 'shop.example.com'])
        stream = result['nginx-stream.conf']
        self.assertEqual(stream.count('panel.example.com 127.0.0.1:10443;'), 1)
        self.assertEqual(stream.count('shop.example.com 127.0.0.1:10443;'), 1)
        self.assertIn('proxy_protocol on;', stream)
        self.assertIn('real_ip_header proxy_protocol;', result['nginx-panel.conf'])

    def test_reject_config_injection_and_self_target(self):
        for bad in ['a.com; include /tmp/evil;', 'a.com\n', '*.example.com', 'https://a.com', 'A.COM', 'a' * 64 + '.com']:
            with self.assertRaises(argparse.ArgumentTypeError):
                render.domain(bad)
        with self.assertRaises(ValueError):
            render.render('a.example.com', 'b.example.com', 'a.example.com')
        with self.assertRaises(ValueError):
            render.render('a.example.com', 'b.example.com', 'shop.example.com', ['shop.example.com'])

    def test_cli_outputs_without_host_mutation(self):
        with tempfile.TemporaryDirectory() as temp:
            subprocess.run([sys.executable, str(Path(render.__file__)), '--panel-domain', 'panel.example.com', '--node-domain', 'node.example.com', '--output', temp], check=True, capture_output=True)
            self.assertEqual({p.name for p in Path(temp).iterdir()}, {'nginx-stream.conf', 'nginx-panel.conf'})


class CertificateTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.cert, self.key = self.root / 'cert.pem', self.root / 'key.pem'
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '3', '-subj', '/CN=panel.example.com', '-addext', 'subjectAltName=DNS:panel.example.com,DNS:node.example.com', '-keyout', str(self.key), '-out', str(self.cert)], capture_output=True, check=True)

    def test_pair_and_hostname_validation(self):
        certificates.validate(self.cert, self.key, ['panel.example.com', 'node.example.com'])
        with self.assertRaises((ValueError, subprocess.CalledProcessError)):
            certificates.validate(self.cert, self.key, ['wrong.example.com'])
        wrong = self.root / 'wrong.key'
        subprocess.run(['openssl', 'genrsa', '-out', str(wrong), '2048'], capture_output=True, check=True)
        with self.assertRaises(ValueError):
            certificates.validate(self.cert, wrong, ['panel.example.com'])

    def test_generation_swap_and_rollback_keep_matching_pair(self):
        base = self.root / 'published'
        uid, gid = os.getuid(), os.getgid()
        self.assertIsNone(certificates.publish(base, self.cert, self.key, uid, gid))
        first = os.readlink(base / 'current')
        previous = certificates.publish(base, self.cert, self.key, uid, gid)
        self.assertEqual(previous, first)
        self.assertNotEqual(os.readlink(base / 'current'), first)
        certificates.swap(base, previous)
        certificates.validate(base / 'current/fullchain.pem', base / 'current/privkey.pem', ['node.example.com'])
        self.assertEqual((base / 'current/privkey.pem').stat().st_mode & 0o777, 0o600)


class BundleTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        for name in ['VERSION', 'bin/guangyue-linux-amd64', 'bin/hysteria-node-linux-amd64', 'web/index.html']:
            p = self.root / name
            p.parent.mkdir(exist_ok=True)
            p.write_text('synthetic test content')
        self.manifest()

    def manifest(self):
        (self.root / 'SHA256SUMS').write_text('\n'.join(hashlib.sha256(p.read_bytes()).hexdigest() + '  ' + p.relative_to(self.root).as_posix() for p in self.root.rglob('*') if p.is_file() and p.name != 'SHA256SUMS') + '\n')

    def test_tamper_rejected_before_execution(self):
        (self.root / 'bin/guangyue-linux-amd64').write_text('tampered')
        with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
            install.verify_bundle(self.root)

    def test_extra_file_and_symlink_rejected(self):
        (self.root / 'unexpected.py').write_text('unexpected')
        with self.assertRaisesRegex(ValueError, 'unverified'):
            install.verify_bundle(self.root)
        (self.root / 'unexpected.py').unlink()
        (self.root / 'escape').symlink_to('/etc/passwd')
        with self.assertRaisesRegex(ValueError, 'symlink'):
            install.verify_bundle(self.root)

    def test_missing_core_and_wrong_core_rejected(self):
        with self.assertRaisesRegex(ValueError, 'fetch-xray'):
            install.verify_bundle(self.root)
        (self.root / 'bin/xray-linux-amd64').write_text('untrusted core')
        with self.assertRaisesRegex(ValueError, 'upstream binary checksum'):
            install.verify_bundle(self.root)

    def test_manifest_path_escape_rejected(self):
        (self.root / 'SHA256SUMS').write_text('0' * 64 + '  ../outside\n')
        with self.assertRaisesRegex(ValueError, 'invalid bundle'):
            install.verify_bundle(self.root)


if __name__ == '__main__':
    unittest.main()

class EditionTests(unittest.TestCase):
    def test_missing_infrastructure_and_downgrade_refused(self):
        with self.assertRaises(ValueError):infrastructure.edition_config({},'pro','site_a')
        with self.assertRaises(ValueError):infrastructure.edition_config({'edition':'pro'},'lite','site_a')
        for site in ['../../outside','UPPER','site-a','a'*41]:
            with self.assertRaises(ValueError):infrastructure.edition_config({},'lite',site)
    def test_profile_permissions_and_resource_budgets(self):
        with tempfile.TemporaryDirectory() as temp:
            path=Path(temp)/'profile.json'
            path.write_text(json.dumps({'database':{'driver':'postgres','dsn':'postgres://localhost/fixture'},'redis_url':'redis://localhost/0'}))
            path.chmod(0o644)
            with self.assertRaises(ValueError):infrastructure.read_profile(path)
            path.chmod(0o600)
            pro=infrastructure.edition_config({'reality_public':'stable'},'pro','site_a',path)
            self.assertEqual(pro['reality_public'],'stable')
            unit=Path(install.__file__).parent/'guangyue.service'
            self.assertIn('MemoryMax=384M',infrastructure.service_text(unit,'pro'))
            self.assertIn('MemoryMax=160M',infrastructure.service_text(unit,'lite'))

class InfrastructureBundleTests(unittest.TestCase):
    def test_help_does_not_write_into_verified_bundle(self):
        import shutil
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp)
            for name in ['infrastructure.py','common.py']:
                shutil.copyfile(Path(install.__file__).parent/name,root/name)
            subprocess.run([sys.executable,str(root/'infrastructure.py'),'--help'],check=True,capture_output=True)
            self.assertFalse((root/'__pycache__').exists())

class BusinessDeploymentTests(unittest.TestCase):
    def test_business_role_uses_sqlite_without_infrastructure(self):
        with tempfile.TemporaryDirectory() as temp:
            path=Path(temp)/'enrollment.json'
            path.write_text(json.dumps({'site_id':'site_east','controller_url':'https://control.example.com','enrollment_token':'gye_'+'A'*43}))
            path.chmod(0o600)
            config=infrastructure.edition_config({},'pro','site_east',role='business',enrollment=path)
            self.assertEqual(config['database']['driver'],'sqlite')
            self.assertEqual(config['redis_url'],'')
            self.assertEqual(config['role'],'business')
            self.assertEqual(infrastructure.edition_config(config,'pro','site_east'),config)
            unit=Path(install.DEPLOY)/'guangyue.service'
            self.assertIn('MemoryMax=160M',infrastructure.service_text(unit,'pro','business'))
            with self.assertRaises(ValueError):infrastructure.edition_config({},'lite','site_east',role='business',enrollment=path)
            with self.assertRaises(ValueError):infrastructure.edition_config({},'pro','site_west',role='business',enrollment=path)
            path.chmod(0o644)
            with self.assertRaises(ValueError):infrastructure.edition_config({},'pro','site_east',role='business',enrollment=path)
