#!/usr/bin/env python3
"""Socket-activated, root-owned release operations. No client URLs or shell commands."""
import ast
import hashlib
import http.server
import json
import os
import re
import shutil
import socket
import socketserver
import subprocess
import sys
import tarfile
import tempfile
import threading
import time
import traceback
import urllib.parse
import urllib.request
import uuid
from pathlib import Path
sys.dont_write_bytecode = True
import update_state as state
from common import deployment_lock
import upgrade

API = 'https://api.github.com/repos/' + state.REPO + '/releases?per_page=100'
DOWNLOAD = 'https://github.com/' + state.REPO + '/releases/download/'
ACTIVE = {'queued', 'downloading', 'verifying', 'backing_up', 'installing', 'restarting', 'recovering'}
lock = threading.RLock()
last_activity = time.monotonic()


class TrustedRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        trusted_url(newurl)
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def trusted_url(url):
    u = urllib.parse.urlsplit(url)
    if u.scheme != 'https' or u.username or u.password or u.port not in (None, 443) or u.hostname not in {'api.github.com', 'github.com', 'release-assets.githubusercontent.com', 'objects.githubusercontent.com'}:
        raise ValueError('更新下载地址不受信任')


def download(url, destination, maximum):
    trusted_url(url)
    req = urllib.request.Request(url, headers={'User-Agent': 'Guangyue-Panel-Updater', 'Accept': 'application/vnd.github+json' if url.startswith('https://api.github.com/') else 'application/octet-stream'})
    opener = urllib.request.build_opener(TrustedRedirect())
    start = time.monotonic(); total = 0
    with opener.open(req, timeout=20) as response, Path(destination).open('xb') as output:
        if int(response.headers.get('Content-Length', '0')) > maximum: raise ValueError('更新文件超过大小限制')
        while True:
            b = response.read(64 << 10)
            if not b: break
            total += len(b)
            if total > maximum or time.monotonic() - start > 180: raise ValueError('更新下载超过资源限制')
            output.write(b)


def catalog(force=False):
    cached = state.read('catalog.json', {'checked_at': 0, 'releases': []})
    if cached['checked_at'] > time.time() - (60 if force else 900): return cached
    try:
        with tempfile.TemporaryDirectory(dir=state.ROOT) as temp:
            path = Path(temp) / 'releases.json'; download(API, path, 4 << 20)
            data = json.loads(path.read_text())
        if not isinstance(data, list): raise ValueError('版本来源响应无效')
        releases = []
        for row in data:
            if row.get('draft') or row.get('prerelease'): continue
            tag = row.get('tag_name', '')
            try: state.version(tag.removeprefix('v'))
            except ValueError: continue
            if not tag.startswith('v'): continue
            v = tag[1:]
            names = {a.get('name') for a in row.get('assets', []) if a.get('state') == 'uploaded'}
            editions = [e for e in ['lite', 'pro'] if asset_name(v, e) in names]
            if 'SHA256SUMS' not in names or not editions: continue
            releases.append({'version': v, 'name': str(row.get('name') or tag)[:160], 'notes': str(row.get('body') or '')[:10000], 'published_at': row.get('published_at'), 'url': 'https://github.com/' + state.REPO + '/releases/tag/' + tag, 'editions': editions})
        cached = {'checked_at': int(time.time()), 'releases': sorted(releases, key=lambda r: state.version(r['version']), reverse=True)}
        state.write('catalog.json', cached)
        return cached
    except Exception:
        return dict(cached, warning='版本来源暂不可用，请稍后重试')


def asset_name(v, edition):
    state.version(v)
    if edition not in {'lite', 'pro'}: raise ValueError('安装版本类型无效')
    return 'guangyue-panel-' + edition + '-' + v + '-linux-amd64.tar.gz'


def verify_payload(bundle):
    covered = set()
    for row in (bundle / 'SHA256SUMS').read_text().splitlines():
        digest, name = row.split('  ', 1); p = bundle / name
        if not re.fullmatch('[a-f0-9]{64}', digest) or name in covered or name.startswith('/') or '..' in Path(name).parts or p.is_symlink() or not p.resolve().is_relative_to(bundle.resolve()): raise ValueError('安装包文件清单无效')
        if hashlib.sha256(p.read_bytes()).hexdigest() != digest: raise ValueError('安装包校验失败')
        covered.add(name)
    required = {'VERSION', 'EDITION', 'bin/guangyue-linux-amd64', 'bin/hysteria-node-linux-amd64', 'web/index.html', 'deploy/install.py', 'scripts/fetch-xray.sh', 'scripts/fetch-mihomo.sh'}
    if not required <= covered: raise ValueError('安装包缺少必要文件')
    for p in bundle.rglob('*'):
        name = p.relative_to(bundle).as_posix()
        if p.is_symlink() or p.is_file() and name not in covered | {'SHA256SUMS', 'bin/xray-linux-amd64', 'bin/mihomo-linux-amd64'}: raise ValueError('安装包包含未经校验的文件')


