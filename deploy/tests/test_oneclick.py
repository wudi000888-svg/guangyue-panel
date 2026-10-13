import errno
import hashlib
import io
import json
import os
import socket
import sys
import tarfile
import tempfile
import unittest
import uuid
import contextlib
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch, Mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import oneclick
import native_gateway


def plan():
    return {'schema': 1, 'id': str(uuid.uuid4()), 'version': '0.41.0', 'edition': 'lite', 'backend': 'native', 'panel_domain': 'panel.example.com', 'node_domain': 'node.example.com', 'reality_sni': 'www.cloudflare.com', 'site_id': 'default', 'acme_agreed': True, 'created_at': 1}


def release(version='0.41.0', **kwargs):
    names = [oneclick.asset_name(version, 'lite'), oneclick.asset_name(version, 'pro'), 'SHA256SUMS', 'SBOM.spdx.json', 'guangyue-installer-' + version + '.tar.gz', 'install.sh', 'DOCKER_IMAGES.json', 'guangyue-panel-docker-' + version + '-linux-amd64.tar.gz']
    return dict({'tag_name': 'v' + version, 'draft': False, 'prerelease': False, 'assets': [{'name': n, 'size': 1, 'browser_download_url': oneclick.DOWNLOAD + 'v' + version + '/' + n} for n in names]}, **kwargs)


