"""Exercise real deterministic archive/checksum generation with tiny fixtures."""
import ast
import hashlib
import io
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest


class CompleteReleasePackageTests(unittest.TestCase):
    def test_every_install_mode_has_complete_checked_assets_and_reproducible_archives(self):
        source = Path(__file__).resolve().parents[2]
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / 'scripts').mkdir()
            shutil.copyfile(source / 'scripts/package.py', root / 'scripts/package.py')
            for name in ['VERSION', 'README.md', 'README_EN.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md', 'CHANGELOG.md', 'SECURITY.md', 'CONTRIBUTING.md', 'install.sh']:
                (root / name).write_text('0.41.0\n' if name == 'VERSION' else 'test fixture\n')
            for directory in ['frontend/dist', 'deploy/docker', 'scripts', 'docs', 'core-patches', 'examples', 'build/notices', 'licenses', 'build/bin']:
                (root / directory).mkdir(parents=True, exist_ok=True)
            (root / 'frontend/dist/index.html').write_text('<html>fixture</html>')
            (root / 'deploy/install.py').write_text("UPSTREAM_HASHES = {'xray': '" + 'a' * 64 + "', 'mihomo': '" + 'b' * 64 + "'}\n")
            for name in ['oneclick.py', 'docker_deploy.py', 'native_gateway.py', 'common.py']:
                (root / 'deploy' / name).write_text('# fixture module\n')
            shutil.copyfile(source / 'deploy/oneclick.py', root / 'deploy/oneclick.py')
            (root / 'deploy/docker/entrypoint.sh').write_text('#!/bin/sh\nexit 0\n')
            for name in ['guangyue-linux-amd64', 'guangyue-gateway-linux-amd64', 'hysteria-node-linux-amd64', 'xray-linux-amd64', 'mihomo-linux-amd64']:
                (root / 'build/bin' / name).write_text('test executable ' + name)
            for name in ['SBOM.spdx.json', 'mihomo-source.tar.gz', 'MIHOMO-LICENSE.txt', 'go-ethereum-source.tar.gz']:
                (root / 'build/notices' / name).write_text('test provenance ' + name)
            (root / 'build/xray-source.tar.gz').write_text('test xray source')
            subprocess.run([sys.executable, str(root / 'scripts/package.py')], capture_output=True, check=True)
            output = root / 'build/releases'
            expected = {'guangyue-panel-lite-0.41.0-linux-amd64.tar.gz', 'guangyue-panel-pro-0.41.0-linux-amd64.tar.gz', 'guangyue-installer-0.41.0.tar.gz', 'install.sh', 'SBOM.spdx.json'}
            checksum_rows = dict((name, digest) for digest, name in (row.split('  ', 1) for row in (output / 'SHA256SUMS').read_text().splitlines()))
            self.assertEqual(set(checksum_rows), expected)
            before = {name: hashlib.sha256((output / name).read_bytes()).hexdigest() for name in expected}
            self.assertEqual(checksum_rows, before)
            for edition in ('lite', 'pro'):
                name = 'guangyue-panel-' + edition + '-0.41.0-linux-amd64'
                with tarfile.open(output / (name + '.tar.gz')) as archive:
                    members = {item.name: item for item in archive.getmembers()}
                    for path in ['bin/guangyue-gateway-linux-amd64', 'bin/mihomo-linux-amd64', 'licenses/mihomo-source.tar.gz', 'licenses/MIHOMO-LICENSE.txt', 'install.sh', 'deploy/oneclick.py', 'deploy/docker/entrypoint.sh']:
                        self.assertIn(name + '/' + path, members)
                    self.assertTrue(members[name + '/install.sh'].mode & 0o111)
                    for item in members.values():
                        self.assertEqual((item.uid, item.gid, item.mtime), (0, 0, 0))
                    code = archive.extractfile(name + '/deploy/install.py').read().decode()
                    hashes = ast.literal_eval(ast.parse(code).body[0].value)
                    self.assertEqual(hashes['xray'], hashlib.sha256((root / 'build/bin/xray-linux-amd64').read_bytes()).hexdigest())
            with tarfile.open(output / 'guangyue-installer-0.41.0.tar.gz') as archive:
                names = set(archive.getnames())
                for path in ['deploy/oneclick.py', 'deploy/docker_deploy.py', 'deploy/native_gateway.py', 'deploy/common.py', 'install.sh']:
                    self.assertIn('guangyue-installer-0.41.0/' + path, names)
            subprocess.run([sys.executable, str(root / 'scripts/package.py')], capture_output=True, check=True)
            self.assertEqual(before, {name: hashlib.sha256((output / name).read_bytes()).hexdigest() for name in expected})
            # Exercise the production Docker build orchestration and manifest
            # with a bounded CLI adapter; real image execution runs separately
            # in the dedicated disposable Docker CI jobs.
            shutil.copyfile(source / 'scripts/build-docker.sh', root / 'scripts/build-docker.sh')
            shutil.copyfile(source / 'scripts/docker-provenance.py', root / 'scripts/docker-provenance.py')
            for name in ('Dockerfile', 'entrypoint.sh'):
                shutil.copyfile(source / 'deploy/docker' / name, root / 'deploy/docker' / name)
            fakebin = root / 'fakebin'; fakebin.mkdir()
            journal = root / 'docker-calls.jsonl'
            redis_source = root / 'redis-source.tar.gz'
            with tarfile.open(redis_source, 'w:gz') as archive:
                content = b'Upstream Redis fixture licensing options: AGPLv3, RSALv2, SSPLv1\n'
                item = tarfile.TarInfo('redis-8.0.0/LICENSE.txt'); item.size = len(content)
                archive.addfile(item, io.BytesIO(content))
            redis_sha = hashlib.sha256(redis_source.read_bytes()).hexdigest()
            cache = root / '.cache/docker-source'; cache.mkdir(parents=True)
            shutil.copyfile(redis_source, cache / ('redis-source-' + redis_sha + '.tar.gz'))
            fake = fakebin / 'docker'
            fake.write_text('#!' + sys.executable + '\n' + '''
import json, os, sys, shutil
with open(os.environ['DOCKER_FIXTURE_JOURNAL'], 'a') as out: out.write(json.dumps(sys.argv[1:]) + '\\n')
args=sys.argv[1:]
if args[:2]==['image','inspect']:
    if len(args)==3:
        redis=args[-1].startswith('redis:')
        env=['REDIS_VERSION=8.0.0'] if redis else ['PG_VERSION=17.6-1.pgdg12+1','PG_MAJOR=17']
        print(json.dumps([{'Id':'sha256:'+('c' if redis else 'b')*64,'Os':'linux','Architecture':'amd64','Config':{'Env':env}}]))
    elif args[3]=='{{.Os}}/{{.Architecture}}': print('linux/amd64')
    else:
        value=args[-1];letter='b' if value.startswith('postgres:') else 'c' if value.startswith('redis:') else 'a'
        print('sha256:'+letter*64)
elif args[0]=='save': sys.stdout.buffer.write(b'disposable-docker-save-fixture')
elif args[0]=='history': print('RUN |2 REDIS_DOWNLOAD_URL=https://github.com/redis/redis/archive/refs/tags/8.0.0.tar.gz REDIS_DOWNLOAD_SHA='+os.environ['DOCKER_FIXTURE_REDIS_SHA']+' /bin/sh -c set -eux; # buildkit')
elif args[0]=='run':
    executable=args[args.index('--entrypoint')+1]
    if executable=='postgres': print('postgres (PostgreSQL) 17.6 (Debian 17.6-1.pgdg12+1)')
    elif executable=='redis-server': print('Redis server v=8.0.0 sha=00000000:0 malloc=jemalloc bits=64')
    elif executable=='cat': print('PostgreSQL official image package copyright fixture')
    else: raise SystemExit(2)
elif args[0]=='build': shutil.copytree(args[-1]+'/licenses',os.environ['DOCKER_FIXTURE_LICENSES'])
elif args[0] not in ('pull','build'): raise SystemExit(2)
''')
            fake.chmod(0o755)
            environment = dict(os.environ, PATH=str(fakebin) + os.pathsep + os.environ['PATH'], DOCKER_FIXTURE_JOURNAL=str(journal), DOCKER_FIXTURE_REDIS_SHA=redis_sha, DOCKER_FIXTURE_LICENSES=str(root / 'image-licenses'))
            subprocess.run(['bash', str(root / 'scripts/build-docker.sh'), '--bundle', str(output / 'guangyue-panel-pro-0.41.0-linux-amd64'), '--output', str(root / 'docker-artifacts')], env=environment, capture_output=True, check=True)
            docker = root / 'docker-artifacts'
            manifest = json.loads((docker / 'DOCKER_IMAGES.json').read_text())
            self.assertEqual((manifest['schema'], manifest['version'], manifest['platform']), (1, '0.41.0', 'linux/amd64'))
            self.assertEqual(manifest['sha256'], hashlib.sha256((docker / manifest['archive']).read_bytes()).hexdigest())
            self.assertEqual({name: value['id'] for name, value in manifest['images'].items()}, {name: 'sha256:' + letter * 64 for name, letter in [('app', 'a'), ('postgres', 'b'), ('redis', 'c')]})
            self.assertIn('@sha256:', manifest['images']['postgres']['source'])
            self.assertIn('@sha256:', manifest['images']['redis']['source'])
            calls = [json.loads(line) for line in journal.read_text().splitlines()]
            self.assertFalse(any(call[0] in ('push', 'exec', 'compose') for call in calls))
            for call in (call for call in calls if call[0] == 'run'):
                self.assertEqual(call[call.index('--network') + 1], 'none')
                self.assertIn('--read-only', call)
                self.assertEqual(call[call.index('--cap-drop') + 1], 'ALL')
                self.assertEqual(call[call.index('--user') + 1], '65534:65534')
            provenance = json.loads((root / 'image-licenses/DOCKER_COMPONENTS.json').read_text())
            self.assertEqual((provenance['redis']['version'], provenance['postgres']['version']), ('8.0.0', '17.6'))
            self.assertEqual(provenance['redis']['source_sha256'], redis_sha)
            self.assertEqual(hashlib.sha256((root / 'image-licenses' / provenance['redis']['source_archive']).read_bytes()).hexdigest(), redis_sha)
            self.assertIn('excludes the OS package inventory', provenance['scope'])
            self.assertTrue((root / 'image-licenses/POSTGRES-IMAGE-COPYRIGHT.txt').is_file())
            self.assertIn('REDIS_DOWNLOAD_SHA=' + redis_sha, (root / 'image-licenses/REDIS-IMAGE-BUILD-HISTORY.txt').read_text())
            self.assertEqual(provenance['redis']['source'], 'https://github.com/redis/redis/archive/refs/tags/8.0.0.tar.gz')
            self.assertEqual(next(call for call in calls if call[0] == 'save')[1:], ['sha256:' + letter * 64 for letter in 'abc'])


class DockerRedisProvenanceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        path = Path(__file__).resolve().parents[2] / 'scripts/docker-provenance.py'
        spec = importlib.util.spec_from_file_location('docker_provenance', path)
        cls.provenance = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(cls.provenance)

    def test_current_immutable_image_build_args_and_legacy_env_are_verified(self):
        version = '8.2.10'; checksum = 'd6de0ec3bce8eeba13ecbd7478dd56fb87a4bbe8ec832dd876892a86ac1a55ec'
        source = 'https://github.com/redis/redis/archive/refs/tags/' + version + '.tar.gz'
        history = 'RUN |2 REDIS_DOWNLOAD_URL=' + source + ' REDIS_DOWNLOAD_SHA=' + checksum + ' /bin/sh -c set -eux; # buildkit'
        self.assertEqual(self.provenance.redis_build_source({'REDIS_VERSION': version}, history), (version, source, checksum))
        legacy = {'REDIS_VERSION': version, 'REDIS_DOWNLOAD_URL': 'http://download.redis.io/releases/redis-' + version + '.tar.gz', 'REDIS_DOWNLOAD_SHA': checksum}
        self.assertEqual(self.provenance.redis_build_source(legacy, '')[1:], ('https://download.redis.io/releases/redis-' + version + '.tar.gz', checksum))

    def test_missing_conflicting_or_unofficial_source_metadata_fails_closed(self):
        version = '8.2.10'; checksum = 'a' * 64
        source = 'https://github.com/redis/redis/archive/refs/tags/' + version + '.tar.gz'
        valid = 'RUN |2 REDIS_DOWNLOAD_URL=' + source + ' REDIS_DOWNLOAD_SHA=' + checksum + ' /bin/sh'
        for env, history in [
            ({'REDIS_VERSION': version}, ''),
            ({'REDIS_VERSION': version, 'REDIS_DOWNLOAD_SHA': 'b' * 64}, valid),
            ({'REDIS_VERSION': version}, valid + ' REDIS_DOWNLOAD_URL=https://example.com/archive.tar.gz'),
            ({'REDIS_VERSION': version}, valid.replace('/8.2.10.tar.gz', '/8.2.9.tar.gz')),
            ({'REDIS_VERSION': version}, valid.replace(checksum, 'unverified')),
            ({'REDIS_VERSION': '8'}, valid),
        ]:
            with self.subTest(env=env, history=history), self.assertRaises(ValueError):
                self.provenance.redis_build_source(env, history)

    def test_verified_cache_keeps_exact_source_archive_and_rejects_missing_notices(self):
        with tempfile.TemporaryDirectory() as temporary:
            cache = Path(temporary)
            for name, notices in [('source.tar.gz', True), ('no-license.tar.gz', False)]:
                archive = cache / name
                with tarfile.open(archive, 'w:gz') as tar:
                    body = b'upstream license fixture' if notices else b'fixture source'
                    item = tarfile.TarInfo('redis-8.2.10/' + ('LICENSE.txt' if notices else 'source.c'))
                    item.size = len(body); tar.addfile(item, io.BytesIO(body))
                checksum = hashlib.sha256(archive.read_bytes()).hexdigest()
                pinned = cache / ('redis-source-' + checksum + '.tar.gz'); shutil.copyfile(archive, pinned)
                history = 'RUN |2 REDIS_DOWNLOAD_URL=https://github.com/redis/redis/archive/refs/tags/8.2.10.tar.gz REDIS_DOWNLOAD_SHA=' + checksum + ' /bin/sh'
                if notices:
                    version, source, sha, result, texts = self.provenance.redis_source({'REDIS_VERSION': '8.2.10'}, cache, history)
                    self.assertEqual((version, sha, result), ('8.2.10', checksum, pinned))
                    self.assertEqual(texts, {'LICENSE.txt': 'upstream license fixture'})
                else:
                    with self.assertRaisesRegex(ValueError, 'no upstream license'):
                        self.provenance.redis_source({'REDIS_VERSION': '8.2.10'}, cache, history)
