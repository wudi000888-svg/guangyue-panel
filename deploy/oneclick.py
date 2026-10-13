#!/usr/bin/env python3
"""Interactive, receipt-based installer. Inspection never changes host services."""
import argparse
import errno
import fcntl
import hashlib
import importlib
import ipaddress
import json
import os
import platform
import re
import shutil
import socket
import ssl
import stat
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
REPO = 'wudi000888-svg/guangyue-panel'
API = 'https://api.github.com/repos/' + REPO + '/releases'
DOWNLOAD = 'https://github.com/' + REPO + '/releases/download/'
ROOT = Path('/var/lib/guangyue-installer')
SUPPORTED = {('debian', '12'), ('debian', '13'), ('ubuntu', '22.04'), ('ubuntu', '24.04')}
TRUSTED_DOWNLOAD_HOSTS = {'github.com', 'api.github.com', 'release-assets.githubusercontent.com', 'objects.githubusercontent.com'}
VERSION_RE = re.compile(r'(?:0|[1-9][0-9]{0,5})\.(?:0|[1-9][0-9]{0,5})\.(?:0|[1-9][0-9]{0,5})\Z')
TERMINAL_STAGES = {'installed', 'failed'}


def command(*args, timeout=30):
    environment = None
    if args and args[0] == 'docker':
        # Docker contexts are an administrator preference for other projects,
        # and may select a remote host. Installation inspects only this local
        # server and never silently contacts or changes a remote daemon.
        args = ('docker', '--host', 'unix:///var/run/docker.sock', *args[1:])
        environment = {key: value for key, value in os.environ.items() if not key.startswith('DOCKER_')}
    result = subprocess.run(args, capture_output=True, text=True, timeout=timeout, env=environment)
    if result.returncode:
        raise ValueError('命令未完成：' + Path(args[0]).name + '；原有服务未被停用')
    return result.stdout


def redact(text):
    text = re.sub(r'(?i)(postgres(?:ql)?|rediss?)://[^\s\"\']+', r'\1://[已隐藏]', str(text))
    text = re.sub(r'(?i)(password|secret|token|private[_ -]?key|authorization)(\s*[=:]\s*)[^\s,;]+', r'\1\2[已隐藏]', text)
    text = re.sub(r'g(?:yp|ye|yb)_[A-Za-z0-9_-]{16,}', '[已隐藏令牌]', text)
    return text[:2000]


class TrustedRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        assert_trusted_download(newurl)
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def assert_trusted_download(url):
    u = urllib.parse.urlsplit(url)
    if u.scheme != 'https' or u.hostname not in TRUSTED_DOWNLOAD_HOSTS or u.username or u.password or u.port not in (None, 443):
        raise ValueError('拒绝非官方 HTTPS 下载或不可信重定向')


def sync_directory(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def download(url, destination, maximum):
    assert_trusted_download(url)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), TrustedRedirect())
    req = urllib.request.Request(url, headers={'User-Agent': 'Guangyue-Installer/1', 'Accept': 'application/octet-stream'})
    destination = Path(destination)
    # A hard interruption can leave a partial download behind. A private,
    # uniquely named file lets the next resume proceed without trusting or
    # truncating the residue of an earlier attempt.
    fd, temporary = tempfile.mkstemp(prefix='.' + destination.name + '-', suffix='.part', dir=destination.parent)
    temp = Path(temporary)
    count, started = 0, time.monotonic()
    last_progress = started
    label = re.sub(r'[^A-Za-z0-9._-]', '_', Path(destination).name)[:180]
    print('正在下载 ' + label + '…', flush=True)
    try:
        with os.fdopen(fd, 'wb') as output, opener.open(req, timeout=30) as response:
            declared = response.headers.get('Content-Length')
            if declared and int(declared) > maximum:
                raise ValueError('下载文件超过安全大小限制')
            while True:
                block = response.read(1 << 20)
                if not block:
                    break
                count += len(block)
                now = time.monotonic()
                if count > maximum or now - started > 900:
                    raise ValueError('下载文件超过安全限制，请重试')
                output.write(block)
                if now - last_progress >= 20:
                    # No source URL, redirect token, request header or response
                    # body is ever included in the terminal/journal progress.
                    print(f'下载 {label}：已接收 {count / (1 << 20):.1f} MiB', flush=True)
                    last_progress = now
            output.flush(); os.fsync(output.fileno())
        os.replace(temp, destination)
        sync_directory(destination.parent)
        print(f'下载完成 {label}：{count / (1 << 20):.1f} MiB，准备校验', flush=True)
    finally:
        temp.unlink(missing_ok=True)


