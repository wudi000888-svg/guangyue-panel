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
    def test_wallet_restore_has_only_a_scoped_upload_exception(self):
        panel = render.render('panel.example.com', 'node.example.com', 'www.cloudflare.com')['nginx-panel.conf']
        restore = render.WALLET_RESTORE_LOCATION
        self.assertEqual(panel.count(restore), 1)
        self.assertEqual(panel.count('client_max_body_size 16m;'), 1)
        self.assertIn('client_max_body_size 5m;', panel)
        self.assertIn('location = /api/support/upload {\n        client_max_body_size 3m;', panel)
        for directive in ['proxy_request_buffering off;', 'proxy_pass http://127.0.0.1:19100;',
                          'proxy_set_header Host $host;', 'proxy_set_header X-Real-IP $remote_addr;',
                          'proxy_set_header X-Forwarded-For $remote_addr;',
                          'proxy_set_header X-Forwarded-Proto https;', 'proxy_read_timeout 45s;']:
            self.assertIn(directive, restore)
        # A location-level add_header would suppress inherited server headers.
        self.assertNotIn('add_header', restore)
        self.assertIn('add_header Strict-Transport-Security', panel)
        self.assertEqual(render.wallet_restore_location(panel), panel)

        treasury = render.CRYPTO_TREASURY_LOCATION
        collection = render.CRYPTO_WALLET_COLLECTION_LOCATION
        self.assertEqual(panel.count(collection), 1)
        self.assertIn('location = /api/commerce/crypto/admin/wallets {', collection)
        self.assertIn('proxy_pass http://127.0.0.1:19100;', collection)
        self.assertIn('proxy_read_timeout 45s;', collection)
        for directive in ['proxy_set_header Host $host;', 'proxy_set_header X-Real-IP $remote_addr;',
                          'proxy_set_header X-Forwarded-For $remote_addr;',
                          'proxy_set_header X-Forwarded-Proto https;']:
            self.assertIn(directive, collection)
        self.assertNotIn('add_header', collection)
        self.assertNotIn('client_max_body_size', collection)
        self.assertEqual(panel.count(treasury), 1)
        self.assertIn('proxy_read_timeout 100s;', treasury)
        self.assertNotIn('add_header', treasury)
        self.assertNotIn('client_max_body_size', treasury)
        legacy = panel.replace(restore, '').replace(treasury, '')
        self.assertEqual(render.wallet_restore_location(legacy).replace(restore, '').replace(treasury, ''), legacy)
        custom = panel.replace(restore, '').replace('access_log off;', '# preserve custom settings\n    access_log off;', 1)
        self.assertEqual(render.wallet_restore_location(custom).replace(restore, ''), custom)
        with self.assertRaises(ValueError):
            render.wallet_restore_location(panel.replace('client_max_body_size 16m;', 'client_max_body_size 1m;'))

    def test_v037_and_v038_wallet_collection_upgrade_is_additive_and_idempotent(self):
        panel = render.render('panel.example.com', 'node.example.com', 'www.cloudflare.com')['nginx-panel.conf']
        collection = render.CRYPTO_WALLET_COLLECTION_LOCATION
        for version in ['0.37.0', '0.38.0']:
            with self.subTest(version=version):
                legacy = panel.replace(collection, '').replace('access_log off;', '# custom ' + version + '\n    access_log off;', 1)
                upgraded = render.wallet_restore_location(legacy)
                self.assertEqual(upgraded.count(collection), 1)
                self.assertEqual(upgraded.replace(collection, ''), legacy)
                self.assertEqual(render.wallet_restore_location(upgraded), upgraded)
        # A conflicting custom collection is never silently overwritten.
        with self.assertRaisesRegex(ValueError, 'differs from the managed'):
            render.wallet_restore_location(panel.replace(collection, collection.replace('45s;', '70s;')))

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


