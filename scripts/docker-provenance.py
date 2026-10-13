#!/usr/bin/env python3
"""Keep exact bundled database versions, notices and Redis corresponding source.

This is a component record, not an SBOM for every package in the OS images.
Official immutable images are inspected in network-disabled, unprivileged runs.
"""
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import time
import urllib.request
from urllib.parse import urlparse


def command(*args):
    result = subprocess.run(args, capture_output=True, check=True, timeout=120)
    if len(result.stdout) > 4 << 20:
        raise ValueError('Image provenance response is too large')
    return result.stdout.decode('utf-8')


def inspect(source):
    if not re.fullmatch(r'(postgres:17-bookworm|redis:8-bookworm)@sha256:[a-f0-9]{64}', source):
        raise ValueError('Database image must use the approved immutable source')
    rows = json.loads(command('docker', 'image', 'inspect', source))
    if len(rows) != 1 or rows[0].get('Os') != 'linux' or rows[0].get('Architecture') != 'amd64':
        raise ValueError('Database image provenance platform mismatch')
    image = rows[0]
    if not re.fullmatch(r'sha256:[a-f0-9]{64}', image.get('Id', '')):
        raise ValueError('Database image identity is invalid')
    env = dict(item.split('=', 1) for item in image['Config']['Env'] if '=' in item)
    return image['Id'], env


def image_output(image, executable, *args):
    return command('docker', 'run', '--rm', '--network', 'none', '--read-only',
                   '--user', '65534:65534', '--cap-drop', 'ALL',
                   '--security-opt', 'no-new-privileges', '--pids-limit', '16', '--memory', '128m',
                   '--tmpfs', '/tmp:rw,nosuid,noexec,size=8m',
                   '--tmpfs', '/data:rw,nosuid,noexec,size=1m',
                   '--tmpfs', '/var/lib/postgresql/data:rw,nosuid,noexec,size=1m',
                   '--entrypoint', executable, image, *args).strip()


def digest(path):
    result = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1 << 20), b''):
            result.update(block)
    return result.hexdigest()


def redis_build_source(env, history):
    """Redis images use either ENV (older) or build ARGs (current).

    ARG values remain in the immutable image's untruncated build history, but
    are deliberately absent from its runtime environment. Require one exact
    source/checksum pair and reject conflicting metadata instead of guessing.
    """
    version = env.get('REDIS_VERSION', '')
    values = {name: set() for name in ('URL', 'SHA')}
    for name in values:
        if env.get('REDIS_DOWNLOAD_' + name):
            values[name].add(env['REDIS_DOWNLOAD_' + name])
    for name, value in re.findall(r'(?:^|\s)REDIS_DOWNLOAD_(URL|SHA)=([^\s]+)', history):
        values[name].add(value)
    if any(len(items) != 1 for items in values.values()):
        raise ValueError('Pinned Redis image must contain one verified source URL/checksum pair in ENV or build history')
    expected = next(iter(values['SHA']))
    source = next(iter(values['URL'])).replace('http://', 'https://', 1)
    if not re.fullmatch(r'8\.[0-9]+\.[0-9]+', version) or not re.fullmatch('[a-f0-9]{64}', expected):
        raise ValueError('Pinned Redis image must expose its exact upstream version and source checksum')
    allowed = (f'https://download.redis.io/releases/redis-{version}.tar.gz',
               f'https://github.com/redis/redis/archive/refs/tags/{version}.tar.gz')
    if source not in allowed:
        raise ValueError('Redis source must match the immutable image official release URL')
    return version, source, expected