def checked_version(value):
    if not isinstance(value, str) or not VERSION_RE.fullmatch(value):
        raise ValueError('版本号无效，请使用完整正式版本，例如 0.41.0')
    return value


def release_catalog(version=None):
    url = API + ('/tags/v' + checked_version(version) if version else '?per_page=30')
    with tempfile.TemporaryDirectory(prefix='guangyue-release-check-') as directory:
        path = Path(directory) / 'catalog.json'; download(url, path, 4 << 20)
        value = json.loads(path.read_text())
    for release in [value] if isinstance(value, dict) else value:
        if release.get('draft') or release.get('prerelease'):
            continue
        tag = release.get('tag_name', '')
        if not tag.startswith('v') or not VERSION_RE.fullmatch(tag[1:]):
            continue
        selected = tag[1:]
        expected = {asset_name(selected, 'lite'), asset_name(selected, 'pro'), 'SHA256SUMS', 'SBOM.spdx.json', 'install.sh', 'DOCKER_IMAGES.json', 'guangyue-installer-' + selected + '.tar.gz', 'guangyue-panel-docker-' + selected + '-linux-amd64.tar.gz'}
        assets = {a.get('name'): a for a in release.get('assets', []) if a.get('size', 0) > 0}
        if not expected <= assets.keys():
            continue
        for name in expected:
            if assets[name].get('browser_download_url') != DOWNLOAD + tag + '/' + name:
                raise ValueError('正式 Release 下载地址与项目不匹配')
        return {'version': selected, 'assets': assets}
    raise ValueError('未找到包含完整一键安装资产的正式 Release，请稍后重试')


def asset_name(version, edition):
    if edition not in ('lite', 'pro'):
        raise ValueError('版本类型无效')
    return 'guangyue-panel-' + edition + '-' + checked_version(version) + '-linux-amd64.tar.gz'


def manifest_rows(content):
    result = {}
    for row in content.splitlines():
        if not row.strip():
            continue
        match = re.fullmatch(r'([a-f0-9]{64})  ([A-Za-z0-9][A-Za-z0-9._/-]{0,240})', row)
        if not match:
            raise ValueError('SHA256 清单格式无效')
        digest, name = match.groups()
        if name in result or any(p in ('.', '..') for p in name.split('/')):
            raise ValueError('SHA256 清单包含重复或不安全文件')
        result[name] = digest
    if not result:
        raise ValueError('SHA256 清单为空')
    return result


def sha256(path):
    h = hashlib.sha256()
    with Path(path).open('rb') as stream:
        for block in iter(lambda: stream.read(1 << 20), b''):
            h.update(block)
    return h.hexdigest()


def fetch_release_asset(version, name, destination, max_bytes=300 << 20):
    checked_version(version)
    if not re.fullmatch('[A-Za-z0-9][A-Za-z0-9._-]{0,180}', name):
        raise ValueError('安装资产名称无效')
    destination = Path(destination)
    destination.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='checksums-', dir=destination.parent) as directory:
        sums = Path(directory) / 'SHA256SUMS'; download(DOWNLOAD + 'v' + version + '/SHA256SUMS', sums, 64 << 10)
        expected = manifest_rows(sums.read_text()).get(name)
    if not expected:
        raise ValueError('安装资产没有官方 SHA256 校验记录')
    if destination.exists():
        if destination.is_symlink() or not destination.is_file() or sha256(destination) != expected:
            raise ValueError('已有下载文件校验失败，请移走损坏文件后重试')
        return destination
    download(DOWNLOAD + 'v' + version + '/' + name, destination, max_bytes)
    if sha256(destination) != expected:
        destination.unlink(missing_ok=True)
        raise ValueError('安装资产 SHA256 校验失败')
    return destination