class OneclickDownloadTests(unittest.TestCase):
    def test_interrupted_download_residue_does_not_block_or_get_truncated_by_resume(self):
        response = io.BytesIO(b'complete')
        response.headers = {'Content-Length': '8'}
        opener = Mock(); opener.open.return_value = response
        with tempfile.TemporaryDirectory() as directory, patch.object(oneclick.urllib.request, 'build_opener', return_value=opener), contextlib.redirect_stdout(io.StringIO()):
            target = Path(directory) / 'package.tar.gz'
            residue = target.with_name(target.name + '.part'); residue.write_bytes(b'previous interrupted attempt')
            oneclick.download('https://github.com/asset', target, 100)
            self.assertEqual(target.read_bytes(), b'complete')
            self.assertEqual(residue.read_bytes(), b'previous interrupted attempt')
            self.assertEqual({p.name for p in Path(directory).iterdir()}, {'package.tar.gz', 'package.tar.gz.part'})

    def test_slow_download_progress_has_only_file_and_size_never_signed_url(self):
        class Response:
            headers = {'Content-Length': str(2 << 20)}
            def __init__(self): self.blocks = iter([b'x' * (1 << 20), b'y' * (1 << 20), b''])
            def __enter__(self): return self
            def __exit__(self, *args): return False
            def read(self, count): return next(self.blocks)
        opener = Mock(); opener.open.return_value = Response()
        output = io.StringIO()
        with tempfile.TemporaryDirectory() as directory, patch.object(oneclick.urllib.request, 'build_opener', return_value=opener), patch.object(oneclick.time, 'monotonic', side_effect=[0, 21, 42]), contextlib.redirect_stdout(output):
            target = Path(directory) / 'guangyue-package.tar.gz'
            oneclick.download('https://release-assets.githubusercontent.com/archive?X-Amz-Signature=DO_NOT_PRINT', target, 3 << 20)
            self.assertEqual(target.stat().st_size, 2 << 20)
        self.assertIn('guangyue-package.tar.gz', output.getvalue())
        self.assertIn('1.0 MiB', output.getvalue())
        self.assertIn('2.0 MiB', output.getvalue())
        self.assertNotIn('https://', output.getvalue())
        self.assertNotIn('DO_NOT_PRINT', output.getvalue())
        self.assertNotIn('X-Amz', output.getvalue())

    def test_official_redirects_only(self):
        for url in ('http://github.com/x', 'https://github.com.evil.example/x', 'https://evil.githubusercontent.com/x', 'https://user:pass@github.com/x', 'https://github.com:444/x'):
            with self.subTest(url=url), self.assertRaises(ValueError):
                oneclick.assert_trusted_download(url)
        oneclick.assert_trusted_download('https://release-assets.githubusercontent.com/a?token=signed')

    def test_release_selection_excludes_draft_prerelease_and_partial(self):
        partial = release('0.41.2'); partial['assets'].pop()
        values = [release('0.41.4', draft=True), release('0.41.3', prerelease=True), partial, release()]
        def download(url, path, maximum):
            Path(path).write_text(json.dumps(values))
        with patch.object(oneclick, 'download', side_effect=download):
            self.assertEqual(oneclick.release_catalog()['version'], '0.41.0')

    def test_release_rejects_foreign_asset_even_with_matching_name(self):
        value = release(); value['assets'][0]['browser_download_url'] = 'https://evil.example/asset'
        with patch.object(oneclick, 'download', side_effect=lambda url, path, limit: Path(path).write_text(json.dumps(value))):
            with self.assertRaisesRegex(ValueError, '项目不匹配'):
                oneclick.release_catalog('0.41.0')

    def test_manifest_rejects_duplicate_escape_and_unverified_names(self):
        good = 'a' * 64 + '  package.tar.gz'
        self.assertEqual(oneclick.manifest_rows(good)['package.tar.gz'], 'a' * 64)
        for value in (good + '\n' + good, 'a' * 64 + '  ../evil', 'a' * 64 + '  /etc/evil', 'bad package'):
            with self.assertRaises(ValueError):
                oneclick.manifest_rows(value)

    def test_asset_checksum_and_cached_file_are_always_verified(self):
        content = b'checked-release'
        digest = hashlib.sha256(content).hexdigest()
        calls = []
        def fetch(url, path, limit):
            calls.append(url)
            Path(path).write_bytes((digest + '  package.tar.gz\n').encode() if url.endswith('SHA256SUMS') else content)
        with tempfile.TemporaryDirectory() as directory, patch.object(oneclick, 'download', side_effect=fetch):
            path = Path(directory) / 'package.tar.gz'
            oneclick.fetch_release_asset('0.41.0', path.name, path)
            oneclick.fetch_release_asset('0.41.0', path.name, path)
            self.assertEqual(sum(url.endswith('package.tar.gz') for url in calls), 1)
            path.write_bytes(b'corrupted')
            with self.assertRaisesRegex(ValueError, '已有下载文件校验失败'):
                oneclick.fetch_release_asset('0.41.0', path.name, path)

    def test_checksum_mismatch_removes_download_without_executing_it(self):
        def fetch(url, path, limit):
            Path(path).write_bytes(('a' * 64 + '  package.tar.gz').encode() if url.endswith('SHA256SUMS') else b'bad')
        with tempfile.TemporaryDirectory() as directory, patch.object(oneclick, 'download', side_effect=fetch):
            path = Path(directory) / 'package.tar.gz'
            with self.assertRaisesRegex(ValueError, 'SHA256 校验失败'):
                oneclick.fetch_release_asset('0.41.0', path.name, path)
            self.assertFalse(path.exists())

    def test_archive_traversal_and_links_do_not_create_destination(self):
        with tempfile.TemporaryDirectory() as directory:
            for index, (name, kind) in enumerate((('bundle/../evil', tarfile.REGTYPE), ('bundle/link', tarfile.SYMTYPE), ('bundle/hard', tarfile.LNKTYPE), ('bundle/device', tarfile.CHRTYPE))):
                archive = Path(directory) / (str(index) + '.tar.gz')
                with tarfile.open(archive, 'w:gz') as tar:
                    item = tarfile.TarInfo(name); item.type = kind; item.linkname = '/etc/passwd'
                    item.size = 1 if kind == tarfile.REGTYPE else 0
                    tar.addfile(item, io.BytesIO(b'x') if item.size else None)
                destination = Path(directory) / ('out-' + str(index))
                with self.assertRaises(ValueError):
                    oneclick.safe_extract(archive, destination, 'bundle')
                self.assertFalse(destination.exists())

    def test_archive_extracts_regular_files_and_rejects_overwrite(self):
        with tempfile.TemporaryDirectory() as directory:
            archive = Path(directory) / 'a.tar.gz'
            with tarfile.open(archive, 'w:gz') as tar:
                item = tarfile.TarInfo('bundle/deploy/file.py'); item.size = 4; item.mode = 0o4777
                tar.addfile(item, io.BytesIO(b'data'))
            output = Path(directory) / 'out'
            oneclick.safe_extract(archive, output, 'bundle')
            self.assertEqual((output / 'deploy/file.py').read_bytes(), b'data')
            self.assertEqual((output / 'deploy/file.py').stat().st_mode & 0o7777, 0o755)
            with self.assertRaises(ValueError):
                oneclick.safe_extract(archive, output, 'bundle')


    def test_extract_publishes_only_a_complete_directory_and_retries_after_interruption(self):
        with tempfile.TemporaryDirectory() as directory:
            archive = Path(directory) / 'bundle.tar.gz'
            with tarfile.open(archive, 'w:gz') as tar:
                item = tarfile.TarInfo('bundle/web/index.html'); item.size = 8
                tar.addfile(item, io.BytesIO(b'complete'))
            destination = Path(directory) / 'bundle'
            def interrupted(source, target):
                self.assertFalse(destination.exists(), 'a partially extracted canonical directory became visible')
                target.write(b'partial')
                raise RuntimeError('injected interruption')
            with patch.object(oneclick.shutil, 'copyfileobj', side_effect=interrupted), self.assertRaises(RuntimeError):
                oneclick.safe_extract(archive, destination, 'bundle')
            self.assertFalse(destination.exists())
            # SIGKILL residue is inside a private staging directory, which a
            # later extraction must neither trust nor overwrite.
            residue = Path(directory) / '.bundle-extract-interrupted'
            residue.mkdir(); (residue / 'partial').write_bytes(b'preserve')
            oneclick.safe_extract(archive, destination, 'bundle')
            self.assertEqual((destination / 'web/index.html').read_bytes(), b'complete')
            self.assertEqual((residue / 'partial').read_bytes(), b'preserve')


