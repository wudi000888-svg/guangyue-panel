import hashlib
import io
import json
import os
import shutil
import sqlite3
import sys
import tarfile
import tempfile
import time
import unittest
from contextlib import contextmanager
from pathlib import Path
from unittest.mock import patch
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import update_state as state
import update_agent as agent
import upgrade
import install


class UpdateTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(); self.addCleanup(self.temp.cleanup); self.root = Path(self.temp.name)
        self.app = self.root / 'app'; self.app.mkdir(); (self.app / 'VERSION').write_text('0.18.0\n')
        self.data = self.root / 'data'; self.data.mkdir()
        self.config = self.root / 'config.json'; self.config.write_text(json.dumps({'edition': 'lite', 'role': 'standalone', 'site_id': 'test', 'state_dir': str(self.data), 'database': {'driver': 'sqlite'}}))
        db = sqlite3.connect(self.data / 'panel.db'); db.execute('CREATE TABLE schema_migrations(version TEXT, checksum TEXT)'); db.execute("INSERT INTO schema_migrations VALUES('001','fixed')"); db.commit(); db.close()
        for obj, key, value in [(state, 'ROOT', self.root / 'updates'), (state, 'APP', self.app), (state, 'CONFIG', self.config), (upgrade, 'APP', self.app), (upgrade, 'CONFIG', self.config)]:
            p = patch.object(obj, key, value); p.start(); self.addCleanup(p.stop)
        state.baseline('0.17.0')

    def request(self, **kw):
        return dict(action='update', version='0.19.0', expected_version='0.18.0', request_id='11111111-2222-4333-a444-555555555555', **kw)

    def cache(self):
        state.write('catalog.json', {'checked_at': int(time.time()), 'releases': [{'version': '0.19.0', 'editions': ['lite']}]})

    def test_numeric_floor_is_persistent_and_checked_on_both_paths(self):
        self.assertGreater(state.version('0.10.0'), state.version('0.9.99'))
        self.assertEqual(state.baseline('0.99.0')['version'], '0.17.0')
        for action in ['update', 'rollback']:
            request = self.request(); request.update(action=action, version='0.16.99')
            with self.assertRaises(ValueError): agent.submit(request, launch=False)
        self.assertFalse((state.ROOT / 'operation.json').exists())
        with self.assertRaises(ValueError): upgrade.rollback('0.16.0')
        for invalid in ['0.17.0-beta', '../0.17.0', '01.17.0', '0.17.0\n', '0.17']:
            with self.assertRaises(ValueError): state.version(invalid)

    def test_replay_busy_and_stale_version_do_not_launch_another_operation(self):
        self.cache(); first = agent.submit(self.request(), launch=False)
        self.assertEqual(agent.submit(self.request(), launch=False), first)
        second = self.request(); second['request_id'] = '22222222-2222-4333-a444-555555555555'
        with self.assertRaises(ValueError): agent.submit(second, launch=False)
        agent.progress('failed'); second['expected_version'] = '0.17.0'
        with self.assertRaises(ValueError): agent.submit(second, launch=False)
        self.assertEqual(state.read('operation.json')['request_id'], first['request_id'])

    def test_progress_polling_does_not_touch_database_during_file_switch(self):
        state.write('operation.json',dict(self.request(),stage='installing'))
        with patch.object(state,'candidates',side_effect=AssertionError('database must remain closed')):
            result=agent.status()
        self.assertEqual(result['operation']['stage'],'installing')
        self.assertEqual(result['rollback_versions'],[])

    def test_catalog_filters_unstable_and_mismatched_assets_and_preserves_offline_cache(self):
        def release(v, **extra):
            return dict(tag_name='v'+v, assets=[{'name': agent.asset_name(v, 'lite'), 'state': 'uploaded'}, {'name':'SHA256SUMS','state':'uploaded'}], **extra)
        data = [release('0.19.0'), release('0.20.0', prerelease=True), release('0.21.0', draft=True), {'tag_name': 'v0.22.0', 'assets': []}]
        with patch.object(agent, 'download', side_effect=lambda u,p,m:p.write_text(json.dumps(data))):
            self.assertEqual([r['version'] for r in agent.catalog()['releases']], ['0.19.0'])
        saved = state.read('catalog.json'); saved['checked_at'] = 1; state.write('catalog.json', saved)
        with patch.object(agent, 'download', side_effect=OSError('private transport details')):
            cached = agent.catalog(); self.assertEqual(cached['releases'][0]['version'], '0.19.0'); self.assertIn('warning', cached)
            self.assertNotIn('private', cached['warning'])

    def test_download_origin_restricted(self):
        for url in ['http://github.com/a', 'https://github.com.attacker.test/a', 'https://user@github.com/a', 'https://127.0.0.1/a', 'https://github.com:444/a']:
            with self.assertRaises(ValueError): agent.trusted_url(url)
        agent.trusted_url('https://release-assets.githubusercontent.com/a?signature=public-fixture')

    def archive(self, entries):
        path = self.root / 'test.tar.gz'
        with tarfile.open(path, 'w:gz') as tar:
            for name, kind, content in entries:
                info = tarfile.TarInfo(name); info.type = kind
                if kind == tarfile.REGTYPE: info.size = len(content)
                else: info.linkname = content.decode()
                tar.addfile(info, io.BytesIO(content) if kind == tarfile.REGTYPE else None)
        return path

    def test_archive_rejects_traversal_links_and_duplicate_overwrites(self):
        for entries in [[('../outside', tarfile.REGTYPE, b'x')], [('release/link', tarfile.SYMTYPE, b'/etc/passwd')], [('release/link', tarfile.LNKTYPE, b'release/file')], [('release/a',tarfile.REGTYPE,b'a'),('release/a',tarfile.REGTYPE,b'b')]]:
            with self.assertRaises(ValueError): agent.extract(self.archive(entries), self.root / 'out', 'release')
            shutil.rmtree(self.root/'out', ignore_errors=True)
        self.assertFalse((self.root/'outside').exists())

    def test_outer_checksum_rejected_before_extract(self):
        def fetch(url,path,maximum):
            if path.name=='SHA256SUMS':path.write_text('0'*64+'  '+agent.asset_name('0.19.0','lite')+'\n')
            else:path.write_bytes(b'corrupt')
        with patch.object(agent,'download',side_effect=fetch),patch.object(agent,'extract') as extract:
            with self.assertRaises(ValueError):agent.fetch_bundle('0.19.0','lite',self.root)
            extract.assert_not_called()

    def test_rollback_schema_gate_precedes_any_mutation(self):
        with patch.object(state,'candidates',return_value=[{'version':'0.17.0','compatible':False}]),patch.object(upgrade,'transaction') as mutation:
            with self.assertRaises(ValueError):upgrade.rollback('0.17.0')
            mutation.assert_not_called()

    def test_corrupted_backup_is_rejected_before_replacing_current_app(self):
        old=self.root/'backup';(old/'app').mkdir(parents=True);(old/'app/VERSION').write_text('0.17.0')
        (old/'app-checksums.json').write_text(json.dumps(upgrade.app_hashes(old/'app')))
        (old/'app/VERSION').write_text('0.16.0')
        with patch.object(state,'candidates',return_value=[{'version':'0.17.0','compatible':True,'backup':str(old)}]),patch.object(upgrade,'transaction') as mutation:
            with self.assertRaises(ValueError):upgrade.rollback('0.17.0')
            mutation.assert_not_called()
        self.assertEqual(state.current(),'0.18.0')

    def test_manual_rollback_keeps_current_configuration_database_and_baseline(self):
        old = self.root/'backup';(old/'app').mkdir(parents=True);(old/'app/VERSION').write_text('0.17.0')
        (old/'app-checksums.json').write_text(json.dumps(upgrade.app_hashes(old/'app')))
        original = self.config.read_bytes(); database = (self.data/'panel.db').read_bytes()
        def run_change(change,target,config,progress):change(self.root/'newbackup')
        with patch.object(state,'candidates',return_value=[{'version':'0.17.0','compatible':True,'backup':str(old)}]),patch.object(upgrade,'transaction',side_effect=run_change),patch.object(upgrade,'restore_units'):
            upgrade.rollback('0.17.0')
        self.assertEqual(self.config.read_bytes(),original);self.assertEqual((self.data/'panel.db').read_bytes(),database);self.assertEqual(state.baseline()['version'],'0.17.0')

    def test_interrupted_operation_recovers_from_root_journal(self):
        state.write('operation.json',dict(self.request(),stage='installing'))
        state.write('transaction.json',{'backup':'/root/guangyue-backups/upgrade-fixture','target':'0.19.0','phase':'changing'})
        from contextlib import nullcontext
        with patch.object(agent,'deployment_lock',return_value=nullcontext()),patch.object(upgrade,'restore_current') as restore:
            agent.recover();restore.assert_called_once_with(Path('/root/guangyue-backups/upgrade-fixture'))
        self.assertIsNone(state.read('transaction.json'));self.assertEqual(state.read('operation.json')['stage'],'failed');self.assertEqual(state.baseline()['version'],'0.17.0')

    def test_interruption_before_backup_restarts_and_checks_original_version(self):
        from contextlib import nullcontext
        state.write('operation.json',dict(self.request(),stage='backing_up'))
        state.write('transaction.json',{'target':'0.19.0','previous':'0.18.0','phase':'preparing'})
        with patch.object(agent,'deployment_lock',return_value=nullcontext()),patch.object(upgrade,'run') as run,patch.object(upgrade,'health') as health,patch.object(upgrade,'restore_current') as restore:
            agent.recover();run.assert_called_once_with('systemctl','start',*upgrade.UNITS);health.assert_called_once_with('0.18.0');restore.assert_not_called()
        self.assertIsNone(state.read('transaction.json'));self.assertEqual(state.read('operation.json')['stage'],'failed')

    def test_symlink_cannot_redirect_root_state_write(self):
        victim=self.root/'victim';victim.write_text('original');(state.ROOT/'operation.json.new').symlink_to(victim)
        with self.assertRaises(OSError):state.write('operation.json',{})
        self.assertEqual(victim.read_text(),'original')

    def recovery_fixture(self):
        # Resolve the host temporary root so macOS /var aliases are not mistaken
        # for symlinks in the privileged Linux deployment paths under test.
        root = self.root.resolve()
        helper = root / 'helper'; helper.mkdir(mode=0o755)
        (helper / 'infrastructure.py').write_bytes(b'old recovery module\n')
        (helper / 'VERSION').write_text('0.35.0\n')
        (helper / 'update_agent.py').write_text('unchanged helper\n')
        bundle = root / 'release'; (bundle / 'deploy').mkdir(parents=True)
        (bundle / 'bin').mkdir(); (bundle / 'web').mkdir()
        files = {'VERSION': b'0.35.1\n', 'deploy/infrastructure.py': b'new recovery module\n',
                 'bin/guangyue-linux-amd64': b'panel', 'bin/hysteria-node-linux-amd64': b'hy2',
                 'bin/xray-linux-amd64': b'xray', 'bin/mihomo-linux-amd64': b'mihomo', 'web/index.html': b'web'}
        for name, content in files.items(): (bundle / name).write_bytes(content)
        (bundle / 'SHA256SUMS').write_text(''.join(hashlib.sha256(content).hexdigest() + '  ' + name + '\n' for name, content in files.items()))
        for obj, key, value in [(state, 'HELPER', helper), (state, 'ROOT', root / 'updates'),
                                (install, 'UPSTREAM_HASHES', {name: hashlib.sha256(name.encode()).hexdigest() for name in ['xray', 'mihomo']})]:
            p = patch.object(obj, key, value); p.start(); self.addCleanup(p.stop)
        return helper, bundle

    def test_recovery_prime_verifies_backs_up_and_replaces_only_infrastructure(self):
        helper, bundle = self.recovery_fixture()
        before = {name: (state.ROOT / name).read_bytes() for name in ['installation.json']}
        original_config = self.config.read_bytes()
        with patch.object(upgrade, 'run') as commands, patch.object(upgrade.os, 'fsync', wraps=os.fsync) as sync:
            backup = upgrade.prime_helper_recovery(bundle)
        self.assertEqual((helper / 'infrastructure.py').read_bytes(), b'new recovery module\n')
        self.assertEqual((backup / 'infrastructure.py').read_bytes(), b'old recovery module\n')
        self.assertEqual((helper / 'infrastructure.py').stat().st_mode & 0o777, 0o644)
        self.assertEqual(backup.stat().st_mode & 0o777, 0o700)
        self.assertEqual((backup / 'infrastructure.py').stat().st_mode & 0o777, 0o600)
        manifest = json.loads((backup / 'manifest.json').read_text())
        self.assertEqual(manifest['installed_sha256'], hashlib.sha256(b'new recovery module\n').hexdigest())
        self.assertGreaterEqual(sync.call_count, 6)
        self.assertEqual([call.args for call in commands.call_args_list], [('systemctl', 'stop', 'guangyue-updater.socket', 'guangyue-updater.service'), ('systemctl', 'start', 'guangyue-updater.socket')])
        self.assertEqual((helper / 'VERSION').read_text(), '0.35.0\n')
        self.assertEqual((helper / 'update_agent.py').read_text(), 'unchanged helper\n')
        self.assertEqual(self.config.read_bytes(), original_config)
        for name, content in before.items(): self.assertEqual((state.ROOT / name).read_bytes(), content)

    def test_recovery_prime_rejects_corrupt_bundle_before_stopping_service(self):
        helper, bundle = self.recovery_fixture()
        (bundle / 'deploy/infrastructure.py').write_bytes(b'tampered')
        with patch.object(upgrade, 'run') as commands, self.assertRaises(ValueError):
            upgrade.prime_helper_recovery(bundle)
        commands.assert_not_called()
        self.assertEqual((helper / 'infrastructure.py').read_bytes(), b'old recovery module\n')
        self.assertFalse((state.ROOT / 'recovery').exists())

    def test_recovery_prime_refuses_active_or_unrecognized_operations(self):
        helper, bundle = self.recovery_fixture()
        cases = [('transaction.json', {'phase': 'changing'}), ('operation.json', {'stage': 'queued'}),
                 ('operation.json', {'stage': 'installing'}), ('operation.json', {'stage': 'future-active-stage'}),
                 ('network-operation.json', {'stage': 'applying'})]
        for name, value in cases:
            with self.subTest(name=name, value=value):
                state.write(name, value)
                with patch.object(upgrade, 'run') as commands, self.assertRaises(ValueError):
                    upgrade.prime_helper_recovery(bundle)
                commands.assert_not_called(); self.assertEqual(state.read(name), value)
                state.write(name, None)
        self.assertEqual((helper / 'infrastructure.py').read_bytes(), b'old recovery module\n')

    def test_recovery_prime_retains_a_concurrent_queue_and_restores_socket(self):
        helper, bundle = self.recovery_fixture()
        pending = {'stage': 'queued', 'request_id': 'racing-request'}
        def run(*args):
            if args[1] == 'stop': state.write('operation.json', pending)
        with patch.object(upgrade, 'run', side_effect=run) as commands, self.assertRaises(ValueError):
            upgrade.prime_helper_recovery(bundle)
        self.assertEqual(commands.call_args.args, ('systemctl', 'start', 'guangyue-updater.socket'))
        self.assertEqual(state.read('operation.json'), pending)
        self.assertEqual((helper / 'infrastructure.py').read_bytes(), b'old recovery module\n')
        self.assertFalse((state.ROOT / 'recovery').exists())

    def test_recovery_prime_rejects_symlinks_and_writable_helper_directory(self):
        helper, bundle = self.recovery_fixture()
        original = helper / 'infrastructure.py'
        victim = self.root / 'victim'; victim.write_bytes(b'unchanged')
        original.unlink(); original.symlink_to(victim)
        with patch.object(upgrade, 'run') as commands, self.assertRaises(OSError): upgrade.prime_helper_recovery(bundle)
        commands.assert_not_called(); self.assertEqual(victim.read_bytes(), b'unchanged')
        original.unlink(); original.write_bytes(b'old recovery module\n'); helper.chmod(0o777)
        with patch.object(upgrade, 'run') as commands, self.assertRaises(ValueError): upgrade.prime_helper_recovery(bundle)
        commands.assert_not_called()
        helper.chmod(0o755)
        redirected = helper.with_name('helper-link'); redirected.symlink_to(helper, target_is_directory=True)
        with patch.object(state, 'HELPER', redirected), patch.object(upgrade, 'run') as commands, self.assertRaises(ValueError): upgrade.prime_helper_recovery(bundle)
        commands.assert_not_called()

    def test_recovery_prime_preserves_old_module_if_atomic_replace_fails(self):
        helper, bundle = self.recovery_fixture()
        replace = os.replace
        def fail_target(source, target):
            if target == helper / 'infrastructure.py': raise OSError('disk error')
            return replace(source, target)
        with patch.object(upgrade.os, 'replace', side_effect=fail_target), patch.object(upgrade, 'run') as commands, self.assertRaises(OSError):
            upgrade.prime_helper_recovery(bundle)
        self.assertEqual(commands.call_args.args, ('systemctl', 'start', 'guangyue-updater.socket'))
        self.assertEqual((helper / 'infrastructure.py').read_bytes(), b'old recovery module\n')
        backup = next((state.ROOT / 'recovery').iterdir())
        self.assertEqual((backup / 'infrastructure.py').read_bytes(), b'old recovery module\n')
        self.assertFalse(list(helper.glob('.infrastructure.py-*')))

    def test_recovery_prime_does_not_downgrade_a_newer_helper(self):
        helper, bundle = self.recovery_fixture(); (helper / 'VERSION').write_text('0.36.0\n')
        with patch.object(upgrade, 'run') as commands, self.assertRaises(ValueError): upgrade.prime_helper_recovery(bundle)
        commands.assert_not_called()
        self.assertEqual((helper / 'infrastructure.py').read_bytes(), b'old recovery module\n')

    def test_recovery_prime_rejects_redirected_journal_and_recovery_directory(self):
        helper, bundle = self.recovery_fixture()
        victim = self.root / 'journal-victim'; victim.write_text('null')
        (state.ROOT / 'transaction.json').symlink_to(victim)
        with patch.object(upgrade, 'run') as commands, self.assertRaises(OSError): upgrade.prime_helper_recovery(bundle)
        commands.assert_not_called(); self.assertEqual(victim.read_text(), 'null')
        (state.ROOT / 'transaction.json').unlink()
        outside = self.root / 'outside'; outside.mkdir()
        (state.ROOT / 'recovery').symlink_to(outside, target_is_directory=True)
        with patch.object(upgrade, 'run') as commands, self.assertRaises(ValueError): upgrade.prime_helper_recovery(bundle)
        self.assertEqual(commands.call_args.args, ('systemctl', 'start', 'guangyue-updater.socket'))
        self.assertEqual(list(outside.iterdir()), [])
        self.assertEqual((helper / 'infrastructure.py').read_bytes(), b'old recovery module\n')

    def test_recovery_prime_restores_socket_if_stopping_helper_fails(self):
        helper, bundle = self.recovery_fixture()
        def fail_stop(*args):
            if args[1] == 'stop': raise OSError('systemd failure')
        with patch.object(upgrade, 'run', side_effect=fail_stop) as commands, self.assertRaises(OSError): upgrade.prime_helper_recovery(bundle)
        self.assertEqual(commands.call_args.args, ('systemctl', 'start', 'guangyue-updater.socket'))
        self.assertEqual((helper / 'infrastructure.py').read_bytes(), b'old recovery module\n')
        self.assertFalse((state.ROOT / 'recovery').exists())

    def test_recovery_prime_reloads_matching_module_without_another_backup(self):
        helper, bundle = self.recovery_fixture()
        (helper / 'infrastructure.py').write_bytes((bundle / 'deploy/infrastructure.py').read_bytes())
        with patch.object(upgrade, 'run') as commands: self.assertIsNone(upgrade.prime_helper_recovery(bundle))
        self.assertEqual(commands.call_count, 2)
        self.assertFalse((state.ROOT / 'recovery').exists())

    def test_recovery_prime_leaves_an_installation_without_a_helper_untouched(self):
        helper, bundle = self.recovery_fixture(); shutil.rmtree(helper)
        with patch.object(upgrade, 'run') as commands: self.assertIsNone(upgrade.prime_helper_recovery(bundle))
        commands.assert_not_called(); self.assertFalse(helper.exists())
        self.assertFalse((state.ROOT / 'recovery').exists())

    def test_upgrade_dry_run_never_primes_or_changes_helper(self):
        helper, bundle = self.recovery_fixture()
        with patch.object(sys, 'argv', ['upgrade.py', '--bundle', str(bundle)]), patch.object(upgrade, 'preflight'), patch.object(upgrade, 'edition_config'), patch.object(upgrade, 'prime_helper_recovery') as prime, patch.object(upgrade, 'upgrade') as apply, patch.object(upgrade, 'deployment_lock') as lock:
            upgrade.main()
        prime.assert_not_called(); apply.assert_not_called(); lock.assert_not_called()
        self.assertEqual((helper / 'infrastructure.py').read_bytes(), b'old recovery module\n')
        self.assertFalse((state.ROOT / 'recovery').exists())

    def test_upgrade_apply_holds_lock_across_verification_priming_and_upgrade(self):
        helper, bundle = self.recovery_fixture(); held = []
        @contextmanager
        def locked():
            held.append(True)
            try: yield
            finally: held.pop()
        def verify(_bundle): self.assertTrue(held)
        def apply(*args):
            self.assertTrue(held)
            self.assertEqual((helper / 'infrastructure.py').read_bytes(), b'new recovery module\n')
        with patch.object(sys, 'argv', ['upgrade.py', '--bundle', str(bundle), '--apply']), patch.object(upgrade, 'deployment_lock', side_effect=locked), patch.object(upgrade, 'preflight', side_effect=verify), patch.object(upgrade, 'edition_config'), patch.object(upgrade, 'run'), patch.object(upgrade, 'upgrade', side_effect=apply) as mutation:
            upgrade.main()
        mutation.assert_called_once(); self.assertFalse(held)

    def test_upgrade_apply_lock_contention_never_stops_an_active_update(self):
        helper, bundle = self.recovery_fixture()
        @contextmanager
        def busy_lock():
            raise ValueError('another install, upgrade or certificate deployment is running')
            yield
        with patch.object(sys, 'argv', ['upgrade.py', '--bundle', str(bundle), '--apply']), patch.object(upgrade, 'deployment_lock', side_effect=busy_lock), patch.object(upgrade, 'preflight') as verify, patch.object(upgrade, 'run') as commands, patch.object(upgrade, 'upgrade') as mutation, self.assertRaises(ValueError):
            upgrade.main()
        verify.assert_not_called(); commands.assert_not_called(); mutation.assert_not_called()