def safe_extract(archive, destination, prefix, max_bytes=800 << 20):
    destination = Path(destination)
    if destination.exists():
        raise ValueError('解包目标已存在，拒绝覆盖')
    total, seen = 0, set()
    with tarfile.open(archive, 'r:gz') as tar:
        members = []
        for item in tar:
            if len(members) >= 20000:
                raise ValueError('安装包文件数量超限')
            parts = item.name.split('/')
            if item.name in seen or len(item.name) > 400 or '\\' in item.name or parts[0] != prefix or any(p in ('.', '..', '') for p in parts) or item.name.startswith('/') or not (item.isfile() or item.isdir()):
                raise ValueError('安装包包含不安全路径、链接或设备')
            seen.add(item.name); total += item.size
            if item.size < 0 or total > max_bytes:
                raise ValueError('安装包解压大小超限')
            members.append(item)
        destination.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        staging = Path(tempfile.mkdtemp(prefix='.' + destination.name + '-extract-', dir=destination.parent))
        try:
            for item in members:
                target = staging.joinpath(*item.name.split('/')[1:])
                if item.isdir():
                    target.mkdir(mode=0o755, parents=True, exist_ok=True)
                else:
                    target.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
                    with tar.extractfile(item) as source, target.open('xb') as output:
                        shutil.copyfileobj(source, output)
                        output.flush(); os.fsync(output.fileno())
                    target.chmod(0o755 if item.mode & 0o111 else 0o644)
            # The canonical bundle directory becomes visible only after all
            # regular files have been copied; SIGKILL cannot expose a half
            # extracted bundle to the subsequent receipt-based resume.
            os.rename(staging, destination)
            sync_directory(destination.parent)
        finally:
            if staging.exists():
                shutil.rmtree(staging)
    return destination


def verify_bundle(directory, plan):
    directory = Path(directory)
    rows = manifest_rows((directory / 'SHA256SUMS').read_text())
    required = {'VERSION', 'EDITION', 'bin/guangyue-linux-amd64', 'bin/guangyue-gateway-linux-amd64', 'bin/hysteria-node-linux-amd64', 'bin/xray-linux-amd64', 'bin/mihomo-linux-amd64', 'web/index.html', 'deploy/oneclick.py'}
    if not required <= rows.keys() or any(not (directory / name).is_file() for name in required):
        raise ValueError('安装包缺少完整的一键安装组件')
    for path in directory.rglob('*'):
        if path.is_symlink():
            raise ValueError('安装包包含不安全链接')
        if path.is_file():
            name = path.relative_to(directory).as_posix()
            if name != 'SHA256SUMS' and (name not in rows or sha256(path) != rows[name]):
                raise ValueError('安装包内部文件校验失败：' + name)
    if (directory / 'VERSION').read_text().strip() != plan['version'] or (directory / 'EDITION').read_text().strip() != plan['edition']:
        raise ValueError('安装包版本或版别与安装计划不一致')