class OneclickInspectionTests(unittest.TestCase):
    def test_docker_inspection_is_pinned_to_local_socket_not_remote_environment(self):
        with patch.dict(os.environ, {'DOCKER_HOST': 'tcp://remote.example:2376', 'DOCKER_CONTEXT': 'remote', 'DOCKER_TLS_VERIFY': '1'}), patch.object(oneclick.subprocess, 'run', return_value=SimpleNamespace(returncode=0, stdout='local')) as command:
            self.assertEqual(oneclick.command('docker', 'info'), 'local')
            self.assertEqual(command.call_args.args[0], ('docker', '--host', 'unix:///var/run/docker.sock', 'info'))
            self.assertFalse(any(key.startswith('DOCKER_') for key in command.call_args.kwargs['env']))

    def test_panel_domain_limit_is_63_not_the_general_dns_limit(self):
        self.assertEqual(len(oneclick.domain('a' * 59 + '.com')), 63)
        with self.assertRaisesRegex(ValueError, '并非 DNS 通用上限'):
            oneclick.domain('a' * 60 + '.com')

    def test_missing_tls_roots_never_disables_https_verification(self):
        context = Mock(); context.cert_store_stats.return_value = {'x509_ca': 0}
        with patch.object(oneclick.ssl, 'create_default_context', return_value=context):
            with self.assertRaisesRegex(ValueError, 'ca-certificates'):
                oneclick.tls_roots_preflight()
        context.cert_store_stats.return_value = {'x509_ca': 1}
        with patch.object(oneclick.ssl, 'create_default_context', return_value=context):
            oneclick.tls_roots_preflight()

    def test_dns_checks_all_records_including_stale_ipv6(self):
        rows = [(socket.AF_INET, 1, 6, '', ('93.184.216.34', 443)), (socket.AF_INET6, 1, 6, '', ('2001:4860:4860::8888', 443, 0, 0))]
        with patch.object(oneclick.socket, 'getaddrinfo', return_value=rows):
            with self.assertRaisesRegex(ValueError, 'AAAA'):
                oneclick.domain_preflight(['panel.example.com'], {'93.184.216.34'})
            oneclick.domain_preflight(['panel.example.com'], {'93.184.216.34', '2001:4860:4860::8888'})

    def test_docker_rejects_aaaa_even_when_it_points_to_this_server(self):
        rows = [(socket.AF_INET6, 1, 6, '', ('2001:4860:4860::8888', 443, 0, 0))]
        with patch.object(oneclick.socket, 'getaddrinfo', return_value=rows):
            with self.assertRaisesRegex(ValueError, '仅支持 IPv4'):
                oneclick.domain_preflight(['panel.example.com'], {'2001:4860:4860::8888'}, backend='docker')
            oneclick.domain_preflight(['panel.example.com'], {'2001:4860:4860::8888'}, backend='native')

    def test_inactive_nginx_configuration_claiming_443_blocks(self):
        with patch.object(oneclick.shutil, 'which', return_value='/usr/sbin/nginx'), patch.object(oneclick, 'command', return_value='server {\n listen [::]:443 ssl;\n}'):
            with self.assertRaisesRegex(ValueError, '配置声明监听 443'):
                oneclick.nginx_preflight()
        with patch.object(oneclick.shutil, 'which', return_value='/usr/sbin/nginx'), patch.object(oneclick, 'command', return_value='listen 80;\nlisten 11443;\n# listen 443;'):
            oneclick.nginx_preflight()

    def test_docker_nat_port_mapping_blocks_without_restarting_container(self):
        mapping = [{'HostConfig': {'PortBindings': {'443/udp': [{'HostPort': '443', 'HostIp': '0.0.0.0'}]}}}]
        with patch.object(oneclick.shutil, 'which', return_value='/usr/bin/docker'), patch.object(oneclick, 'command', side_effect=['{}', 'container-id', json.dumps(mapping)]) as command:
            with self.assertRaisesRegex(ValueError, 'Docker 容器发布 443'):
                oneclick.docker_preflight()
            self.assertEqual([c.args[1] for c in command.call_args_list], ['info', 'ps', 'inspect'])

    def test_ipv4_only_hosts_pass_and_udp_collision_does_not(self):
        class Probe:
            def __init__(self, family, kind): self.family, self.kind = family, kind
            def __enter__(self): return self
            def __exit__(self, *args): return False
            def setsockopt(self, *args): pass
            def bind(self, addr):
                if self.family == socket.AF_INET6: raise OSError(errno.EAFNOSUPPORT, 'disabled')
        with patch.object(oneclick.socket, 'socket', side_effect=Probe):
            oneclick.port_preflight()
        class BusyUDP(Probe):
            def bind(self, addr):
                if self.kind == socket.SOCK_DGRAM: raise OSError(errno.EADDRINUSE, 'busy')
        with patch.object(oneclick.socket, 'socket', side_effect=BusyUDP):
            with self.assertRaisesRegex(ValueError, 'UDP 443'):
                oneclick.port_preflight()

    def test_resume_port_allowance_is_protocol_scoped_and_never_ignores_permissions(self):
        class Busy:
            def __init__(self, family, kind): self.family, self.kind = family, kind
            def __enter__(self): return self
            def __exit__(self, *args): return False
            def setsockopt(self, *args): pass
            def bind(self, addr): raise OSError(errno.EADDRINUSE, 'busy')
        allow = {(socket.AF_INET, socket.SOCK_STREAM, 443), (socket.AF_INET6, socket.SOCK_STREAM, 443)}
        with patch.object(oneclick.socket, 'socket', side_effect=Busy), patch.object(oneclick.shutil, 'which', return_value=None):
            with self.assertRaisesRegex(ValueError, 'UDP 443'):
                oneclick.port_preflight(allowed_bindings=allow)
        class Denied(Busy):
            def bind(self, addr): raise OSError(errno.EACCES, 'denied')
        with patch.object(oneclick.socket, 'socket', side_effect=Denied), patch.object(oneclick.shutil, 'which', return_value=None):
            with self.assertRaisesRegex(ValueError, 'TCP 443'):
                oneclick.port_preflight(allowed_bindings=allow)

    def test_plan_cannot_skip_acme_or_change_fixed_default_target(self):
        value = plan(); oneclick.validate_plan(value)
        for key, replacement in (('acme_agreed', False), ('reality_sni', '127.0.0.1'), ('backend', 'shell'), ('panel_domain', 'https://evil.example')):
            bad = dict(value, **{key: replacement})
            with self.assertRaises(ValueError):
                oneclick.validate_plan(bad)

    def test_check_does_not_write_receipts_or_execute_backend(self):
        value = plan()
        with tempfile.TemporaryDirectory() as directory:
            file = Path(directory) / 'plan.json'; file.write_text(json.dumps(value))
            with patch.object(oneclick, 'release_catalog', return_value={'version': value['version']}), patch.object(oneclick, 'preflight', return_value={'public_ips': ['93.184.216.34'], 'os': 'debian 12'}), patch.object(oneclick, 'write_receipt') as write, patch.object(oneclick, 'install_plan') as install:
                oneclick.main(['--plan', str(file), '--check'])
                write.assert_not_called(); install.assert_not_called()

    def test_download_failure_resume_runs_fresh_host_checks_without_claiming_ownership(self):
        value = plan()
        with tempfile.TemporaryDirectory() as directory, contextlib.ExitStack() as stack:
            stack.enter_context(patch.object(oneclick, 'ROOT', Path(directory)))
            stack.enter_context(patch.object(oneclick.os, 'geteuid', return_value=0))
            stack.enter_context(patch.object(oneclick.platform, 'system', return_value='Linux'))
            stack.enter_context(patch.object(oneclick.platform, 'machine', return_value='amd64'))
            stack.enter_context(patch.object(oneclick.platform, 'freedesktop_os_release', return_value={'ID': 'ubuntu', 'VERSION_ID': '24.04'}, create=True))
            stack.enter_context(patch.object(oneclick.Path, 'is_dir', return_value=True))
            stack.enter_context(patch.object(oneclick.Path, 'exists', return_value=False))
            stack.enter_context(patch.object(oneclick.Path, 'is_symlink', return_value=False))
            stack.enter_context(patch.object(oneclick, 'available_memory', return_value=4 << 30))
            stack.enter_context(patch.object(oneclick.shutil, 'disk_usage', return_value=SimpleNamespace(free=8 << 30)))
            stack.enter_context(patch.object(oneclick.shutil, 'which', return_value=None))
            stack.enter_context(patch.object(oneclick, 'apt_locks_available'))
            stack.enter_context(patch.object(oneclick, 'nginx_preflight'))
            stack.enter_context(patch.object(oneclick, 'docker_preflight', return_value={'available': False}))
            stack.enter_context(patch.object(oneclick, 'clock_preflight'))
            stack.enter_context(patch.object(oneclick, 'public_ips', return_value={'93.184.216.34'}))
            stack.enter_context(patch.object(oneclick, 'domain_preflight'))
            ports = stack.enter_context(patch.object(oneclick, 'port_preflight'))
            backend = stack.enter_context(patch.object(oneclick.importlib, 'import_module'))
            evidence = oneclick.preflight(value, resuming=True)
            self.assertEqual(evidence['public_ips'], ['93.184.216.34'])
            ports.assert_called_once_with(True, frozenset())
            backend.assert_not_called()
            # A receipt alone cannot authorize replacing a foreign config.
            with patch.object(oneclick.Path, 'exists', new=lambda path: str(path) == '/etc/guangyue-personal.json'):
                with self.assertRaisesRegex(ValueError, '已有面板'):
                    oneclick.preflight(value, resuming=True)

    def test_secrets_redacted_from_failures(self):
        text = oneclick.redact('postgres://name:password@host/db secret=very-secret token=gye_' + 'A' * 43)
        for secret in ('name:password', 'very-secret', 'A' * 43):
            self.assertNotIn(secret, text)