def redis_source(env, cache, history=''):
    version, source, expected = redis_build_source(env, history)
    cache.mkdir(parents=True, exist_ok=True)
    archive = cache / ('redis-source-' + expected + '.tar.gz')
    if archive.is_file() and digest(archive) != expected:
        archive.unlink()
    if not archive.is_file():
        temporary = archive.with_suffix('.part')
        for attempt in range(3):
            try:
                # GitHub archive links resolve to this same official tarball;
                # using codeload directly avoids the redirect front end.
                download = source if source.startswith('https://download.redis.io/') else f'https://codeload.github.com/redis/redis/tar.gz/refs/tags/{version}'
                request = urllib.request.Request(download, headers={'User-Agent': 'Guangyue-Release-Provenance/1'})
                with urllib.request.urlopen(request, timeout=30) as response, temporary.open('wb') as output:
                    if urlparse(response.geturl()).hostname not in ('download.redis.io', 'github.com', 'codeload.github.com'):
                        raise ValueError('Redis source redirected outside the official origin')
                    count = 0
                    for block in iter(lambda: response.read(1 << 20), b''):
                        count += len(block)
                        if count > 128 << 20:
                            raise ValueError('Redis source archive exceeded its size limit')
                        output.write(block)
                if digest(temporary) != expected:
                    raise ValueError('Redis source differs from the immutable image source checksum')
                temporary.replace(archive)
                break
            except (OSError, ValueError):
                temporary.unlink(missing_ok=True)
                if attempt == 2:
                    raise
                time.sleep(1)
    notices = {}
    with tarfile.open(archive, 'r:gz') as tar:
        for item in tar:
            parts = Path(item.name).parts
            if len(parts) != 2 or parts[0] != 'redis-' + version or not item.isfile():
                continue
            if re.match(r'^(LICENSE|COPYING|NOTICE)(\.|$|-)', parts[1], re.I):
                if item.size > 1 << 20:
                    raise ValueError('Redis release notice exceeds its size limit')
                notices[parts[1]] = tar.extractfile(item).read().decode('utf-8')
    if not notices:
        raise ValueError('Redis corresponding source contains no upstream license notices')
    return version, source, expected, archive, notices


def collect(out, cache, postgres, redis):
    out.mkdir(parents=True, exist_ok=True)
    pg_id, pg_env = inspect(postgres)
    redis_id, redis_env = inspect(redis)
    pg_output = image_output(pg_id, 'postgres', '--version')
    redis_output = image_output(redis_id, 'redis-server', '--version')
    pg_version = re.match(r'17\.[0-9]+', pg_env.get('PG_VERSION', ''))
    if not pg_version or not re.search(r'PostgreSQL\) ' + re.escape(pg_version[0]) + r'(?:\s|$)', pg_output):
        raise ValueError('PostgreSQL executable differs from its pinned image version')
    pg_notice = image_output(pg_id, 'cat', '/usr/share/doc/postgresql-17/copyright')
    if not pg_notice:
        raise ValueError('PostgreSQL image license notice is missing')
    (out / 'POSTGRES-IMAGE-COPYRIGHT.txt').write_text(pg_notice + '\n')
    redis_history = command('docker', 'history', '--no-trunc', '--format', '{{.CreatedBy}}', redis_id)
    version, source, checksum, archive, notices = redis_source(redis_env, cache, redis_history)
    if not re.search(r'\bv=' + re.escape(version) + r'(?:\s|$)', redis_output):
        raise ValueError('Redis executable differs from the verified corresponding source')
    name = 'redis-' + version + '-source.tar.gz'
    import shutil
    shutil.copyfile(archive, out / name)
    for filename, content in notices.items():
        (out / ('REDIS-' + filename)).write_text(content)
    (out / 'REDIS-IMAGE-BUILD-HISTORY.txt').write_text(redis_history)
    record = {
        'schema': 1,
        'scope': 'Exact bundled database components and upstream notices; excludes the OS package inventory. SBOM.spdx.json describes the application dependencies and proxy cores, not the entire Docker archive.',
        'postgres': {'image_id': pg_id, 'image_source': postgres, 'version': pg_version[0],
                     'package_version': pg_env['PG_VERSION'], 'executable_version': pg_output,
                     'source': 'https://ftp.postgresql.org/pub/source/v' + pg_version[0] + '/',
                     'notice': 'POSTGRES-IMAGE-COPYRIGHT.txt'},
        'redis': {'image_id': redis_id, 'image_source': redis, 'version': version,
                  'executable_version': redis_output, 'source': source, 'source_sha256': checksum,
                  'source_verification': 'Exact ENV/build ARG from the immutable image, verified by SHA-256.',
                  'image_build_recipe': 'REDIS-IMAGE-BUILD-HISTORY.txt',
                  'source_archive': name, 'upstream_license_notices': ['REDIS-' + item for item in sorted(notices)],
                  'modifications': 'Official image binary; upstream source and licensing options from the exact Redis release. The immutable image build history records its build flags and packaging modifications.'},
    }
    (out / 'DOCKER_COMPONENTS.json').write_text(json.dumps(record, indent=2) + '\n')


if __name__ == '__main__':
    if len(sys.argv) != 5:
        raise SystemExit('Expected output directory, source cache and pinned PostgreSQL/Redis images')
    collect(Path(sys.argv[1]), Path(sys.argv[2]), sys.argv[3], sys.argv[4])