def domain(value):
    value = value.strip().lower().rstrip('.')
    if len(value) > 63 or not re.fullmatch(r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+', value):
        raise ValueError('当前面板支持最多 63 字符的完整域名（并非 DNS 通用上限），不含 https://、端口或路径')
    return value


def tls_roots_preflight():
    try:
        if ssl.create_default_context().cert_store_stats().get('x509_ca', 0) > 0:
            return
    except (OSError, ValueError):
        pass
    raise ValueError('HTTPS CA 信任根缺失；请使用 bootstrap 补齐 ca-certificates，不会关闭 TLS 校验')


def available_memory():
    value = Path('/proc/meminfo').read_text()
    match = re.search(r'^MemAvailable:\s+(\d+)\s+kB$', value, re.M)
    if not match:
        raise ValueError('无法检测可用内存')
    available = int(match.group(1)) * 1024
    for limit_path, usage_path in (('/sys/fs/cgroup/memory.max', '/sys/fs/cgroup/memory.current'), ('/sys/fs/cgroup/memory/memory.limit_in_bytes', '/sys/fs/cgroup/memory/memory.usage_in_bytes')):
        if Path(limit_path).is_file() and Path(usage_path).is_file():
            limit = Path(limit_path).read_text().strip()
            if limit.isdigit():
                available = min(available, max(0, int(limit) - int(Path(usage_path).read_text().strip())))
    return available


def apt_locks_available():
    for name in ('/var/lib/dpkg/lock', '/var/lib/dpkg/lock-frontend', '/var/lib/apt/lists/lock', '/var/cache/apt/archives/lock'):
        path = Path(name)
        if not path.exists():
            continue
        fd = os.open(path, os.O_RDWR | os.O_NOFOLLOW)
        try:
            try:
                fcntl.lockf(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                raise ValueError('系统包管理正在运行，请等待 APT/dpkg 完成后重试') from None
            fcntl.lockf(fd, fcntl.LOCK_UN)
        finally:
            os.close(fd)


def port_preflight(include_internal=False, allowed_bindings=frozenset()):
    ports = [(socket.SOCK_STREAM, 443), (socket.SOCK_DGRAM, 443)]
    if include_internal:
        ports += [(socket.SOCK_STREAM, p) for p in (18443, 19100, 19101, 19185, 19186, 19199)] + [(socket.SOCK_DGRAM, 19186)]
    for family, address in ((socket.AF_INET, '0.0.0.0'), (socket.AF_INET6, '::')):
        for kind, port in ports:
            try:
                with socket.socket(family, kind) as probe:
                    if family == socket.AF_INET6:
                        probe.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 1)
                    probe.bind((address, port))
            except OSError as exc:
                if family == socket.AF_INET6 and exc.errno in (errno.EAFNOSUPPORT, errno.EPROTONOSUPPORT, errno.EADDRNOTAVAIL):
                    break
                if exc.errno == errno.EADDRINUSE and (family, kind, port) in allowed_bindings:
                    continue
                protocol = 'TCP' if kind == socket.SOCK_STREAM else 'UDP'
                detail = ''
                if shutil.which('ss'):
                    try:
                        rows = command('ss', '-H', '-lntup').splitlines()
                        relevant = [redact(row) for row in rows if re.search(r':' + str(port) + r'\b', row)]
                        if relevant:
                            detail = '；占用信息：' + ' | '.join(relevant[:3])[:700]
                    except ValueError:
                        pass
                raise ValueError(f'{protocol} {port} 已被占用或无法绑定{detail}；不会停用其他服务，请自行检查 ss -lntup') from None


def nginx_preflight():
    if not shutil.which('nginx'):
        return
    value = command('nginx', '-T')
    # Include inactive-but-configured listeners: a later nginx reload must not
    # unexpectedly claim the fixed public gateway entry.
    for line in value.splitlines():
        line = line.split('#', 1)[0]
        match = re.match(r'^\s*listen\s+([^;\s]+)', line)
        if match and (match.group(1) == '443' or re.search(r':443$', match.group(1))):
            raise ValueError('已有 Nginx 配置声明监听 443；请先由管理员自行调整，一键安装不会修改它')


def docker_preflight():
    if not shutil.which('docker'):
        return {'available': False}
    # A daemon that is installed but unavailable is not restarted or repaired.
    command('docker', 'info', '--format', '{{json .}}')
    ids = command('docker', 'ps', '-aq').split()
    if ids:
        values = json.loads(command('docker', 'inspect', *ids))
        for item in values:
            for bindings in (item.get('HostConfig', {}).get('PortBindings') or {}).values():
                for binding in bindings or []:
                    if str(binding.get('HostPort')) == '443':
                        raise ValueError('已有 Docker 容器发布 443 端口，不会停止或重新配置该容器')
    return {'available': True}


def public_ips():
    found = set()
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    for url in ('https://api.ipify.org', 'https://api6.ipify.org'):
        try:
            req = urllib.request.Request(url, headers={'User-Agent': 'Guangyue-Installer/1'})
            with opener.open(req, timeout=8) as response:
                address = ipaddress.ip_address(response.read(100).decode().strip())
                if address.is_global:
                    found.add(str(address))
        except (OSError, ValueError, urllib.error.URLError):
            pass
    # Multi-IP hosts may receive traffic on another globally assigned IPv4
    # address than the outward source picked by the HTTPS IP probe.
    if shutil.which('ip'):
        try:
            ipv6_verified = any(ipaddress.ip_address(value).version == 6 for value in found)
            for interface in json.loads(command('ip', '-j', 'address', 'show', 'scope', 'global')):
                for info in interface.get('addr_info', []):
                    address = ipaddress.ip_address(info.get('local', ''))
                    if address.is_global and (address.version == 4 or ipv6_verified):
                        found.add(str(address))
        except (ValueError, json.JSONDecodeError):
            pass
    if not found:
        raise ValueError('无法检测本机公网 IP；请检查外网 HTTPS 和 DNS，再重试')
    return found


def domain_preflight(domains, addresses, backend='native'):
    for name in set(domains):
        try:
            resolved = {str(ipaddress.ip_address(row[4][0])) for row in socket.getaddrinfo(name, 443, type=socket.SOCK_STREAM)}
        except socket.gaierror:
            raise ValueError('域名 ' + name + ' 尚未解析；请先设置 DNS A/AAAA 记录') from None
        if backend == 'docker' and any(ipaddress.ip_address(value).version == 6 for value in resolved):
            raise ValueError('当前 Docker 公网入口仅支持 IPv4；请将 ' + name + ' 改为仅 A 记录并关闭 CDN，或选择原生 native 双栈部署')
        if not resolved or not resolved <= set(addresses):
            raise ValueError('域名 ' + name + ' 的 A/AAAA 记录未全部指向本机公网 IP；请关闭 CDN/代理并检查多余 AAAA 记录')


def clock_preflight():
    req = urllib.request.Request(API + '?per_page=1', headers={'User-Agent': 'Guangyue-Installer/1'})
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), TrustedRedirect())
    try:
        with opener.open(req, timeout=12) as response:
            from email.utils import parsedate_to_datetime
            value = response.headers.get('Date')
            if not value or abs(parsedate_to_datetime(value).timestamp() - time.time()) > 300:
                raise ValueError('服务器时间不准确；请修正时间同步后重试，安装器不会修改系统时钟')
    except (OSError, urllib.error.URLError):
        raise ValueError('无法检测时间与官方 Release 连通性，请检查 HTTPS 网络') from None