class NativeInstallerSafetyTests(unittest.TestCase):
    def test_certificate_retry_timer_has_its_real_unit_path(self):
        self.assertEqual(native_gateway.unit_path('guangyue-gateway-cert.timer'), native_gateway.SYSTEMD / 'guangyue-gateway-cert.timer')

    def test_completed_apt_resume_removes_only_owned_policy_before_empty_missing_return(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'policy-rc.d'; journal = {'id': str(uuid.uuid4())}
            journal['package_policy'] = native_gateway.policy_content(journal)[1]
            with patch.object(native_gateway, 'POLICY', path), patch.object(native_gateway, 'save'), patch.object(native_gateway.subprocess, 'run', return_value=SimpleNamespace(returncode=0, stdout='install ok installed')), patch.object(oneclick, 'apt_locks_available') as locks, patch.object(native_gateway, 'run') as install:
                native_gateway.create_owned_policy(journal)
                native_gateway.prepare_packages('lite', journal)
                locks.assert_called_once(); install.assert_not_called()
                self.assertFalse(path.exists())
                self.assertNotIn('package_policy', journal)

    def test_completed_apt_resume_never_removes_shim_while_apt_lock_is_busy(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'policy-rc.d'; journal = {'id': str(uuid.uuid4())}
            journal['package_policy'] = native_gateway.policy_content(journal)[1]
            with patch.object(native_gateway, 'POLICY', path), patch.object(native_gateway, 'save'), patch.object(oneclick, 'apt_locks_available', side_effect=ValueError('APT busy')):
                native_gateway.create_owned_policy(journal)
                with self.assertRaisesRegex(ValueError, 'APT busy'):
                    native_gateway.prepare_packages('lite', journal)
                self.assertTrue(path.exists())
                self.assertIn('package_policy', journal)

    def test_fixture_certificate_requires_both_controlled_ci_flags(self):
        value = plan()
        for environment in ({}, {'GITHUB_ACTIONS': 'true'}, {'GY_ONECLICK_INTEGRATION': '1'}):
            with patch.dict(os.environ, environment, clear=True), patch.object(oneclick, 'verify_bundle'), patch.object(native_gateway, 'load') as load:
                with self.assertRaisesRegex(ValueError, '仅供受控'):
                    native_gateway.install(value, Path('/unused'), cert_file=Path('/test-cert'), key_file=Path('/test-key'))
                load.assert_not_called()
        with patch.dict(os.environ, {'GITHUB_ACTIONS': 'true', 'GY_ONECLICK_INTEGRATION': '1'}, clear=True), patch.object(oneclick, 'verify_bundle'):
            with self.assertRaises(ValueError):
                native_gateway.install(value, Path('/unused'), cert_file=Path('/test-cert'))

    def test_resume_socket_owners_must_all_match_the_expected_service(self):
        lines = ['tcp LISTEN 0 4096 0.0.0.0:443 0.0.0.0:* users:(("gateway",pid=101,fd=3))', 'udp UNCONN 0 0 [::]:443 [::]:* users:(("hy2",pid=202,fd=3))']
        expected = {(socket.SOCK_STREAM, 443): {101}, (socket.SOCK_DGRAM, 443): {202}}
        ours = native_gateway.owned_listener_bindings(lines, expected)
        self.assertIn((socket.AF_INET, socket.SOCK_STREAM, 443), ours)
        self.assertIn((socket.AF_INET6, socket.SOCK_DGRAM, 443), ours)
        foreign = 'tcp LISTEN 0 4096 127.0.0.2:443 0.0.0.0:* users:(("foreign",pid=303,fd=3))'
        mixed = native_gateway.owned_listener_bindings(lines + [foreign], expected)
        self.assertNotIn((socket.AF_INET, socket.SOCK_STREAM, 443), mixed)
        self.assertIn((socket.AF_INET, socket.SOCK_DGRAM, 443), mixed)
        self.assertFalse(native_gateway.owned_listener_bindings([lines[0].split('users:')[0]], expected))

    def test_dependency_simulation_rejects_upgrade_before_apply(self):
        journal = {'id': str(uuid.uuid4())}
        with patch.object(native_gateway.subprocess, 'run', return_value=SimpleNamespace(returncode=1, stdout='')), patch.object(oneclick, 'apt_locks_available'), patch.object(native_gateway, 'run', side_effect=['', 'Inst openssl [old-version] (new-version Debian)']) as run, patch.object(native_gateway, 'create_owned_policy') as policy:
            with self.assertRaisesRegex(ValueError, '升级或删除'):
                native_gateway.prepare_packages('lite', journal)
            policy.assert_not_called()
            self.assertEqual(run.call_count, 2)

    def test_existing_service_policy_is_never_overwritten(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'policy-rc.d'; path.write_text('administrator-policy')
            with patch.object(native_gateway, 'POLICY', path), patch.object(native_gateway, 'save'):
                journal = {'id': str(uuid.uuid4())}; journal['package_policy'] = native_gateway.policy_content(journal)[1]
                with self.assertRaisesRegex(ValueError, '已有 policy'):
                    native_gateway.create_owned_policy(journal)
                self.assertEqual(path.read_text(), 'administrator-policy')

    def test_temporary_policy_is_removed_after_package_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'policy-rc.d'; journal = {'id': str(uuid.uuid4())}
            def run(*args, **kwargs):
                if args[0] == 'env':
                    self.assertTrue(any('DPkg::Pre-Invoke' in a for a in args))
                    native_gateway.create_owned_policy(journal)
                    self.assertIn('exit 101', path.read_text())
                    raise RuntimeError('package failure')
                if args[:2] == ('apt-get', '-s'):
                    return 'Inst openssl (3.0.18 Debian:12/stable [amd64])'
                return ''
            with patch.object(native_gateway, 'POLICY', path), patch.object(native_gateway, 'save'), patch.object(oneclick, 'apt_locks_available'), patch.object(native_gateway.subprocess, 'run', return_value=SimpleNamespace(returncode=1, stdout='')), patch.object(native_gateway, 'run', side_effect=run):
                with self.assertRaises(RuntimeError):
                    native_gateway.prepare_packages('lite', journal)
                self.assertFalse(path.exists())
                self.assertNotIn('package_policy', journal)

    def test_temporary_policy_external_change_is_retained(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'policy-rc.d'
            with patch.object(native_gateway, 'POLICY', path), patch.object(native_gateway, 'save'):
                journal = {'id': str(uuid.uuid4())}; journal['package_policy'] = native_gateway.policy_content(journal)[1]
                native_gateway.create_owned_policy(journal)
                path.write_text('new-administrator-policy')
                native_gateway.remove_owned_policy(journal)
                self.assertEqual(path.read_text(), 'new-administrator-policy')

    def test_recovery_stops_only_unmodified_owned_units(self):
        with tempfile.TemporaryDirectory() as directory:
            units = Path(directory)
            ours = units / 'guangyue.service'; ours.write_text('known')
            changed = units / 'guangyue-hy2.service'; changed.write_text('external modification')
            journal = {'units': ['guangyue', 'guangyue-hy2', 'foreign'], 'unit_hashes': {'guangyue': oneclick.sha256(ours), 'guangyue-hy2': 'a' * 64}}
            with patch.object(native_gateway, 'SYSTEMD', units), patch.object(native_gateway.subprocess, 'run') as command:
                native_gateway.stop_owned(journal)
                self.assertEqual([call.args[0] for call in command.call_args_list], [['systemctl', 'stop', 'guangyue']])

    def test_recovery_does_not_overwrite_changed_owned_unit(self):
        with tempfile.TemporaryDirectory() as directory:
            units = Path(directory); path = units / 'guangyue.service'; path.write_text('new external config')
            journal = {'units': ['guangyue'], 'paths': [str(path)], 'unit_hashes': {'guangyue': 'a' * 64}}
            with patch.object(native_gateway, 'SYSTEMD', units), patch.object(native_gateway, 'save'):
                with self.assertRaisesRegex(ValueError, '外部修改'):
                    native_gateway.own_unit('guangyue', journal)
            self.assertEqual(path.read_text(), 'new external config')

    def test_foreign_files_and_links_refused_without_modification(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'state'; path.write_text('foreign-data')
            with patch.object(native_gateway, 'save'):
                with self.assertRaisesRegex(ValueError, '拒绝覆盖'):
                    native_gateway.own_path(path, {'paths': []})
                link = Path(directory) / 'link'; link.symlink_to(path)
                with self.assertRaisesRegex(ValueError, '不可信链接'):
                    native_gateway.own_path(link, {'paths': [str(link)]})
            self.assertEqual(path.read_text(), 'foreign-data')

    def test_completed_native_install_only_checks_health_never_resets(self):
        value = plan(); result = {'version': value['version'], 'stage': 'installed'}
        with patch.object(oneclick, 'verify_bundle'), patch.object(native_gateway, 'load', return_value={'phase': 'installed', 'result': result}), patch.object(native_gateway, 'native_health') as health, patch.object(native_gateway, 'prepare_packages') as packages, patch.object(native_gateway, 'run') as run:
            self.assertEqual(native_gateway.install(value, Path('/unused')), result)
            health.assert_called_once_with(value['version']); packages.assert_not_called(); run.assert_not_called()


if __name__ == '__main__':
    unittest.main()