class PostgresRecoveryTests(unittest.TestCase):
    toc = b'''; Archive created by pg_dump
6; 2615 16400 SCHEMA - gy_site_a guangyue
201; 1259 16401 TABLE gy_site_a users guangyue
202; 1259 16402 SEQUENCE gy_site_a users_id_seq guangyue
203; 0 0 SEQUENCE OWNED BY gy_site_a users_id_seq guangyue
204; 2604 16403 DEFAULT gy_site_a users id guangyue
205; 0 16401 TABLE DATA gy_site_a users guangyue
206; 0 0 SEQUENCE SET gy_site_a users_id_seq guangyue
207; 2606 16404 CONSTRAINT gy_site_a users users_pkey guangyue
208; 1259 16405 INDEX gy_site_a users_name guangyue
209; 2606 16406 FK CONSTRAINT gy_site_a users parent_fk guangyue
210; 0 0 ACL - SCHEMA gy_site_a guangyue
211; 0 0 COMMENT gy_site_a TABLE users guangyue
'''

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.snapshot = Path(self.temp.name) / 'postgres.dump'
        self.snapshot.write_bytes(b'fixture archive')
        self.config = {'site_id': 'site_a', 'database': {'driver': 'postgres',
                       'dsn': 'postgres://fixture:private-fixture-password@127.0.0.1:25433/fixture'}}

    def test_invalid_site_or_archive_path_cannot_start_recovery(self):
        with patch.object(infrastructure.subprocess, 'run') as command:
            for site in ['site_a; DROP SCHEMA public', 'site_a\n', 'SiteA', '*', '../other', '', None, 'a' * 41]:
                with self.subTest(site=site), self.assertRaises(ValueError):
                    infrastructure.restore_postgres(dict(self.config, site_id=site), self.snapshot)
            link = Path(self.temp.name) / 'link.dump'
            link.symlink_to(self.snapshot)
            with self.assertRaises(ValueError):
                infrastructure.restore_postgres(self.config, link)
            command.assert_not_called()

    def test_wrong_site_global_objects_and_unsupported_ddl_are_rejected_before_psql(self):
        dangerous = [
            self.toc.replace(b'gy_site_a', b'gy_other'),
            self.toc + b'212; 2615 19990 SCHEMA - public owner\n',
            self.toc + b'212; 1259 19991 TABLE public unrelated owner\n',
            self.toc + b'212; 1262 19992 DATABASE - fixture owner\n',
            self.toc + b'212; 0 19993 BLOB - 19993 owner\n',
            self.toc + b'212; 3079 19994 EXTENSION - pgcrypto owner\n',
            self.toc + b'212; 1255 19995 FUNCTION gy_site_a unsafe() owner\n',
            self.toc + b'212; 0 0 TABLE ATTACH gy_site_a partition owner\n',
            self.toc + b'212; 0 0 ACL - DATABASE fixture owner\n',
            self.toc + b'201; 1259 16401 TABLE gy_site_a users guangyue\n',
            b'6; 2615 16400 SCHEMA - gy_site_a guangyue\n',
        ]
        for listing in dangerous:
            with self.subTest(listing=listing[-90:]), patch.object(infrastructure.subprocess, 'run',
                    return_value=subprocess.CompletedProcess([], 0, stdout=listing)) as command:
                with self.assertRaises(ValueError):
                    infrastructure.restore_postgres(self.config, self.snapshot)
                self.assertEqual(command.call_count, 1)
                self.assertEqual(command.call_args.args[0][:2], ['pg_restore', '--list'])

    def test_scope_cleanup_and_restore_are_one_transaction_without_cascade(self):
        calls, scripts = [], []

        def command(args, **kwargs):
            calls.append(args)
            self.assertNotIn('private-fixture-password', ' '.join(args))
            self.assertEqual(kwargs['env']['PGPASSWORD'], 'private-fixture-password')
            if args[:2] == ['pg_restore', '--list']:
                return subprocess.CompletedProcess(args, 0, stdout=self.toc)
            if args[0] == 'pg_restore':
                self.assertIn('--file=-', args)
                self.assertFalse(any(arg == '-n' or arg.startswith('--schema') for arg in args))
                self.assertNotIn('--clean', args)
                kwargs['stdout'].write(b'CREATE SCHEMA gy_site_a;\nCREATE TABLE gy_site_a.users(id bigint);\n')
            else:
                self.assertEqual(args[0], 'psql')
                self.assertIn('-X', args)
                self.assertIn('--single-transaction', args)
                self.assertIn('--set=ON_ERROR_STOP=1', args)
                self.assertTrue(kwargs['capture_output'])
                script = Path(args[args.index('--file') + 1])
                scripts.append(script)
                self.assertEqual(script.stat().st_mode & 0o777, 0o600)
                self.assertEqual(script.parent.stat().st_mode & 0o777, 0o700)
                sql = script.read_text()
                self.assertNotIn('CASCADE', sql)
                self.assertIn("EXECUTE 'DROP TABLE ' || objects || ' RESTRICT'", sql)
                self.assertIn("EXECUTE 'DROP SCHEMA gy_site_a RESTRICT'", sql)
                self.assertIn('CREATE SCHEMA gy_site_a;', sql)
                self.assertLess(sql.index('DROP SCHEMA gy_site_a'), sql.index('CREATE SCHEMA gy_site_a'))
            return subprocess.CompletedProcess(args, 0, stdout=b'')

        with patch.object(infrastructure.subprocess, 'run', side_effect=command):
            infrastructure.restore_postgres(self.config, self.snapshot)
        self.assertEqual([args[0] for args in calls], ['pg_restore', 'pg_restore', 'psql'])
        self.assertTrue(scripts)
        self.assertTrue(all(not script.parent.exists() for script in scripts))

    def test_corrupt_archive_or_sql_failure_is_private_and_export_failure_never_connects(self):
        for stage in ['list', 'export', 'apply']:
            calls = []

            def command(args, **kwargs):
                calls.append(args)
                current = 'list' if args[:2] == ['pg_restore', '--list'] else 'apply' if args[0] == 'psql' else 'export'
                if current == stage:
                    raise subprocess.CalledProcessError(1, args, stderr=b'private-fixture-password; secret-row-value')
                if current == 'export':
                    kwargs['stdout'].write(b'CREATE SCHEMA gy_site_a;\n')
                return subprocess.CompletedProcess(args, 0, stdout=self.toc if current == 'list' else b'')

            with self.subTest(stage=stage), patch.object(infrastructure.subprocess, 'run', side_effect=command):
                with self.assertRaises(ValueError) as error:
                    infrastructure.restore_postgres(self.config, self.snapshot)
                self.assertNotIn('secret', str(error.exception))
                self.assertNotIn('private-fixture-password', str(error.exception))
                self.assertEqual(any(args[0] == 'psql' for args in calls), stage == 'apply')