def preflight(plan, *, resuming=False):
    if os.geteuid() != 0:
        raise ValueError('请使用 root 或 sudo 运行安装器')
    release = platform.freedesktop_os_release()
    if platform.system() != 'Linux' or platform.machine() not in ('amd64', 'x86_64') or (release.get('ID'), release.get('VERSION_ID')) not in SUPPORTED:
        raise ValueError('当前仅支持 Debian 12/13、Ubuntu 22.04/24.04 的 amd64 服务器')
    if not Path('/run/systemd/system').is_dir():
        raise ValueError('需要以 systemd 启动的完整服务器；不支持普通容器和受限 LXC')
    if shutil.which('systemd-detect-virt'):
        virtual = subprocess.run(['systemd-detect-virt', '--container'], capture_output=True, text=True, timeout=5)
        if virtual.returncode == 0:
            raise ValueError('当前是容器/LXC 环境，一键安装只支持完整服务器，不会修改容器宿主设置')
    kernel = re.match(r'^(\d+)\.(\d+)', platform.release())
    if plan['backend'] == 'docker' and (not kernel or tuple(map(int, kernel.groups())) < (5, 4)):
        raise ValueError('Docker 后端要求 Linux 5.4 或更新内核')
    required_memory = (600 if plan['edition'] == 'lite' else 1400) << 20
    if available_memory() < required_memory:
        raise ValueError('当前可用内存不足；建议 Lite 至少 1 GiB、Pro 至少 2 GiB，并为其他程序保留内存')
    required_disk = (4 if plan['backend'] == 'docker' else 2) << 30
    if shutil.disk_usage('/var/lib').free < required_disk:
        raise ValueError('可用磁盘不足；原生至少 2 GiB、Docker 至少 4 GiB，另需为数据和备份预留空间')
    apt_locks_available()
    if shutil.which('dpkg') and command('dpkg', '--audit').strip():
        raise ValueError('系统存在未完成的软件包安装，请先修复 dpkg 后重试')
    nginx_preflight()
    allowed_bindings = frozenset()
    # A download failure happens before any backend journal exists. Such a
    # resume is a fresh host inspection, not permission to ignore old paths or
    # claim ownership of listeners. This also keeps network retries usable.
    backend_journal = ROOT / ('docker.json' if plan['backend'] == 'docker' else 'native.json')
    resume_owned = resuming and (backend_journal.exists() or backend_journal.is_symlink())
    if resume_owned:
        backend = importlib.import_module('docker_deploy' if plan['backend'] == 'docker' else 'native_gateway')
        backend.resume_preflight(plan)
        allowed_bindings = backend.resume_binding_owners(plan)
    docker = {'available': bool(shutil.which('docker'))} if resume_owned and plan['backend'] == 'docker' else docker_preflight()
    if not resume_owned:
        for managed in ('/etc/guangyue-personal.json', '/etc/guangyue-deployment.json', '/etc/guangyue-gateway.json', '/etc/guangyue-infrastructure.json', '/opt/guangyue-personal', '/var/lib/guangyue', '/opt/guangyue-docker', '/opt/guangyue-updater', '/var/lib/guangyue-updater'):
            if Path(managed).exists() or Path(managed).is_symlink():
                raise ValueError('检测到已有面板或保留数据；请使用 --resume 或既有更新入口，不会覆盖')
    port_preflight(plan['backend'] == 'native', allowed_bindings)
    clock_preflight()
    addresses = public_ips(); domain_preflight((plan['panel_domain'], plan['node_domain']), addresses, plan['backend'])
    return {'public_ips': sorted(addresses), 'os': release['ID'] + ' ' + release['VERSION_ID'], 'architecture': 'amd64', 'docker_available': docker['available'], 'public_udp_verified': False}


