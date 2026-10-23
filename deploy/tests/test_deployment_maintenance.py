import importlib.util
import hashlib
import io
import json
import os
import shutil
import runpy
import stat
import subprocess
import sys
import tempfile
import types
import unittest
from pathlib import Path
from unittest.mock import patch
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import deployment_mode
import gateway_certificate as certificate
import update_agent
import upgrade
import maintenance_gate

spec = importlib.util.spec_from_file_location('release_merge', Path(__file__).resolve().parents[2] / 'scripts/merge-release-assets.py')
release_merge = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release_merge)


class MaintenanceTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(); self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()

    def test_identity_rejects_fifo_without_waiting_for_writer(self):
        fifo = self.root / 'marker'; os.mkfifo(fifo, 0o600)
        with patch.object(deployment_mode, 'MARKER', fifo), self.assertRaises(ValueError):
            deployment_mode.specification()

    def test_private_gateway_units_include_ingress_in_stopped_snapshot(self):
        with patch.object(deployment_mode, 'private_gateway', return_value=True):
            self.assertEqual(upgrade.managed_units(), [*upgrade.UNITS, 'guangyue-gateway'])
            with patch.object(upgrade, 'run') as command:
                upgrade.upgrade_panel_proxy()
                command.assert_not_called()

    def test_isolated_certificate_entry_disables_cache_before_local_imports(self):
        helper = self.root / 'certificate-helper'; helper.mkdir()
        for name in ['gateway_certificate.py', 'certificates.py', 'common.py', 'deployment_mode.py', 'update_state.py']:
            shutil.copyfile(Path(upgrade.__file__).parent / name, helper / name)
        # Execute the actual entry source without its root-only CLI mutation.
        # -I ignores PYTHONDONTWRITEBYTECODE on regular CPython; explicit code
        # must disable writes before any of these local imports occurs.
        code = 'import runpy,sys; runpy.run_path(sys.argv[1], run_name="certificate_fixture"); assert sys.dont_write_bytecode'
        result = subprocess.run([sys.executable, '-I', '-c', code, str(helper / 'gateway_certificate.py')], capture_output=True, env=dict(os.environ, PYTHONDONTWRITEBYTECODE='1'), timeout=15)
        self.assertEqual(result.returncode, 0)
        self.assertFalse((helper / '__pycache__').exists())

    def test_native_legacy_cache_fixture_is_protected_even_with_group_writable_umask(self):
        fixture = Path(upgrade.__file__).parent / 'tests/native_oneclick_integration.py'
        with patch.object(os, 'geteuid', return_value=0), patch.dict(os.environ, {'GITHUB_ACTIONS': 'true', 'GY_ONECLICK_INTEGRATION': '1'}):
            functions = runpy.run_path(str(fixture), run_name='native_fixture_unit')
        helper = self.root / 'native-cache-fixture'; helper.mkdir(mode=0o755)
        previous = os.umask(0o002)
        try:
            cache = functions['legacy_cache_fixture'](helper)
        finally:
            os.umask(previous)
        self.assertEqual(stat.S_IMODE(cache.stat().st_mode), 0o700)
        self.assertEqual(stat.S_IMODE((cache / 'common.cpython-312.pyc').stat().st_mode), 0o600)
        self.assertEqual(upgrade._helper_source_files(helper), [])

    def native_fixture_functions(self):
        source = Path(upgrade.__file__).parent / 'tests/native_oneclick_integration.py'
        with patch.object(os, 'geteuid', return_value=0), patch.dict(os.environ, {'GITHUB_ACTIONS': 'true', 'GY_ONECLICK_INTEGRATION': '1'}):
            return runpy.run_path(str(source), run_name='native_fixture_unit')

    def test_native_reality_override_rejects_either_missing_guard_without_side_effects(self):
        functions = self.native_fixture_functions()
        import native_gateway
        for github, integration in [('false', '1'), ('true', ''), ('false', '')]:
            with self.subTest(github=github, integration=integration), patch.object(os, 'geteuid', return_value=0), patch.dict(os.environ, {'GITHUB_ACTIONS': github, 'GY_ONECLICK_INTEGRATION': integration}), patch.object(native_gateway, 'install') as install:
                with self.assertRaisesRegex(ValueError, 'fixture controls'):
                    functions['start_reality_fixture'](self.root)
                with self.assertRaisesRegex(ValueError, 'fixture controls'):
                    functions['install_fixture']({}, self.root, cert_file=self.root/'cert', key_file=self.root/'key', reality_target='127.0.0.1:12345')
                install.assert_not_called()
                self.assertFalse((self.root/'reality-target').exists())

    def test_native_reality_fixture_changes_only_its_config_target_and_preserves_sni(self):
        functions = self.native_fixture_functions()
        import native_gateway
        original = {'reality_sni': 'www.cloudflare.com', 'reality_target': 'www.cloudflare.com:443'}
        def installing(*args, **kwargs):
            native_gateway.atomic_json(native_gateway.CONFIG, original, 0o640, 'guangyue')
            native_gateway.atomic_json(self.root/'unrelated.json', original)
            return 'fixture-installed'
        with patch.object(os, 'geteuid', return_value=0), patch.dict(os.environ, {'GITHUB_ACTIONS': 'true', 'GY_ONECLICK_INTEGRATION': '1'}), patch.object(native_gateway, 'atomic_json') as write, patch.object(native_gateway, 'install', side_effect=installing) as install:
            self.assertEqual(functions['install_fixture']({}, self.root, cert_file=self.root/'cert', key_file=self.root/'key', reality_target='127.0.0.1:12345'), 'fixture-installed')
            self.assertEqual(write.call_args_list[0].args[1], {'reality_sni': 'www.cloudflare.com', 'reality_target': '127.0.0.1:12345'})
            self.assertEqual(write.call_args_list[1].args[1], original)
            self.assertEqual(set(install.call_args.kwargs), {'cert_file', 'key_file'})
            for target in ['www.cloudflare.com:443', '0.0.0.0:12345', '127.0.0.1:443', '127.0.0.1:65536']:
                with self.assertRaises(ValueError): functions['checked_loopback_target'](target)
        self.assertEqual(original['reality_target'], 'www.cloudflare.com:443')

    def test_certificate_wait_does_not_treat_activating_unapplied_or_unready_child_as_done(self):
        functions = self.native_fixture_functions(); wait = functions['wait_certificate_reconciled']
        configuration = self.root/'native-config.json'; configuration.write_text(json.dumps({'stats_secret': 'private-fixture'}))
        working = {'ActiveState': 'activating', 'SubState': 'start', 'MainPID': '99'}
        finished = {'ActiveState': 'inactive', 'SubState': 'dead', 'MainPID': '0'}
        child = {'ActiveState': 'active', 'SubState': 'running', 'MainPID': '42'}
        workers = iter([working, finished, finished, finished])
        def service(unit):
            return next(workers) if unit == 'guangyue-gateway-cert.service' else child
        systems = [{'status': 'applied', 'configuration': {'state': 'applied'}}, {'status': 'starting', 'configuration': {'state': 'pending'}}, {'status': 'applied', 'configuration': {'state': 'applied'}}, {'status': 'applied', 'configuration': {'state': 'applied'}}]
        import native_gateway
        with patch.object(native_gateway, 'CONFIG', configuration), patch.object(update_agent.state, 'read', return_value={'digest': 'generation'}) as read, patch.dict(wait.__globals__, {'service_state': service, 'api': unittest.mock.Mock(side_effect=[(dict(system=value), '') for value in systems]), 'hy2_tls_cache_ready': unittest.mock.Mock(side_effect=[False, True])}), patch.object(wait.__globals__['time'], 'monotonic', side_effect=[0, 1, 2, 3, 4]), patch.object(wait.__globals__['time'], 'sleep') as pause:
            wait('generation', b'41', cookie='private-fixture-cookie')
            self.assertEqual(read.call_count, 4)
            self.assertEqual(pause.call_count, 3)
            self.assertEqual(wait.__globals__['hy2_tls_cache_ready'].call_count, 2)

    def test_certificate_wait_rechecks_same_child_pid_after_authenticated_stats(self):
        functions = self.native_fixture_functions(); wait = functions['wait_certificate_reconciled']
        configuration = self.root/'native-config.json'; configuration.write_text(json.dumps({'stats_secret': 'private-fixture'}))
        finished = {'ActiveState': 'inactive', 'SubState': 'dead', 'MainPID': '0'}
        generations = iter(['42', '43', '43', '43'])
        def service(unit):
            return finished if unit == 'guangyue-gateway-cert.service' else {'ActiveState': 'active', 'SubState': 'running', 'MainPID': next(generations)}
        import native_gateway
        with patch.object(native_gateway, 'CONFIG', configuration), patch.object(update_agent.state, 'read', return_value={'digest': 'generation'}) as read, patch.dict(wait.__globals__, {'service_state': service, 'api': unittest.mock.Mock(return_value=(dict(system={'status': 'applied', 'configuration': {'state': 'applied'}}), '')), 'hy2_tls_cache_ready': unittest.mock.Mock(return_value=True)}), patch.object(wait.__globals__['time'], 'monotonic', side_effect=[0, 1, 2]), patch.object(wait.__globals__['time'], 'sleep') as pause:
            wait('generation', b'41', cookie='private-fixture-cookie')
            self.assertEqual(read.call_count, 2)
            self.assertEqual(pause.call_count, 1)

    def test_proxy_fixture_diagnostic_has_transport_context_without_credentials(self):
        source = Path(upgrade.__file__).parent / 'tests/entitlement_integration.py'
        module_spec = importlib.util.spec_from_file_location('entitlement_fixture_diagnostic', source)
        fixture = importlib.util.module_from_spec(module_spec); module_spec.loader.exec_module(fixture)
        with patch('sys.stderr', new_callable=io.StringIO) as output:
            fixture.proxy_diagnostic('tcp_echo', 'vless', 19891, 0, 32768, 65536, types.SimpleNamespace(poll=lambda: None), TimeoutError('password=private-secret uri=private-link'))
            text = output.getvalue()
        row = json.loads(text.removeprefix('CI proxy failure '))
        self.assertEqual(row['protocol'], 'vless')
        self.assertEqual(row['received_bytes'], 32768)
        self.assertEqual(row['client_exit'], 'running')
        self.assertEqual(row['exception'], 'TimeoutError')
        self.assertNotIn('private-secret', text); self.assertNotIn('private-link', text)

    def helper_restore_fixture(self):
        app, data, helper, units = (self.root / name for name in ['app', 'state', 'helper', 'units'])
        for directory in (app, data, helper, units): directory.mkdir(mode=0o755)
        (app / 'VERSION').write_text('0.41.1\n')
        (app / 'program').write_text('original application\n')
        (data / 'counter').write_text('original data\n')
        (helper / 'VERSION').write_text('0.41.1\n')
        (helper / 'gateway_certificate.py').write_text('original helper\n')
        cache = helper / '__pycache__'; cache.mkdir(mode=0o755)
        (cache / 'common.cpython-312.pyc').write_bytes(b'old compiled cache')
        (helper / 'old.pyc').write_bytes(b'legacy flat cache')
        config = self.root / 'config.json'; config.write_text(json.dumps({'database': {'driver': 'sqlite'}}))
        for name in [*(unit + '.service' for unit in upgrade.UNITS), 'guangyue-gateway.service', 'guangyue-updater.socket', 'guangyue-updater.service', 'guangyue-gateway-cert.path', 'guangyue-gateway-cert.service', 'guangyue-gateway-cert.timer']:
            (units / name).write_text('owned unit fixture\n')
        return app, data, helper, units, config

    def test_real_backup_and_restore_handle_only_protected_legacy_python_caches(self):
        app, data, helper, units, config = self.helper_restore_fixture()
        actual_copy = shutil.copy2
        def copy_owned(source, target, *args, **kwargs):
            def local(value):
                text = str(value)
                return units / Path(text).name if text.startswith('/etc/systemd/system/') else value
            return actual_copy(local(source), local(target), *args, **kwargs)
        def owned_path(value):
            if str(value) == '/root/guangyue-backups': return self.root / 'backups'
            return Path(value)
        with patch.object(upgrade, 'APP', app), patch.object(upgrade, 'STATE', data), patch.object(upgrade, 'CONFIG', config), patch.object(upgrade.updates, 'HELPER', helper), patch.object(upgrade, 'NGINX_PANEL', self.root / 'no-nginx-panel'), patch.object(deployment_mode, 'private_gateway', return_value=True), patch.object(upgrade, 'Path', side_effect=owned_path), patch.object(upgrade.backup_retention, 'BACKUPS', self.root / 'backups'), patch.object(upgrade.shutil, 'copy2', side_effect=copy_owned), patch.object(upgrade.shutil, 'chown'), patch.object(upgrade, 'restore_units'), patch.object(upgrade, 'run'), patch.object(upgrade, 'health') as health:
            backup = upgrade.backup_current(json.loads(config.read_text()))
            self.assertFalse((backup / 'helper/__pycache__').exists())
            self.assertFalse(list((backup / 'helper').glob('*.pyc')))
            # Already-written private backups may contain caches from the old
            # entry point. Preserve their checksum verification, then skip only
            # validated cache entries during the actual restoration.
            shutil.copytree(helper / '__pycache__', backup / 'helper/__pycache__')
            actual_copy(helper / 'old.pyc', backup / 'helper/old.pyc')
            (backup / 'helper-checksums.json').write_text(json.dumps(upgrade.app_hashes(backup / 'helper')))
            (app / 'program').write_text('faulty update\n')
            (data / 'counter').write_text('uncommitted state\n')
            (helper / 'gateway_certificate.py').write_text('uncommitted helper\n')
            upgrade.restore_current(backup)
            health.assert_called_once_with('0.41.1')
        self.assertEqual((app / 'program').read_text(), 'original application\n')
        self.assertEqual((data / 'counter').read_text(), 'original data\n')
        self.assertEqual((helper / 'gateway_certificate.py').read_text(), 'original helper\n')
        self.assertFalse((helper / '__pycache__').exists())
        self.assertFalse((helper / 'old.pyc').exists())

    def test_helper_cache_filter_rejects_unknown_directories_links_and_writable_cache(self):
        for bad in ['unknown-directory', 'cache-link', 'cache-child-link', 'writable-cache', 'unknown-cache-file']:
            with self.subTest(bad=bad):
                helper = self.root / bad; helper.mkdir(mode=0o755)
                code = helper / 'helper.py'; code.write_text('protected code\n')
                cache = helper / '__pycache__'
                if bad == 'unknown-directory': (helper / 'unexpected').mkdir()
                elif bad == 'cache-link': cache.symlink_to(self.root, target_is_directory=True)
                else:
                    cache.mkdir(mode=0o755)
                    if bad == 'cache-child-link': (cache / 'module.pyc').symlink_to(code)
                    elif bad == 'writable-cache': cache.chmod(0o777)
                    else: (cache / 'not-a-python-cache.txt').write_text('preserve\n')
                with self.assertRaises(ValueError): upgrade._clear_helper_caches(helper)
                self.assertTrue(cache.exists() or bad == 'unknown-directory')
                self.assertEqual(code.read_text(), 'protected code\n')

    def test_committed_native_cleanup_failure_never_restores_precommit_data(self):
        config = self.root / 'config.json'; config.write_text('{}')
        journals = []
        def write(name, value):
            if name == 'transaction.json':
                journals.append(value)
                if value is None: raise OSError('simulated journal cleanup failure after commit')
        with patch.object(upgrade, 'CONFIG', config), patch.object(deployment_mode, 'private_gateway', return_value=True), patch.object(upgrade.updates, 'baseline'), patch.object(upgrade.updates, 'require_floor'), patch.object(upgrade.updates, 'current', return_value='0.41.0'), patch.object(upgrade.updates, 'schema', return_value='fixed'), patch.object(upgrade.updates, 'write', side_effect=write), patch.object(upgrade.updates, 'remember'), patch.object(upgrade, 'run'), patch.object(upgrade, 'backup_current', return_value=self.root / 'backup'), patch.object(upgrade, 'prepare_start'), patch.object(maintenance_gate, 'begin'), patch.object(maintenance_gate, 'finish') as finish, patch.object(upgrade, 'restore_current') as restore:
            with self.assertRaisesRegex(ValueError, '新版已提交'):
                upgrade.transaction(lambda _: None, '0.41.1', {})
            finish.assert_called_once()
            restore.assert_not_called()
            self.assertEqual(journals[-2]['phase'], 'committed')

    def test_native_update_refuses_to_replace_previous_recovery_journal(self):
        config = self.root / 'config.json'; config.write_text('{}')
        with patch.object(upgrade, 'CONFIG', config), patch.object(deployment_mode, 'private_gateway', return_value=True), patch.object(upgrade.updates, 'read', return_value={'phase': 'changing'}), patch.object(upgrade.updates, 'write') as write, patch.object(maintenance_gate, 'begin') as begin, patch.object(upgrade, 'run') as command:
            with self.assertRaisesRegex(ValueError, '前一次版本事务'):
                upgrade.transaction(lambda _: None, '0.41.1', {})
            write.assert_not_called(); begin.assert_not_called(); command.assert_not_called()

    def test_docker_recovery_is_serialized_and_closes_interrupted_operation(self):
        events = []
        from contextlib import contextmanager
        @contextmanager
        def locked():
            events.append('lock'); yield; events.append('unlock')
        adapter = types.SimpleNamespace(recover=lambda: events.append('recover'))
        with patch.object(deployment_mode, 'docker', return_value=True), patch.dict(sys.modules, {'docker_deploy': adapter}), patch.object(update_agent, 'deployment_lock', locked), patch.object(update_agent.state, 'read', return_value={'stage': 'installing'}), patch.object(update_agent.state, 'write') as write:
            update_agent.recover()
            self.assertEqual(events, ['lock', 'recover', 'unlock'])
            self.assertEqual(write.call_args.args[1]['stage'], 'failed')
        adapter.recover = lambda: self.fail('Concurrent recovery must not run')
        @contextmanager
        def busy():
            raise ValueError('another install, upgrade or certificate deployment is running')
            yield
        with patch.object(deployment_mode, 'docker', return_value=True), patch.dict(sys.modules, {'docker_deploy': adapter}), patch.object(update_agent, 'deployment_lock', busy), patch.object(update_agent.state, 'write') as write:
            update_agent.recover()
            write.assert_not_called()

    def cert_fixture(self):
        base = self.root / 'tls'; (base / 'generation').mkdir(parents=True)
        cert, key = base / 'generation/cert.pem', base / 'generation/key.pem'
        cert.write_bytes(b'cert'); key.write_bytes(b'key')
        config = self.root / 'config.json'
        config.write_text(json.dumps({'cert': str(cert), 'cert_key': str(key), 'hy2_host': 'node.example.com'}))
        return base, config

    def test_certificate_notification_does_not_start_disabled_core(self):
        base, config = self.cert_fixture()
        with patch.object(certificate, 'TLS_ROOT', base), patch.object(certificate.state, 'CONFIG', config), patch.object(deployment_mode, 'private_gateway', return_value=True), patch.object(deployment_mode, 'docker', return_value=False), patch.object(certificate.certificates, 'validate'), patch.object(certificate.state, 'read', return_value=None), patch.object(certificate.state, 'write') as write, patch.object(certificate.subprocess, 'run', return_value=subprocess.CompletedProcess([], 3)) as command:
            certificate.reconcile()
            self.assertEqual(command.call_count, 1)
            self.assertEqual(command.call_args.args[0], ['systemctl', 'is-active', '--quiet', 'guangyue-hy2'])
            self.assertIn('digest', write.call_args.args[1])

    def test_certificate_notification_rejects_redirected_base(self):
        base, config = self.cert_fixture()
        redirect = self.root / 'redirect'; redirect.symlink_to(base, target_is_directory=True)
        with patch.object(certificate, 'TLS_ROOT', redirect), patch.object(certificate.state, 'CONFIG', config), patch.object(deployment_mode, 'private_gateway', return_value=True), patch.object(deployment_mode, 'docker', return_value=False), patch.object(certificate.subprocess, 'run') as command, self.assertRaises(ValueError):
            certificate.reconcile()
        command.assert_not_called()

    def test_certificate_notification_restarts_only_hy2_and_deduplicates(self):
        base, config = self.cert_fixture()
        with patch.object(certificate, 'TLS_ROOT', base), patch.object(certificate.state, 'CONFIG', config), patch.object(deployment_mode, 'private_gateway', return_value=True), patch.object(deployment_mode, 'docker', return_value=False), patch.object(certificate.certificates, 'validate'), patch.object(certificate.state, 'read', return_value=None), patch.object(certificate.state, 'write') as write, patch.object(certificate.subprocess, 'run', return_value=subprocess.CompletedProcess([], 0)) as command:
            certificate.reconcile()
            self.assertEqual([c.args[0] for c in command.call_args_list], [['systemctl', 'is-active', '--quiet', 'guangyue-hy2'], ['systemctl', 'restart', 'guangyue-hy2'], ['systemctl', 'is-active', '--quiet', 'guangyue-hy2']])
            command.reset_mock()
            with patch.object(certificate.state, 'read', return_value=write.call_args.args[1]):
                certificate.reconcile()
            command.assert_not_called()

    def test_docker_catalog_requires_complete_uploaded_image_archive(self):
        data = [{'tag_name': 'v0.41.0', 'assets': [{'name': name, 'state': 'uploaded'} for name in ['SHA256SUMS', 'guangyue-panel-lite-0.41.0-linux-amd64.tar.gz']]}]
        def download(_url, destination, _maximum):
            Path(destination).write_text(json.dumps(data))
        with patch.object(update_agent.state, 'ROOT', self.root), patch.object(update_agent.state, 'read', return_value={'checked_at': 0, 'releases': []}), patch.object(update_agent.state, 'write'), patch.object(update_agent, 'download', side_effect=download), patch.object(deployment_mode, 'docker', return_value=True):
            self.assertEqual(update_agent.catalog(True)['releases'], [])
            data[0]['assets'] += [{'name': n, 'state': 'uploaded'} for n in ['DOCKER_IMAGES.json', 'guangyue-panel-docker-0.41.0-linux-amd64.tar.gz']]
            self.assertEqual(update_agent.catalog(True)['releases'][0]['version'], '0.41.0')

    def test_release_merge_rejects_corruption_and_checks_image_metadata(self):
        native, docker, dest = self.root / 'native', self.root / 'docker', self.root / 'merged'
        n, d = release_merge.names('0.41.0')
        for directory, expected in [(native, n), (docker, d)]:
            directory.mkdir()
            for name in expected: (directory / name).write_bytes(name.encode())
        archive = 'guangyue-panel-docker-0.41.0-linux-amd64.tar.gz'
        digest = hashlib.sha256((docker / archive).read_bytes()).hexdigest()
        (docker / 'DOCKER_IMAGES.json').write_text(json.dumps({'schema': 1, 'version': '0.41.0', 'platform': 'linux/amd64', 'archive': archive, 'sha256': digest}))
        for directory, expected in [(native, n), (docker, d)]:
            (directory / 'SHA256SUMS').write_text(''.join(hashlib.sha256((directory / name).read_bytes()).hexdigest() + '  ' + name + '\n' for name in sorted(expected)))
        release_merge.merge('0.41.0', native, docker, dest)
        self.assertEqual(len(list(dest.iterdir())), 8)
        release_merge.verify(dest, n | d)
        (native / 'install.sh').write_bytes(b'corrupt')
        with self.assertRaises(ValueError): release_merge.merge('0.41.0', native, docker, self.root / 'rejected')


if __name__ == '__main__': unittest.main()