def extract(archive, destination, prefix):
    total = 0; seen = set()
    with tarfile.open(archive, 'r:gz') as tar:
        for member in tar:
            name = member.name
            if len(seen) >= 10000 or name in seen or name.startswith('/') or '..' in Path(name).parts or Path(name).parts[0] != prefix or not (member.isfile() or member.isdir()): raise ValueError('安装包包含不安全路径或链接')
            seen.add(name); total += member.size
            if total > 300 << 20: raise ValueError('安装包解压大小超过限制')
            target = destination / name
            if member.isdir(): target.mkdir(mode=0o755, parents=True, exist_ok=True)
            else:
                target.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
                with tar.extractfile(member) as source, target.open('xb') as output: shutil.copyfileobj(source, output, 64 << 10)
                target.chmod(0o755 if name.startswith(prefix + '/bin/') else 0o644)
    return destination / prefix


def cores(bundle):
    tree = ast.parse((bundle / 'deploy/install.py').read_text())
    hashes = None
    for node in tree.body:
        if isinstance(node, ast.Assign) and any(isinstance(t, ast.Name) and t.id == 'UPSTREAM_HASHES' for t in node.targets): hashes = ast.literal_eval(node.value)
    if not isinstance(hashes, dict) or set(hashes) != {'xray', 'mihomo'} or not all(re.fullmatch('[a-f0-9]{64}', h) for h in hashes.values()): raise ValueError('代理核心清单无效')
    for name, expected in hashes.items():
        source = state.APP / 'bin' / name; target = bundle / 'bin' / (name + '-linux-amd64')
        if source.is_file() and hashlib.sha256(source.read_bytes()).hexdigest() == expected: shutil.copyfile(source, target)
        elif not target.is_file() or hashlib.sha256(target.read_bytes()).hexdigest() != expected:
            env = dict(os.environ, GY_BIN_DIR=str(bundle / 'bin'))
            subprocess.run(['bash', str(bundle / 'scripts' / ('fetch-' + name + '.sh'))], check=True, timeout=300, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        if hashlib.sha256(target.read_bytes()).hexdigest() != expected: raise ValueError('代理核心校验失败')
        target.chmod(0o755)


def fetch_bundle(target, edition, temp):
    name = asset_name(target, edition); sums = temp / 'SHA256SUMS'
    download(DOWNLOAD + 'v' + target + '/SHA256SUMS', sums, 64 << 10)
    entries = [line.split('  ', 1) for line in sums.read_text().splitlines()]
    matches = [h for h, n in entries if n == name and re.fullmatch('[a-f0-9]{64}', h)]
    if len(matches) != 1: raise ValueError('发行版缺少唯一的安装包校验值')
    archive = temp / name; download(DOWNLOAD + 'v' + target + '/' + name, archive, 100 << 20)
    if hashlib.sha256(archive.read_bytes()).hexdigest() != matches[0]: raise ValueError('下载文件校验失败')
    return extract(archive, temp, name.removesuffix('.tar.gz'))


def status():
    config = json.loads(state.CONFIG.read_text()); current = state.current()
    cached = state.read('catalog.json', {'checked_at': 0, 'releases': []})
    releases = [r for r in cached['releases'] if config.get('edition', 'lite') in r['editions'] and state.version(r['version']) >= state.version(current)]
    rollback = [{k: r[k] for k in ['version', 'created', 'compatible']} for r in state.candidates()]
    return {'available': True, 'current_version': current, 'installed_version': state.baseline()['version'], 'checked_at': cached['checked_at'], 'releases': releases, 'rollback_versions': rollback, 'operation': state.read('operation.json'), 'refresh_seconds': 8}


def progress(stage):
    with lock:
        op = state.read('operation.json')
        if op:
            op.update(stage=stage, updated_at=int(time.time())); state.write('operation.json', op)


def execute(request, local_bundle=None):
    try:
        with deployment_lock():
            if state.current() != request['expected_version']: raise ValueError('当前版本已变化，请重新检测')
            target = request['version']; state.require_floor(target)
            upgrade.preflight()
            if request['action'] == 'rollback':
                upgrade.rollback(target, progress)
            else:
                if state.version(target) <= state.version(state.current()): raise ValueError('请选择高于当前版本的更新')
                edition = json.loads(state.CONFIG.read_text()).get('edition', 'lite')
                with tempfile.TemporaryDirectory(prefix='release-', dir=state.ROOT) as temp:
                    progress('downloading')
                    bundle = Path(local_bundle) if local_bundle else fetch_bundle(target, edition, Path(temp))
                    progress('verifying'); verify_payload(bundle)
                    if (bundle / 'VERSION').read_text().strip() != target or (bundle / 'EDITION').read_text().strip() != edition: raise ValueError('安装包版本或类型不匹配')
                    cores(bundle)
                    upgrade.upgrade(bundle, progress=progress)
            progress('succeeded')
    except Exception as exc:
        # Record locations and exception type only; command arguments/output may
        # contain credentials and must never reach the journal or browser.
        frames = ','.join(Path(f.filename).name + ':' + str(f.lineno) for f in traceback.extract_tb(exc.__traceback__))
        detail = ''
        if isinstance(exc, subprocess.CalledProcessError):
            stderr = (exc.stderr or b'').decode(errors='replace') if isinstance(exc.stderr, bytes) else (exc.stderr or '')
            markers = ['permission denied', 'read-only file system', 'operation not permitted', 'resource temporarily unavailable', 'no such file', 'already has a controller', 'could not acquire site controller lease', 'incomplete application configuration', 'runtime: failed', 'pam', 'rlimit', 'getcwd']
            detail = ' exit=' + str(exc.returncode) + ' reasons=' + ','.join(m for m in markers if m in stderr.lower())
        print('Update failure ' + type(exc).__name__ + ' at ' + frames + detail, file=sys.stderr, flush=True)
        with lock:
            op = state.read('operation.json') or dict(request)
            op.update(stage='failed', updated_at=int(time.time()), error=str(exc) if isinstance(exc, ValueError) else '更新未完成，已尝试恢复原版本；请检查运行状态')
            state.write('operation.json', op)
    finally:
        global last_activity
        last_activity = time.monotonic()


def submit(request, launch=True):
    if not isinstance(request, dict) or set(request) != {'action', 'version', 'expected_version', 'request_id'} or request['action'] not in {'update', 'rollback'}: raise ValueError('更新请求无效')
    if not isinstance(request['request_id'], str) or not re.fullmatch('[a-f0-9-]{36}', request['request_id']): raise ValueError('更新请求标识无效')
    state.version(request['version']); state.version(request['expected_version']); state.require_floor(request['version'])
    with lock:
        op = state.read('operation.json')
        if op and op['request_id'] == request['request_id']:
            if any(op.get(k) != request[k] for k in request): raise ValueError('更新请求标识已使用')
            return op
        if op and op['stage'] in ACTIVE: raise ValueError('已有版本操作正在执行')
        seen = state.read('requests.json', [])
        if request['request_id'] in seen: raise ValueError('更新请求已处理，请刷新状态')
        if request['expected_version'] != state.current(): raise ValueError('当前版本已变化，请重新检测')
        if request['action'] == 'update':
            config = json.loads(state.CONFIG.read_text())
            available = state.read('catalog.json', {'checked_at': 0, 'releases': []})
            if available['checked_at'] < time.time() - 900: raise ValueError('请先检查更新后再选择版本')
            if not any(r['version'] == request['version'] and config.get('edition', 'lite') in r['editions'] for r in available['releases']) or state.version(request['version']) <= state.version(state.current()): raise ValueError('请选择已发布的兼容新版本')
        else:
            chosen = next((r for r in state.candidates() if r['version'] == request['version']), None)
            if not chosen or not chosen['compatible']: raise ValueError('此版本不具备兼容的本机回退备份')
        op = dict(request, stage='queued', started_at=int(time.time()), updated_at=int(time.time()))
        state.write('requests.json', (seen + [request['request_id']])[-100:]); state.write('operation.json', op)
        if launch: threading.Thread(target=execute, args=(request,), daemon=False).start()
        return op


def recover():
    journal = state.read('transaction.json'); op = state.read('operation.json')
    if not journal and not (op and op['stage'] in ACTIVE): return
    try:
        with deployment_lock():
            if journal:
                if journal['phase'] == 'preparing':
                    upgrade.run('systemctl', 'start', *upgrade.UNITS)
                    upgrade.health(journal['previous'])
                else:
                    p = Path(journal['backup'])
                    if not p.is_relative_to('/root/guangyue-backups'): raise ValueError('恢复记录无效')
                    if journal['phase'] == 'healthy':
                        upgrade.health(journal['target'])
                        state.remember(p, json.loads((p / 'config.json').read_text()), journal['schema'])
                    else:
                        upgrade.restore_current(p)
                state.write('transaction.json', None)
            if op and op['stage'] in ACTIVE:
                op.update(stage='failed', error='上次版本操作被中断，请核对当前版本后重试', updated_at=int(time.time())); state.write('operation.json', op)
    except ValueError as exc:
        if 'another install' in str(exc): return
        raise


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args): pass
    def do_GET(self): self.handle_api()
    def do_POST(self): self.handle_api()
    def handle_api(self):
        global last_activity
        last_activity = time.monotonic()
        try:
            if self.path == '/state' and self.command == 'GET': result = status()
            elif self.path == '/check' and self.command == 'POST':
                with lock:
                    if (state.read('operation.json') or {}).get('stage') in ACTIVE: raise ValueError('已有版本操作正在执行')
                    c = catalog(True); result = dict(status(), warning=c.get('warning', ''))
            elif self.path == '/apply' and self.command == 'POST':
                size = int(self.headers.get('Content-Length', '0'))
                if size <= 0 or size > 1024: raise ValueError('更新请求过大或为空')
                result = submit(json.loads(self.rfile.read(size)))
            else:
                self.send_error(404); return
            payload = json.dumps(result, ensure_ascii=False).encode(); self.send_response(200)
        except Exception as exc:
            payload = json.dumps({'error': str(exc) if isinstance(exc, ValueError) else '更新服务暂不可用，请检查运行状态'}, ensure_ascii=False).encode(); self.send_response(409)
        self.send_header('Content-Type', 'application/json'); self.send_header('Content-Length', str(len(payload))); self.end_headers(); self.wfile.write(payload)