def private_directory(path):
    path = Path(path)
    for parent in reversed((path, *path.parents)):
        if parent.exists() and (parent.is_symlink() or not parent.is_dir() or parent.stat().st_uid != 0 or parent.stat().st_mode & 0o022):
            raise ValueError('安装状态目录存在不可信链接、权限或所有者')
    path.mkdir(mode=0o700, parents=True, exist_ok=True); path.chmod(0o700)


def write_receipt(receipt):
    private_directory(ROOT)
    target = ROOT / 'receipt.json'; temp = ROOT / 'receipt.json.new'
    fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as output:
        json.dump(receipt, output, ensure_ascii=False, indent=2); output.write('\n'); output.flush(); os.fsync(output.fileno())
    os.replace(temp, target)
    sync_directory(ROOT)
    # Operational logs deliberately contain only public plan identity and safe
    # phase/error text. Never serialize backend credentials or process output.
    event = {'at': int(time.time()), 'id': receipt['plan']['id'], 'version': receipt['plan']['version'], 'backend': receipt['plan']['backend'], 'stage': receipt['stage']}
    if receipt.get('error'):
        event['error'] = redact(receipt['error'])
    log = ROOT / 'installer.log'
    fd = os.open(log, os.O_WRONLY | os.O_CREAT | os.O_APPEND | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as output:
        json.dump(event, output, ensure_ascii=False); output.write('\n'); output.flush(); os.fsync(output.fileno())
    sync_directory(ROOT)


def load_receipt():
    path = ROOT / 'receipt.json'
    private_directory(ROOT)
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd) as source:
        info = os.fstat(source.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o077 or info.st_size > 1 << 20:
            raise ValueError('安装回执必须是 root 私有的普通文件')
        value = json.loads(source.read((1 << 20) + 1))
    validate_plan(value.get('plan', {}))
    if value.get('schema') != 1 or value.get('stage') not in {'inspected', 'approved', 'downloading', 'verified', 'installing', 'failed', 'installed'}:
        raise ValueError('安装回执无效，请保留现场并检查')
    return value


def validate_plan(plan):
    if plan.get('schema') != 1 or plan.get('edition') not in ('lite', 'pro') or plan.get('backend') not in ('native', 'docker'):
        raise ValueError('安装计划无效')
    checked_version(plan.get('version'))
    uuid.UUID(plan.get('id', ''))
    for key in ('panel_domain', 'node_domain'):
        if domain(plan.get(key, '')) != plan[key]:
            raise ValueError('安装计划域名无效')
    if plan.get('reality_sni') != 'www.cloudflare.com' or plan.get('site_id') != 'default' or plan.get('acme_agreed') is not True:
        raise ValueError('安装计划 ACME 确认或默认节点参数无效')
    if plan['reality_sni'] in (plan['panel_domain'], plan['node_domain']):
        raise ValueError('面板和节点域名必须与默认 Reality 目标不同')


def ask(prompt, default=''):
    with open('/dev/tty', 'r+') as terminal:
        terminal.write(prompt + (' [' + default + ']' if default else '') + '：'); terminal.flush()
        return terminal.readline().strip() or default


def interactive_plan(version):
    print('请先把域名解析到本服务器，并关闭 CDN/橙云代理。公网 TCP/UDP 固定为 443；Docker 当前仅 IPv4，需要 IPv6 时选择 native。')
    edition = ask('版本 lite/pro', 'lite').lower()
    panel = domain(ask('面板域名'))
    node = domain(ask('节点域名，回车使用同一域名', panel))
    backend = ask('部署方式 docker/native', 'docker').lower()
    print('自动申请和续期 HTTPS 证书需要同意 Let’s Encrypt 服务条款：https://letsencrypt.org/repository/')
    if ask('是否同意上述 ACME 条款 yes/no', 'no').lower() != 'yes':
        raise ValueError('未同意 ACME 条款，安装未开始')
    plan = {'schema': 1, 'id': str(uuid.uuid4()), 'version': version, 'edition': edition, 'backend': backend, 'panel_domain': panel, 'node_domain': node, 'reality_sni': 'www.cloudflare.com', 'site_id': 'default', 'acme_agreed': True, 'acme_email': '', 'created_at': int(time.time())}
    validate_plan(plan)
    return plan


def install_plan(receipt):
    plan = receipt['plan']; private_directory(ROOT)
    receipt['stage'] = 'downloading'; write_receipt(receipt)
    print('正在获取固定版本 v' + plan['version'] + ' 的 ' + plan['edition'].upper() + ' 完整安装包…', flush=True)
    directory = ROOT / ('release-' + plan['version'] + '-' + plan['edition'])
    archive = ROOT / asset_name(plan['version'], plan['edition'])
    fetch_release_asset(plan['version'], archive.name, archive)
    print('安装包外层 SHA256 通过，正在安全解包并检查所有组件…', flush=True)
    if not directory.exists():
        safe_extract(archive, directory, archive.name.removesuffix('.tar.gz'))
    verify_bundle(directory, plan)
    receipt['stage'] = 'verified'; write_receipt(receipt)
    print('安装组件与版别已核验，正在准备 ' + ('Docker' if plan['backend'] == 'docker' else '原生') + ' 服务和自动证书…', flush=True)
    # Execute only modules covered by the verified, version-pinned release.
    sys.path.insert(0, str(directory / 'deploy'))
    module = importlib.import_module('docker_deploy' if plan['backend'] == 'docker' else 'native_gateway')
    receipt['stage'] = 'installing'; write_receipt(receipt)
    result = module.install(plan, directory)
    receipt.update(stage='installed', result=result, finished_at=int(time.time()), error='')
    write_receipt(receipt)
    print('本机服务与目标版本健康检查通过。', flush=True)
    return result


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', help='指定正式版本，否则选择最新完整正式 Release')
    parser.add_argument('--resume', action='store_true', help='从私有安装回执继续；不重置密钥和数据库')
    parser.add_argument('--check', action='store_true', help='仅做只读预检查，不准备依赖和服务')
    parser.add_argument('--plan', type=Path, help='读取明确确认 ACME 的 JSON 安装计划')
    parser.add_argument('--yes', action='store_true', help='同意最终安装计划；仅与 --plan 或 --resume 同用')
    args = parser.parse_args(argv)
    tls_roots_preflight()
    if args.yes and not (args.plan or args.resume):
        raise ValueError('--yes 仅可与明确的 --plan 或 --resume 同用')
    if args.resume:
        receipt = load_receipt(); plan = receipt['plan']
        if args.version and args.version != plan['version']:
            raise ValueError('续装必须保持原版本；不能替换安装计划')
        if receipt['stage'] == 'installed':
            print('面板已完成安装：https://' + plan['panel_domain'] + '；再次运行不会初始化账号或密钥。')
            return
    else:
        version = release_catalog(args.version)['version']
        if args.plan:
            plan = json.loads(args.plan.read_text()); validate_plan(plan)
            if plan['version'] != version:
                raise ValueError('安装计划版本与所选完整 Release 不一致')
        else:
            plan = interactive_plan(version)
        receipt = {'schema': 1, 'plan': plan, 'stage': 'inspected'}
    print('正在只读检查系统、端口、Nginx/Docker、资源、域名、时间和下载通道…')
    evidence = preflight(plan, resuming=args.resume)
    plan['public_ips'] = evidence['public_ips']
    print(json.dumps({'安装版本': plan['version'], '版别': plan['edition'], '部署方式': plan['backend'], '面板': 'https://' + plan['panel_domain'], '节点域名': plan['node_domain'], '公网端口': 'TCP/UDP 443', '公网IP': evidence['public_ips'], '系统': evidence['os'], '宿主网络优化': '默认关闭', '公网UDP': '部署后仍需协议/安全组验证'}, ensure_ascii=False, indent=2))
    if args.check:
        print('只读预检查通过；没有修改服务、依赖、防火墙、Nginx 或系统网络参数。')
        return
    if not args.yes and ask('确认执行上述安装计划 yes/no', 'no').lower() != 'yes':
        print('已取消，没有安装服务。'); return
    from common import deployment_lock
    with deployment_lock():
        receipt['stage'] = 'approved'; write_receipt(receipt)
        try:
            result = install_plan(receipt)
        except Exception as exc:
            error = redact(str(exc)) if isinstance(exc, ValueError) else '安装未完成：' + type(exc).__name__
            receipt.update(stage='failed', error=error); write_receipt(receipt)
            print(error + '。私有回执：' + str(ROOT / 'receipt.json') + '；使用 --resume 继续，不会重置既有密钥。', file=sys.stderr)
            raise ValueError(error) from None
    print('安装完成：' + result.get('public_url', 'https://' + plan['panel_domain']))
    print('首次管理员凭据私有文件：' + result.get('credentials_file', '请查看后端安装回执') + '；登录后立即修改密码。')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, urllib.error.URLError, json.JSONDecodeError, subprocess.SubprocessError) as exc:
        raise SystemExit(redact(str(exc)) if isinstance(exc, ValueError) else '安装未完成：' + type(exc).__name__)