class BusinessDeploymentTests(unittest.TestCase):
    def test_business_role_accepts_token_without_registration_file(self):
        config=infrastructure.edition_config({},'lite','site_tokyo',role='business',controller_url='https://control.example.com',connect_token='gye_'+'A'*43)
        self.assertEqual(config['database'],{'driver':'sqlite'})
        self.assertEqual(config['controller_url'],'https://control.example.com')
        self.assertEqual(config['connect_token'],'gye_'+'A'*43)
        self.assertNotIn('enrollment_token',config)
        with self.assertRaisesRegex(ValueError,'controller URL'):
            infrastructure.edition_config({},'lite','site_tokyo',role='business',controller_url='http://control.example.com',connect_token='gye_'+'A'*43)
        with self.assertRaisesRegex(ValueError,'connection token'):
            infrastructure.edition_config({},'lite','site_tokyo',role='business',controller_url='https://control.example.com',connect_token='bad')

    def test_business_role_uses_sqlite_without_infrastructure(self):
        with tempfile.TemporaryDirectory() as temp:
            path=Path(temp)/'enrollment.json'
            path.write_text(json.dumps({'site_id':'site_east','controller_url':'https://control.example.com','enrollment_token':'gye_'+'A'*43}))
            path.chmod(0o600)
            config=infrastructure.edition_config({},'pro','site_east',role='business',enrollment=path)
            self.assertEqual(config['database']['driver'],'sqlite')
            self.assertEqual(config['redis_url'],'')
            self.assertEqual(config['role'],'business')
            self.assertEqual(infrastructure.edition_config(config,'pro','site_east',existing=True),config)
            unit=Path(install.DEPLOY)/'guangyue.service'
            self.assertIn('MemoryMax=160M',infrastructure.service_text(unit,'pro','business'))
            lite=infrastructure.edition_config({},'lite','site_east',enrollment=path)
            self.assertEqual(lite['edition'],'lite')
            self.assertEqual(lite['role'],'business')
            self.assertEqual(lite['database'],{'driver':'sqlite'})
            self.assertEqual(infrastructure.edition_config(lite,'lite','site_east',existing=True),lite)
            self.assertIn('MemoryMax=160M',infrastructure.service_text(unit,'lite','business'))
            for edition,site,role in [('lite','site_west',None),('pro','site_east',None),('lite','site_east','standalone')]:
                with self.assertRaisesRegex(ValueError,'existing sub-site'):
                    infrastructure.edition_config(lite,edition,site,role=role,existing=True)
            with self.assertRaises(ValueError):infrastructure.edition_config({},'pro','site_west',role='business',enrollment=path)
            path.chmod(0o644)
            with self.assertRaises(ValueError):infrastructure.edition_config({},'pro','site_east',role='business',enrollment=path)

    def test_new_defaults_and_legacy_upgrade(self):
        independent=infrastructure.edition_config({},'lite','edge')
        self.assertEqual(independent['role'],'standalone')
        for legacy in ({'state_dir':'/synthetic/state'},{'edition':'lite'},{'edition':'lite','role':'standalone'}):
            updated=infrastructure.edition_config(legacy,'lite','default',existing=True)
            self.assertEqual(updated['role'],'standalone')
            self.assertEqual(updated['database'],{'driver':'sqlite'})
            self.assertEqual(legacy.get('role'), 'standalone' if 'role' in legacy else None)
        pro={'database':{'driver':'postgres','dsn':'postgres://localhost/fixture'},'redis_url':'redis://localhost/0'}
        self.assertEqual(infrastructure.edition_config(pro,'pro','control')['role'],'controller')
        legacy_pro=dict(pro,edition='pro')
        self.assertEqual(infrastructure.edition_config(legacy_pro,'pro','control',existing=True)['role'],'controller')
        self.assertEqual(infrastructure.edition_config(dict(independent,**pro),'pro','control',existing=True)['role'],'controller')
        with self.assertRaises(ValueError):infrastructure.edition_config({},'lite','edge',role='controller')