class Server(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    daemon_threads = True
    def get_request(self):
        connection, address = super().get_request(); connection.settimeout(25); return connection, address


def serve():
    state.baseline(); recover()
    if os.environ.get('LISTEN_PID') != str(os.getpid()) or os.environ.get('LISTEN_FDS') != '1': raise ValueError('更新服务必须由 systemd socket 启动')
    server = Server(state.SOCKET, Handler, bind_and_activate=False)
    server.socket.close(); server.socket = socket.socket(fileno=3); server.server_address = state.SOCKET
    server.timeout = 2
    while True:
        server.handle_request()
        with lock:
            if time.monotonic() - last_activity > 120 and (state.read('operation.json') or {}).get('stage') not in ACTIVE: break
    server.server_close()


if __name__ == '__main__':
    if os.geteuid() != 0: raise SystemExit('Updater requires root')
    os.umask(0o077)
    try:
        if sys.argv[1:] == ['serve']: serve()
        elif sys.argv[1:] == ['status']: print(json.dumps(status(), ensure_ascii=False))
        elif len(sys.argv) == 3 and sys.argv[1] == 'install-bundle':
            if (state.read('operation.json') or {}).get('stage') in ACTIVE:
                raise ValueError('已有版本操作正在执行')
            bundle = Path(sys.argv[2]).resolve(); target = (bundle / 'VERSION').read_text().strip()
            request = dict(action='update', version=target, expected_version=state.current(), request_id=str(uuid.uuid4()))
            state.write('operation.json', dict(request, stage='queued')); execute(request, bundle)
            if state.read('operation.json')['stage'] != 'succeeded': raise ValueError(state.read('operation.json')['error'])
        elif len(sys.argv) == 3 and sys.argv[1] == 'rollback':
            with deployment_lock(): upgrade.preflight(); upgrade.rollback(sys.argv[2])
        else: raise ValueError('Use serve, status, install-bundle PATH, or rollback VERSION')
    except Exception as exc:
        raise SystemExit(str(exc) if isinstance(exc, ValueError) else 'Update operation failed; inspect service health and private backup')
