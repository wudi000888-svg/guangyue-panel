#!/usr/bin/env python3
"""Owned Docker deployment and offline recovery; never mounts the Docker socket.

All commands are fixed argument arrays. Public maintenance is provided by the
separate host updater, not a privileged container or a shell from the browser.
"""
import gzip
import fcntl
import hashlib
import http.client
import io
import ipaddress
import json
import os
import pwd
import re
import secrets
import shutil
import socket
import ssl
import sqlite3
import stat
import subprocess
import sys
import tempfile
import time
import urllib.request
import urllib.parse
import uuid
from pathlib import Path

import oneclick

APP = Path('/opt/guangyue-personal')
STATE = Path('/var/lib/guangyue')
CONFIG = Path('/etc/guangyue-personal.json')
MARKER = Path('/etc/guangyue-deployment.json')
ROOT = Path('/opt/guangyue-docker')
MAINTENANCE = Path('/var/lib/guangyue-maintenance')
BACKUPS = Path('/root/guangyue-backups')
HELPER = Path('/opt/guangyue-updater')
POLICY = Path('/usr/sbin/policy-rc.d')
DAEMON_CONFIG = Path('/etc/docker/daemon.json')
SYSTEM_UNITS = Path('/etc/systemd/system')
GPG_HASH = '1500c1f56fa9e26b9b8f42452a553675796ade0807cdce11975eb98170b3a570'
ID_RE = re.compile(r'sha256:[a-f0-9]{64}\Z')
SERVICES = {'gateway', 'panel', 'postgres', 'redis'}
MAX_ARCHIVE = 900 << 20
MAX_EXPANDED = 4 << 30


def safe_json(path, maximum=1 << 20, missing=False):
    """Refuse symlink, device, insecure owner/mode before reading a byte."""
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    except FileNotFoundError:
        if missing:
            return None
        raise ValueError('缺少受保护的 Docker 部署文件') from None
    except OSError:
        raise ValueError('Docker 部署文件不是安全的普通文件') from None
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or stat.S_IMODE(info.st_mode) != 0o600 or info.st_size > maximum:
            raise ValueError('Docker 部署文件所有者、类型、大小或权限无效')
        with os.fdopen(fd, 'rb', closefd=False) as source:
            data = source.read(maximum + 1)
        if len(data) > maximum:
            raise ValueError('Docker 部署文件过大')
        return json.loads(data)
    except (UnicodeError, json.JSONDecodeError):
        raise ValueError('Docker 部署文件 JSON 无效') from None
    finally:
        os.close(fd)


def atomic_json(path, value, mode=0o600, uid=0, gid=0):
    path = Path(path)
    fd, name = tempfile.mkstemp(prefix='.docker-new-', dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as output:
            os.fchmod(output.fileno(), mode); os.fchown(output.fileno(), uid, gid)
            json.dump(value, output, ensure_ascii=False, indent=2); output.write('\n'); output.flush(); os.fsync(output.fileno())
        os.replace(name, path)
        directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try: os.fsync(directory)
        finally: os.close(directory)
    finally:
        if os.path.exists(name): os.unlink(name)


def run(*args, timeout=120, input=None, text=True):
    environment={k:v for k,v in os.environ.items() if not k.startswith('DOCKER_')}
    environment.update(LC_ALL='C',COMPOSE_IGNORE_ORPHANS='false')
    if args and args[0]=='docker':args=('docker','--host','unix:///var/run/docker.sock',*args[1:])
    try:
        result = subprocess.run(args, input=input, text=text, capture_output=True, timeout=timeout, check=True,
                                env=environment)
    except (OSError, subprocess.SubprocessError):
        raise ValueError('Docker 部署步骤未完成：' + Path(args[0]).name + '；私有诊断和凭据不会写入界面') from None
    return result.stdout


def project_name(installation_id):
    return 'guangyue_' + uuid.UUID(installation_id).hex[:12]


def validate_marker(value):
    if not isinstance(value, dict): raise ValueError('Docker 部署标记无效')
    allowed = {'schema', 'id', 'version', 'edition', 'backend', 'panel_domain', 'node_domain', 'reality_sni', 'site_id',
               'acme_agreed', 'acme_email', 'created_at', 'public_ips', 'private_gateway', 'project', 'compose_file',
               'compose_sha256', 'uid', 'gid', 'images', 'floor', 'installed_at', 'public_udp_verified'}
    if set(value) - allowed: raise ValueError('Docker 部署标记包含未知字段')
    oneclick.validate_plan(value)
    if value['backend'] != 'docker' or value.get('project') != project_name(value['id']) or value.get('compose_file') != str(ROOT / 'compose.json'):
        raise ValueError('Docker 项目标识或 Compose 路径不匹配')
    for key in ('uid', 'gid'):
        if type(value.get(key)) is not int or value[key] < 1 or value[key] > 2**31 - 1: raise ValueError('Docker 用户标识无效')
    images = value.get('images')
    if not isinstance(images, dict) or set(images) != {'app', 'postgres', 'redis'} or any(not ID_RE.fullmatch(v) for v in images.values() if isinstance(v, str)) or any(not isinstance(v, str) for v in images.values()):
        raise ValueError('Docker 镜像标識无效')
    if not re.fullmatch('[a-f0-9]{64}', value.get('compose_sha256', '')): raise ValueError('Compose 校验值无效')
    oneclick.checked_version(value.get('floor'))
    if version(value['floor']) > version(value['version']): raise ValueError('Docker 版本低于安装基线')
    return value


def installed():
    value = safe_json(MARKER, missing=True)
    if value is None: return False
    # A safe native marker is legitimate; an unsafe file never silently makes
    # the privileged updater fall back to legacy/native execution.
    if not isinstance(value, dict): raise ValueError('部署标记类型无效')
    if value.get('backend') == 'native':
        oneclick.validate_plan(value)
        return False
    if value.get('backend') != 'docker': raise ValueError('部署后端标记未知，拒绝回落到其他安装方式')
    validate_marker(value)
    return True


def load():
    marker = validate_marker(safe_json(MARKER))
    info = ROOT.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or stat.S_IMODE(info.st_mode) != 0o700:
        raise ValueError('Docker 受管目录权限无效')
    compose_path = ROOT / 'compose.json'
    composition = safe_json(compose_path)
    if oneclick.sha256(compose_path) != marker['compose_sha256']:
        raise ValueError('本项目 Compose 已被外部修改，拒绝覆盖或执行')
    if composition.get('name') != marker['project'] or set(composition.get('services', {})) - SERVICES:
        raise ValueError('Docker Compose 不是已登记的项目')
    return marker


def compose_for(marker, *args, timeout=180, input=None, text=True):
    if not args or args[0] not in {'up', 'stop', 'run', 'exec', 'ps', 'config', 'create', 'start', 'rm'}:
        raise ValueError('不支持的固定 Compose 操作')
    return run('docker', 'compose', '--project-name', marker['project'], '--project-directory', str(ROOT),
               '--file', marker['compose_file'], *args, timeout=timeout, input=input, text=text)


def compose(*args, **kwargs):
    return compose_for(load(), *args, **kwargs)


def journal_path(): return oneclick.ROOT / 'docker.json'
def save_journal(value):
    oneclick.private_directory(oneclick.ROOT); atomic_json(journal_path(), value)


def install_journal(plan):
    existing = safe_json(journal_path(), missing=True)
    if existing:
        if existing.get('id') != plan['id'] or existing.get('version') != plan['version'] or existing.get('project') != project_name(plan['id']):
            raise ValueError('Docker 安装现场属于另一个计划，拒绝覆盖')
        return existing
    value = {'id': plan['id'], 'version': plan['version'], 'project': project_name(plan['id']), 'phase': 'preparing', 'paths': [], 'containers': {}, 'credentials': {}}
    save_journal(value); return value


def own_path(path, journal):
    path = Path(path)
    if path.is_symlink() or path.exists() and str(path) not in journal['paths']:
        raise ValueError('Docker 受管路径已有其他数据，拒绝覆盖：' + path.name)
    if str(path) not in journal['paths']: journal['paths'].append(str(path)); save_journal(journal)


def container_inspect():
    ids = run('docker', 'ps', '-aq').split()
    if not ids: return []
    values = json.loads(run('docker', 'inspect', *ids))
    if not isinstance(values, list): raise ValueError('Docker 检查结果无效')
    return values


def owned_container(item, marker, journal=None):
    labels = item.get('Config', {}).get('Labels') or {}
    service = labels.get('com.docker.compose.service')
    return (labels.get('org.guangyue.install-id') == marker['id'] and
            labels.get('com.docker.compose.project') == marker['project'] and service in SERVICES and
            (journal is None or item.get('Id') in journal.get('containers', {}).values()))


def register_containers(marker, journal):
    values = container_inspect(); recorded = {}
    for item in values:
        labels = item.get('Config', {}).get('Labels') or {}
        if labels.get('com.docker.compose.project') == marker['project']:
            if not owned_container(item, marker): raise ValueError('相同 Docker 项目名已有其他安装的容器')
            service = labels['com.docker.compose.service']
            expected = marker['images']['app' if service in ('panel', 'gateway') else service]
            if item.get('Image') != expected: raise ValueError('受管容器镜像与已验证版本不匹配')
            recorded[service] = item['Id']
    journal['containers'] = recorded; save_journal(journal)


def intended_container(item, marker, journal):
    intent=journal.get('creation_intent') or {}
    if not owned_container(item,marker) or intent.get('compose_sha256')!=journal.get('compose_sha256'):
        return False
    path=ROOT/'compose.json'
    if path.is_symlink() or not path.is_file() or oneclick.sha256(path)!=intent.get('compose_sha256'):return False
    labels=item.get('Config',{}).get('Labels') or {};service=labels.get('com.docker.compose.service')
    key='app' if service in ('gateway','panel') else service
    return key in intent.get('images',{}) and item.get('Image')==intent['images'][key]


def resume_preflight(plan):
    journal = safe_json(journal_path())
    if journal.get('id') != plan['id'] or journal.get('project') != project_name(plan['id']) or journal.get('version') != plan['version']:
        raise ValueError('Docker 续装回执与当前计划不匹配')
    marker = {'id': plan['id'], 'project': journal['project']}
    for item in container_inspect():
        labels = item.get('Config', {}).get('Labels') or {}
        same_project = labels.get('com.docker.compose.project') == marker['project']
        is_ours=owned_container(item,marker,journal) or intended_container(item,marker,journal)
        if same_project and not is_ours: raise ValueError('续装遇到未登记的同名 Docker 项目')
        for key, bindings in (item.get('HostConfig', {}).get('PortBindings') or {}).items():
            for binding in bindings or []:
                if str(binding.get('HostPort')) == '443' and (not is_ours or labels.get('com.docker.compose.service') != 'gateway' or key not in ('443/tcp', '443/udp')):
                    raise ValueError('443 由其他 Docker 容器发布，拒绝续装接管')


def resume_binding_owners(plan):
    resume_preflight(plan)
    journal = safe_json(journal_path()); marker = {'id': plan['id'], 'project': journal['project']}
    gateways = [i for i in container_inspect() if (owned_container(i,marker,journal) or intended_container(i,marker,journal)) and (i.get('Config', {}).get('Labels') or {}).get('com.docker.compose.service') == 'gateway' and i.get('State', {}).get('Running')]
    if not gateways: return set()
    ips = {n.get('IPAddress') for g in gateways for n in g.get('NetworkSettings', {}).get('Networks', {}).values()} | {n.get('GlobalIPv6Address') for g in gateways for n in g.get('NetworkSettings', {}).get('Networks', {}).values()}
    ips.discard(''); ips.discard(None)
    owners = set()
    if not shutil.which('ss'): return owners
    for row in run('ss', '-H', '-lntup').splitlines():
        words = row.split()
        if len(words) < 5 or not words[4].endswith(':443'): continue
        proto = 'tcp' if words[0] == 'tcp' else 'udp' if words[0] == 'udp' else ''
        pids = re.findall(r'pid=(\d+)', row)
        if not proto or not pids: continue
        matches = True
        for pid in pids:
            try: args = Path('/proc', pid, 'cmdline').read_bytes().split(b'\0'); args = [a.decode() for a in args if a]
            except (OSError, UnicodeError): matches = False; break
            options = dict(zip(args[1::2], args[2::2]))
            if not args or Path(args[0]).name != 'docker-proxy' or options.get('-container-ip') not in ips or options.get('-container-port') != '443' or options.get('-host-port') != '443' or options.get('-proto') != proto:
                matches = False; break
        if matches:
            address = words[4].rsplit(':', 1)[0].strip('[]')
            family = socket.AF_INET6 if ':' in address else socket.AF_INET
            owners.add((family, socket.SOCK_STREAM if proto == 'tcp' else socket.SOCK_DGRAM, 443))
    return owners


def official_docker_key(path, distribution):
    url = 'https://download.docker.com/linux/' + distribution + '/gpg'
    request = urllib.request.Request(url, headers={'User-Agent': 'Guangyue-Installer/1'})
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self,*args,**kwargs):raise ValueError('Docker 公钥不允许重定向')
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}),NoRedirect())
    with opener.open(request, timeout=30) as response:
        if response.geturl() != url: raise ValueError('Docker 官方公钥下载发生非预期重定向')
        key = response.read((64 << 10) + 1)
    if len(key) > 64 << 10 or hashlib.sha256(key).hexdigest() != GPG_HASH:
        raise ValueError('Docker 官方公钥校验失败，未添加软件源')
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o644)
    with os.fdopen(fd, 'wb') as output: output.write(key); output.flush(); os.fsync(output.fileno())


def package_policy_text(journal):
    return '#!/bin/sh\n# Guangyue Docker dependency policy '+str(uuid.UUID(journal['id']))+'\nexit 101\n'


def remove_package_policy(journal):
    expected=journal.get('package_policy')
    if expected and POLICY.is_file() and not POLICY.is_symlink() and POLICY.stat().st_uid==0 and oneclick.sha256(POLICY)==expected:POLICY.unlink()


def package_policy_hook(start):
    if os.geteuid()!=0:raise ValueError('依赖策略钩子需要 root')
    journal=safe_json(journal_path())
    content=package_policy_text(journal)
    if journal.get('package_policy')!=hashlib.sha256(content.encode()).hexdigest():raise ValueError('依赖策略未获得当前安装回执授权')
    if not start:remove_package_policy(journal);return
    descriptor=os.open('/var/lib/dpkg/lock-frontend',os.O_RDWR|os.O_NOFOLLOW)
    try:
        try:fcntl.lockf(descriptor,fcntl.LOCK_EX|fcntl.LOCK_NB)
        except BlockingIOError:pass
        else:fcntl.lockf(descriptor,fcntl.LOCK_UN);raise ValueError('APT 未持有前端锁，拒绝临时系统策略')
    finally:os.close(descriptor)
    if POLICY.exists() or POLICY.is_symlink():raise ValueError('已有 policy-rc.d，不会覆盖')
    fd=os.open(POLICY,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o755)
    with os.fdopen(fd,'w') as output:output.write(content);output.flush();os.fsync(output.fileno())


def prepare_inspection_tools(journal):
    if shutil.which('ip'):return
    oneclick.apt_locks_available();run('apt-get','update','-qq',timeout=240)
    simulated=run('apt-get','-s','--no-install-recommends','--no-upgrade','install','iproute2')
    if any(row.startswith('Remv ') or row.startswith('Inst ') and '[' in row.split('(',1)[0] for row in simulated.splitlines()):
        raise ValueError('网络预检依赖需要升级或删除既有包，拒绝继续')
    remove_package_policy(journal)
    if POLICY.exists() or POLICY.is_symlink():raise ValueError('已有软件包启动策略，请自行安装 iproute2 后重试')
    content=package_policy_text(journal);journal['package_policy']=hashlib.sha256(content.encode()).hexdigest();save_journal(journal)
    import shlex
    hook=shlex.quote(sys.executable)+' '+shlex.quote(str(Path(__file__).resolve()))
    try:
        run('env','DEBIAN_FRONTEND=noninteractive','apt-get','-y','--no-install-recommends','--no-upgrade','-o','DPkg::Lock::Timeout=30',
            '-o','DPkg::Pre-Invoke::='+hook+' --package-policy-start','-o','DPkg::Post-Invoke::='+hook+' --package-policy-stop','install','iproute2',timeout=600)
    finally:remove_package_policy(journal);journal.pop('package_policy',None);save_journal(journal)
    if not shutil.which('ip'):raise ValueError('iproute2 未就绪，无法安全核验路由')


def inspect_daemon_policy():
    path=DAEMON_CONFIG
    try:fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
    except FileNotFoundError:fd=None
    except OSError:raise ValueError('Docker daemon 配置存在不安全链接') from None
    if fd is not None:
        try:
            info=os.fstat(fd)
            if not stat.S_ISREG(info.st_mode) or info.st_uid!=0 or info.st_mode&0o022 or info.st_size>65536:raise ValueError('Docker daemon 配置权限或大小不安全')
            with os.fdopen(fd,'r',closefd=False) as source:value=json.load(source)
            if not isinstance(value,dict) or value.get('iptables') is False:raise ValueError('Docker 禁用了默认 IPv4 防火墙管理，请使用原生安装；现有设置保持不变')
        finally:os.close(fd)
    try:
        pid=run('systemctl','show','docker','--property=MainPID','--value').strip()
        if re.fullmatch('[1-9][0-9]{0,9}',pid):
            data=Path('/proc',pid,'cmdline').read_bytes()
            if len(data)>16384:raise ValueError('Docker 启动参数过大')
            args=data.split(b'\0')
            if b'--iptables=false' in args or any(args[i:i+2]==[b'--iptables',b'false'] for i in range(len(args))):raise ValueError('Docker 启动时关闭了 IPv4 防火墙管理，请使用原生安装')
    except OSError:pass


def prepare_engine(journal):
    existing = shutil.which('docker') is not None
    if existing:
        inspect_daemon_policy()
        run('docker', 'info', '--format', '{{json .}}')
        try: run('docker', 'compose', 'version', '--short'); return
        except ValueError: packages = ['docker-compose-plugin']
    else:
        prepare_inspection_tools(journal)
        # Installing Docker can establish forwarding/NAT. Refuse machines
        # already acting as routers or using custom policy routing.
        if Path('/proc/sys/net/ipv4/ip_forward').read_text().strip() != '0':
            raise ValueError('服务器已开启 IP 转发且尚无 Docker，请使用原生安装以保留路由业务')
        ipv6_forward=Path('/proc/sys/net/ipv6/conf/all/forwarding')
        if ipv6_forward.exists() and ipv6_forward.read_text().strip()!='0':
            raise ValueError('服务器已开启 IPv6 转发，请使用原生安装以保留路由业务')
        rules = json.loads(run('ip', '-json', 'rule', 'show'))
        if any(r.get('priority') not in (0, 32766, 32767) for r in rules):
            raise ValueError('检测到自定义策略路由，请使用原生安装')
        for package in ('docker.io', 'docker-doc', 'docker-compose', 'docker-compose-v2', 'podman-docker', 'containerd', 'runc', 'docker-ce'):
            check = subprocess.run(['dpkg-query', '-W', '-f=${Status}', package], text=True, capture_output=True)
            if check.returncode == 0 and check.stdout.strip() == 'install ok installed':
                raise ValueError('已有容器运行时或部分 Docker 安装，不会替换；请使用原生安装或自行准备官方 Docker')
        packages = ['docker-ce', 'docker-ce-cli', 'containerd.io', 'docker-buildx-plugin', 'docker-compose-plugin']
    release = __import__('platform').freedesktop_os_release()
    dist, codename = release.get('ID'), release.get('VERSION_CODENAME')
    expected = {('debian', 'bookworm'), ('debian', 'trixie'), ('ubuntu', 'jammy'), ('ubuntu', 'noble')}
    if (dist, codename) not in expected: raise ValueError('没有受支持的 Docker 官方软件源')
    key = Path('/etc/apt/keyrings/guangyue-docker.asc'); source = Path('/etc/apt/sources.list.d/guangyue-docker.sources')
    for p in (key, source): own_path(p, journal)
    key.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
    if not key.exists(): official_docker_key(key, dist)
    if oneclick.sha256(key) != GPG_HASH: raise ValueError('Docker 公钥已被外部修改')
    text = f'Types: deb\nURIs: https://download.docker.com/linux/{dist}\nSuites: {codename}\nComponents: stable\nArchitectures: amd64\nSigned-By: {key}\n'
    if source.exists() and source.read_text() != text: raise ValueError('Docker 专属软件源已被外部修改')
    if not source.exists(): source.write_text(text); source.chmod(0o644)
    oneclick.apt_locks_available(); run('apt-get', 'update', '-qq', timeout=240)
    simulation = run('apt-get', '-s', '--no-install-recommends', '--no-upgrade', 'install', *packages)
    if any(row.startswith('Remv ') or row.startswith('Inst ') and '[' in row.split('(', 1)[0] for row in simulation.splitlines()):
        raise ValueError('安装 Docker 需要升级或移除既有系统包，已停止')
    run('env', 'DEBIAN_FRONTEND=noninteractive', 'apt-get', '-y', '--no-install-recommends', '--no-upgrade', '-o', 'DPkg::Lock::Timeout=30', 'install', *packages, timeout=900)
    if not existing:
        journal['engine_created'] = True; save_journal(journal)
        run('systemctl', 'enable', '--now', 'docker')
    run('docker', 'info', '--format', '{{json .}}'); run('docker', 'compose', 'version', '--short')


def owned_json(path, value, journal, mode=0o600, uid=0, gid=0):
    """Persist write intent before mutation, retaining one prior known hash."""
    path = Path(path)
    hashes = journal.setdefault('file_hashes', {})
    known = hashes.get(str(path), [])
    if path.exists() or path.is_symlink():
        if path.is_symlink() or not path.is_file() or oneclick.sha256(path) not in known:
            raise ValueError('受管文件已被外部修改，拒绝覆盖：' + path.name)
    body = (json.dumps(value, ensure_ascii=False, indent=2) + '\n').encode()
    digest = hashlib.sha256(body).hexdigest()
    hashes[str(path)] = list(dict.fromkeys(known + [digest]))[-2:]; save_journal(journal)
    atomic_json(path, value, mode, uid, gid)


def validated_manifest(value, target):
    if not isinstance(value, dict) or set(value) != {'schema', 'version', 'platform', 'archive', 'sha256', 'images'}:
        raise ValueError('Docker 镜像清单格式无效')
    if value['schema'] != 1 or value['version'] != target or value['platform'] != 'linux/amd64':
        raise ValueError('Docker 镜像版本或平台不匹配')
    if value['archive'] != f'guangyue-panel-docker-{target}-linux-amd64.tar.gz' or not isinstance(value['sha256'],str) or not re.fullmatch('[a-f0-9]{64}', value['sha256']):
        raise ValueError('Docker 归档名称或校验值无效')
    images = value['images']
    if not isinstance(images, dict) or set(images) != {'app', 'postgres', 'redis'}:
        raise ValueError('Docker 镜像清单不完整')
    for name, image in images.items():
        if not isinstance(image, dict) or set(image) - {'id', 'source'} or not isinstance(image.get('id'),str) or not ID_RE.fullmatch(image.get('id', '')):
            raise ValueError('Docker 镜像 ID 无效')
        if name in ('postgres', 'redis'):
            source = image.get('source', '')
            if not isinstance(source,str) or not re.fullmatch(r'(?:docker.io/library/)?' + name + r'(?::[a-zA-Z0-9_.-]+)?@sha256:[a-f0-9]{64}', source):
                raise ValueError('数据库镜像来源必须固定官方 digest')
    return value


def image_check(manifest):
    for name, image in manifest['images'].items():
        data = json.loads(run('docker', 'image', 'inspect', image['id']))
        if not isinstance(data, list) or len(data) != 1 or data[0].get('Id') != image['id'] or data[0].get('Os') != 'linux' or data[0].get('Architecture') != 'amd64':
            raise ValueError('Docker 已加载镜像身份或平台校验失败')
        if name == 'app' and (data[0].get('Config', {}).get('Labels') or {}).get('org.opencontainers.image.version') != manifest['version']:
            raise ValueError('应用镜像版本标签与正式 Release 不符')


def fetch_images(target, directory, test_assets_dir=None):
    directory = Path(directory); directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    manifest_file = directory / 'DOCKER_IMAGES.json'
    if test_assets_dir is not None:
        if os.environ.get('GITHUB_ACTIONS') != 'true' or os.environ.get('GY_DOCKER_INTEGRATION') != '1':
            raise ValueError('本地测试镜像仅可用于受控 CI')
        source = Path(test_assets_dir) / 'DOCKER_IMAGES.json'
        if source.is_symlink() or not source.is_file() or source.stat().st_size > 65536: raise ValueError('CI 镜像清单无效')
        shutil.copyfile(source, manifest_file); manifest_file.chmod(0o600)
    else:
        oneclick.fetch_release_asset(target, 'DOCKER_IMAGES.json', manifest_file, max_bytes=65536); manifest_file.chmod(0o600)
    manifest = validated_manifest(safe_json(manifest_file, 65536), target)
    archive = directory / manifest['archive']
    if test_assets_dir is not None:
        source = Path(test_assets_dir) / manifest['archive']
        if source.is_symlink() or not source.is_file() or source.stat().st_size > MAX_ARCHIVE: raise ValueError('CI 镜像归档无效')
        shutil.copyfile(source, archive)
    else:
        oneclick.fetch_release_asset(target, archive.name, archive, max_bytes=MAX_ARCHIVE)
    if oneclick.sha256(archive) != manifest['sha256']: raise ValueError('Docker 镜像归档 SHA256 不匹配')
    # Never extract Docker archives into the host filesystem. Bound expansion
    # before docker load, whose compressed input handling is otherwise opaque.
    count, started = 0, time.monotonic()
    with gzip.open(archive, 'rb') as stream:
        while block := stream.read(1 << 20):
            count += len(block)
            if count > MAX_EXPANDED or time.monotonic() - started > 180: raise ValueError('Docker 镜像归档解压资源超过限制')
    try: image_check(manifest)
    except ValueError:
        run('docker', 'load', '--input', str(archive), timeout=300)
        image_check(manifest)
    return manifest


def volumes(source, target, readonly=False):
    return {'type': 'bind', 'source': str(source), 'target': target, 'read_only': readonly,
            'bind': {'create_host_path': False}}


# Container healthchecks only report state (docker ps / inspect); restarts and
# upgrade health gates are unchanged. The image ships Debian bash, so the panel
# probe uses bash's /dev/tcp builtin with no curl/wget and no `$` that Compose
# would interpolate. The gateway reuses its read-only -wait-ready probe.
PANEL_HEALTHCHECK = {'test': ['CMD', '/bin/bash', '-c',
                              "exec 3<>/dev/tcp/127.0.0.1/19100 && printf 'GET /api/health HTTP/1.0\\r\\nHost: 127.0.0.1\\r\\n\\r\\n' >&3 && head -n 1 <&3 | grep -q '^HTTP/1\\.[01] 200 '"],
                     'interval': '30s', 'timeout': '5s', 'retries': 3, 'start_period': '60s'}
GATEWAY_HEALTHCHECK = {'test': ['CMD', '/opt/guangyue-personal/bin/guangyue-gateway', '-config', '/etc/guangyue-gateway.json', '-wait-ready', '5s'],
                       'interval': '30s', 'timeout': '8s', 'retries': 3, 'start_period': '60s'}


def with_healthchecks(value):
    """Add app healthchecks to a composition, including pre-0.42 installations."""
    services = value['services']
    services['gateway'].setdefault('healthcheck', dict(GATEWAY_HEALTHCHECK))
    services['panel'].setdefault('healthcheck', dict(PANEL_HEALTHCHECK))
    return value


def composition(marker, config, fixture=False):
    uid, gid = marker['uid'], marker['gid']; labels = {'org.guangyue.install-id': marker['id']}
    common = {'image': marker['images']['app'], 'user': f'{uid}:{gid}', 'init': True, 'restart': 'unless-stopped',
              'labels': labels, 'cap_drop': ['ALL'], 'security_opt': ['no-new-privileges:true'], 'read_only': True,
              'pids_limit': 256, 'stop_grace_period': '35s', 'logging': {'driver': 'local', 'options': {'max-size': '5m', 'max-file': '2'}},
              'tmpfs': [f'/tmp:rw,noexec,nosuid,size=32m,mode=0700,uid={uid},gid={gid}',
                        f'/var/lib/postgresql/data:rw,noexec,nosuid,size=1m,mode=0700,uid={uid},gid={gid}']}
    gateway = dict(common, entrypoint=['/opt/guangyue-personal/bin/guangyue-gateway'], command=['-config', '/etc/guangyue-gateway.json'],
                   ports=[{'target': 443, 'published': '443', 'host_ip':'0.0.0.0','protocol': 'tcp'}, {'target': 443, 'published': '443', 'host_ip':'0.0.0.0','protocol': 'udp'}],
                   sysctls={'net.ipv4.ip_unprivileged_port_start': '0'}, mem_limit='192m',
                   volumes=[volumes(ROOT / 'gateway.json', '/etc/guangyue-gateway.json', True),
                            volumes(STATE / 'gateway', '/var/lib/guangyue/gateway'), volumes(STATE / 'tls', '/var/lib/guangyue/tls'),
                            volumes(ROOT / 'reality', '/run/guangyue-reality'),volumes(MAINTENANCE,'/var/lib/guangyue-maintenance',True)])
    if fixture: gateway['volumes'].append(volumes(ROOT / 'fixture', '/etc/guangyue-fixture', True))
    panel = dict(common, entrypoint=['/usr/local/bin/guangyue-entrypoint'], command=[], network_mode='service:gateway',
                 environment={'GUANGYUE_CONTAINER': '1', 'GOMAXPROCS': '2'}, mem_limit='512m' if marker['edition'] == 'lite' else '768m',
                 volumes=[volumes(CONFIG, '/etc/guangyue-personal.json', True), volumes(STATE, '/var/lib/guangyue'),
                          volumes(ROOT / 'reality', '/run/guangyue-reality'), volumes(MAINTENANCE, '/var/lib/guangyue-maintenance', True)])
    panel['tmpfs'] = common['tmpfs'] + [f'/run/guangyue-runtime:rw,noexec,nosuid,size=2m,mode=0700,uid={uid},gid={gid}']
    services = {'gateway': gateway, 'panel': panel}
    if marker['edition'] == 'pro':
        services['postgres'] = {'image': marker['images']['postgres'], 'user': '999:999', 'restart': 'unless-stopped',
                                'labels': labels, 'cap_drop': ['ALL'], 'security_opt': ['no-new-privileges:true'],
                                'pids_limit': 128, 'mem_limit': '768m', 'stop_grace_period': '60s',
                                'environment': {'PGDATA': '/var/lib/postgresql/data/pgdata', 'POSTGRES_PASSWORD_FILE': '/run/secrets/postgres-password',
                                                'POSTGRES_INITDB_ARGS': '--auth-local=peer --auth-host=scram-sha-256'},
                                'command': ['postgres', '-c', 'shared_buffers=128MB', '-c', 'max_connections=40', '-c', 'log_statement=none'],
                                'volumes': [volumes(ROOT / 'postgres', '/var/lib/postgresql/data'), volumes(ROOT / 'postgres-password', '/run/secrets/postgres-password', True)],
                                'healthcheck': {'test': ['CMD', 'pg_isready', '-U', 'postgres', '-d', 'postgres'], 'interval': '5s', 'timeout': '3s', 'retries': 30}}
        services['redis'] = {'image': marker['images']['redis'], 'user': '999:999', 'restart': 'unless-stopped', 'labels': labels,
                            'read_only': True, 'cap_drop': ['ALL'], 'security_opt': ['no-new-privileges:true'],
                            'pids_limit': 32, 'mem_limit': '128m', 'command': ['redis-server', '/usr/local/etc/redis/redis.conf'],
                            'tmpfs': ['/data:rw,noexec,nosuid,size=2m,mode=0700,uid=999,gid=999'],
                            'volumes': [volumes(ROOT / 'redis.conf', '/usr/local/etc/redis/redis.conf', True)]}
    return with_healthchecks({'name': marker['project'], 'services': services, 'networks': {'default': {'labels': labels}}})


def read_config():
    fd = os.open(CONFIG, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or stat.S_IMODE(info.st_mode) not in (0o600, 0o640) or info.st_size > 65536:
            raise ValueError('应用配置权限无效')
        with os.fdopen(fd, 'r', closefd=False) as source: config = json.load(source)
        if config.get('state_dir') != str(STATE): raise ValueError('Docker 应用状态路径必须保持固定')
        return config
    finally: os.close(fd)


def copy_application(bundle):
    bundle = Path(bundle)
    for name in ('bin', 'web', 'licenses', 'deploy'):
        target = APP / name
        if target.exists(): shutil.rmtree(target)
        shutil.copytree(bundle / name, target)
    for src, dest in [('guangyue-linux-amd64', 'guangyue'), ('guangyue-gateway-linux-amd64', 'guangyue-gateway'),
                      ('hysteria-node-linux-amd64', 'hysteria'), ('xray-linux-amd64', 'xray'), ('mihomo-linux-amd64', 'mihomo')]:
        shutil.copyfile(APP / 'bin' / src, APP / 'bin' / dest); (APP / 'bin' / dest).chmod(0o755)
    shutil.copyfile(bundle / 'VERSION', APP / 'VERSION')
    for path in APP.rglob('*'):
        if path.is_dir(): path.chmod(0o755)
        elif 'bin' not in path.relative_to(APP).parts: path.chmod(0o644)


def private_user_directory(path, uid, gid):
    path = Path(path)
    if path.is_symlink(): raise ValueError('Docker 私有目录存在链接')
    path.mkdir(mode=0o700, parents=True, exist_ok=True); path.chmod(0o700); os.chown(path, uid, gid)


def private_secret(path, value, uid=999, gid=999):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o400)
    with os.fdopen(fd, 'w') as output:
        os.fchown(output.fileno(), uid, gid); output.write(value + '\n'); output.flush(); os.fsync(output.fileno())


def pg_container(marker):
    ids = compose_for(marker, 'ps', '-q', 'postgres').split()
    if len(ids) != 1 or not re.fullmatch('[a-f0-9]{12,64}', ids[0]): raise ValueError('专用 PostgreSQL 容器未运行')
    item = json.loads(run('docker', 'inspect', ids[0]))[0]
    if not owned_container(item, marker) or (item.get('Config', {}).get('Labels') or {}).get('com.docker.compose.service') != 'postgres' or item.get('Image') != marker['images']['postgres']:
        raise ValueError('PostgreSQL 容器不属于当前安装')
    return ids[0]


def pg_sql(marker, sql, database='guangyue'):
    return run('docker', 'exec', '-i', '--user', 'postgres', pg_container(marker), 'psql', '-X', '--no-password', '-v', 'ON_ERROR_STOP=1', '-A', '-t', '-d', database, '--file=-', input=sql)


def provision_database(marker, journal):
    compose_for(marker, 'up', '-d', '--no-build', '--pull', 'never', 'postgres', 'redis')
    register_containers(marker, journal)
    deadline = time.monotonic() + 120
    while True:
        try: pg_sql(marker, 'SELECT 1;', 'postgres'); break
        except ValueError:
            if time.monotonic() >= deadline: raise ValueError('专用数据库未就绪，保留现场供续装') from None
            time.sleep(1)
    credentials = journal['credentials']
    if not pg_sql(marker, "SELECT 1 FROM pg_roles WHERE rolname='guangyue';", 'postgres').strip():
        pg_sql(marker, "CREATE ROLE guangyue LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD '" + credentials['app_pg'] + "';", 'postgres')
    if pg_sql(marker, "SELECT rolsuper FROM pg_roles WHERE rolname='guangyue';", 'postgres').strip() != 'f': raise ValueError('应用数据库角色权限异常')
    if not pg_sql(marker, "SELECT 1 FROM pg_database WHERE datname='guangyue';", 'postgres').strip(): pg_sql(marker, 'CREATE DATABASE guangyue OWNER guangyue;', 'postgres')
    return {'database': {'driver': 'postgres', 'dsn': f"postgres://guangyue:{credentials['app_pg']}@postgres:5432/guangyue?sslmode=disable", 'max_connections': 8},
            'redis_url': f"redis://:{credentials['redis']}@redis:6379/0"}


def health(marker, expected, timeout=90):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            pem = (STATE / 'tls/current/fullchain.pem').read_text()
            match = re.search(r'-----BEGIN CERTIFICATE-----.*?-----END CERTIFICATE-----', pem, re.S)
            if not match: raise ValueError('certificate unavailable')
            expected_der = ssl.PEM_cert_to_DER_cert(match.group(0))
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT); context.check_hostname = False; context.verify_mode = ssl.CERT_NONE
            # This is a local readiness pin, not a claim of public reachability.
            with socket.create_connection(('127.0.0.1', 443), timeout=4) as raw:
                with context.wrap_socket(raw, server_hostname=marker['node_domain']) as connection:
                    if connection.getpeercert(binary_form=True) != expected_der: raise ValueError('certificate mismatch')
                    request = http.client.HTTPConnection('127.0.0.1', 443, timeout=4); request.sock = connection
                    request.request('GET', '/api/health', headers={'Host': marker['node_domain'], 'Connection': 'close'})
                    response = request.getresponse(); value = json.loads(response.read(8192))
                    if response.status == 200 and value.get('version') == expected and value.get('edition') == marker['edition']:
                        items = checked_containers(marker)
                        needed = {'gateway', 'panel'} | ({'postgres', 'redis'} if marker['edition'] == 'pro' else set())
                        active = {(i.get('Config', {}).get('Labels') or {}).get('com.docker.compose.service') for i in items if i.get('State', {}).get('Running')}
                        if needed <= active: return
        except (OSError, ValueError, ssl.SSLError, http.client.HTTPException): pass
        time.sleep(1)
    raise ValueError('Docker 面板或代理核心未通过当前版本健康检查')


def setup_host_updater(plan, journal):
    import update_state
    for path in (update_state.ROOT,update_state.HELPER):own_path(path,journal)
    units=('guangyue-updater.socket','guangyue-updater.service')
    for name in units:
        path=SYSTEM_UNITS/name
        expected=journal.setdefault('unit_hashes',{}).get(name)
        if path.exists() and (path.is_symlink() or not expected or oneclick.sha256(path)!=expected):
            raise ValueError('维护服务已有外部配置，拒绝覆盖：'+name)
        own_path(path,journal)
    try: update_state.setup(plan['version'],APP/'deploy')
    finally:
        for name in units:
            path=SYSTEM_UNITS/name
            if path.is_file() and not path.is_symlink():journal['unit_hashes'][name]=oneclick.sha256(path)
        save_journal(journal)


def install(plan, bundle, *, test_assets_dir=None, cert_file=None, key_file=None):
    oneclick.validate_plan(plan); oneclick.verify_bundle(bundle, plan)
    if plan['backend'] != 'docker': raise ValueError('不是 Docker 安装计划')
    testing = test_assets_dir is not None or cert_file is not None or key_file is not None
    if testing and (os.environ.get('GITHUB_ACTIONS') != 'true' or os.environ.get('GY_DOCKER_INTEGRATION') != '1'):
        raise ValueError('测试参数仅用于受控 CI，生产安装不能绕过证书或 Release 下载')
    if bool(cert_file) != bool(key_file): raise ValueError('测试证书与密钥必须成对')
    journal = install_journal(plan)
    if journal['phase'] == 'installed':
        marker = load(); health(marker, plan['version']); return journal['result']
    marker = None
    try:
        prepare_engine(journal); resume_preflight(plan)
        for p in (ROOT, APP, STATE, CONFIG): own_path(p, journal)
        oneclick.private_directory(ROOT); APP.mkdir(mode=0o755, parents=True, exist_ok=True)
        try:
            user = pwd.getpwnam('guangyue')
            if user.pw_uid == 0 or user.pw_dir != str(STATE): raise ValueError('已有同名用户不属于本项目')
        except KeyError:
            run('useradd', '--system', '--user-group', '--home-dir', str(STATE), '--shell', '/usr/sbin/nologin', 'guangyue'); user = pwd.getpwnam('guangyue')
        for p in (STATE, STATE / 'gateway', STATE / 'tls', ROOT / 'reality'): private_user_directory(p, user.pw_uid, user.pw_gid)
        if not journal.get('application_copied'): copy_application(bundle); journal['application_copied'] = True; save_journal(journal)
        if not CONFIG.exists(): run(str(APP / 'bin/guangyue'), '-init'); journal['config_initialized'] = True; save_journal(journal)
        config = read_config()
        with tempfile.TemporaryDirectory(prefix='images-', dir=ROOT) as directory:
            manifest = fetch_images(plan['version'], directory, test_assets_dir)
        marker = {**plan, 'private_gateway': True, 'project': journal['project'], 'compose_file': str(ROOT / 'compose.json'),
                  'uid': user.pw_uid, 'gid': user.pw_gid, 'images': {k: v['id'] for k,v in manifest['images'].items()},
                  'floor': plan['version'], 'installed_at': int(time.time()), 'public_udp_verified': False}
        config.update(public_url='https://' + plan['panel_domain'], vless_host=plan['node_domain'], hy2_host=plan['node_domain'],
                      reality_sni=plan['reality_sni'], reality_target=plan['reality_sni'] + ':443', private_gateway=True, hy2_optimized=False,
                      cert='/var/lib/guangyue/tls/current/fullchain.pem', cert_key='/var/lib/guangyue/tls/current/privkey.pem',
                      mihomo='/opt/guangyue-personal/bin/mihomo', edition=plan['edition'], site_id=plan['site_id'],
                      role='controller' if plan['edition'] == 'pro' else 'standalone')
        if plan['edition'] == 'pro':
            if not journal['credentials']:
                journal['credentials'] = {key: secrets.token_urlsafe(36) for key in ('admin_pg', 'app_pg', 'redis')}; save_journal(journal)
            private_user_directory(ROOT / 'postgres', 999, 999)
            if not (ROOT / 'postgres-password').exists(): private_secret(ROOT / 'postgres-password', journal['credentials']['admin_pg'])
            if not (ROOT / 'redis.conf').exists():
                private_secret(ROOT / 'redis.conf', f"bind 0.0.0.0\nport 6379\nprotected-mode yes\nrequirepass {journal['credentials']['redis']}\nmaxmemory 64mb\nmaxmemory-policy allkeys-lru\nsave \"\"\nappendonly no\ndaemonize no\nloglevel warning\nlogfile \"\"")
        else: config.update(database={'driver': 'sqlite'}, redis_url='')
        own_path(MAINTENANCE, journal); MAINTENANCE.mkdir(mode=0o755, parents=True, exist_ok=True)
        gateway = {'panel_domain': plan['panel_domain'], 'node_domain': plan['node_domain'], 'reality_sni': plan['reality_sni'],
                   'accept_tos': True, 'acme_email': plan.get('acme_email',''), 'state_dir': '/var/lib/guangyue/gateway',
                   'tls_dir': '/var/lib/guangyue/tls', 'panel_upstream': 'http://127.0.0.1:19100',
                   'reality_upstream': '127.0.0.1:18443', 'reality_socket_dir': '/run/guangyue-reality', 'listen': ':443'}
        if cert_file:
            fixture = ROOT / 'fixture'; private_user_directory(fixture, user.pw_uid, user.pw_gid)
            for source, name in ((cert_file,'fullchain.pem'),(key_file,'privkey.pem')):
                if Path(source).is_symlink() or not Path(source).is_file(): raise ValueError('CI 证书不是普通文件')
                shutil.copyfile(source,fixture/name);os.chown(fixture/name,user.pw_uid,user.pw_gid);(fixture/name).chmod(0o600)
            gateway.update(cert_file='/etc/guangyue-fixture/fullchain.pem',key_file='/etc/guangyue-fixture/privkey.pem')
        owned_json(ROOT/'gateway.json',gateway,journal,0o640,0,user.pw_gid)
        owned_json(ROOT/'compose.json',composition(marker,config,bool(cert_file)),journal);marker['compose_sha256']=oneclick.sha256(ROOT/'compose.json')
        owned_json(ROOT/'DOCKER_IMAGES.json',manifest,journal)
        journal['compose_sha256']=marker['compose_sha256']
        journal['creation_intent']={'compose_sha256':marker['compose_sha256'],'images':marker['images']};save_journal(journal)
        if plan['edition']=='pro':config.update(provision_database(marker,journal))
        atomic_json(CONFIG,config,0o640,0,user.pw_gid)
        journal['phase']='certificate';save_journal(journal)
        compose_for(marker,'up','-d','--no-build','--pull','never','gateway');register_containers(marker,journal)
        compose_for(marker,'exec','-T','gateway','/opt/guangyue-personal/bin/guangyue-gateway','-config','/etc/guangyue-gateway.json','-wait-ready','180s',timeout=195)
        journal['phase']='starting';save_journal(journal)
        compose_for(marker,'up','-d','--no-deps','--no-build','--pull','never','panel');register_containers(marker,journal);health(marker,plan['version'])
        own_path(MARKER,journal);atomic_json(MARKER,marker)
        setup_host_updater(plan,journal)
        result={'version':plan['version'],'public_url':config['public_url'],'credentials_file':str(STATE/'initial-owner.json'),'stage':'installed'}
        journal.update(phase='installed',result=result,error='');save_journal(journal)
        return result
    except Exception as exc:
        # Stop only the already-recorded containers, never compose-down a
        # possibly foreign project or delete persistent state/seed/config.
        if marker is not None and journal.get('creation_intent'):
            try:register_containers(marker,journal)
            except ValueError:pass
        try: cleanup = container_inspect() if shutil.which('docker') and journal.get('containers') else []
        except ValueError: cleanup = []
        for item in cleanup:
            if owned_container(item,{'id':plan['id'],'project':journal['project']},journal):
                try:run('docker','stop','--time','35',item['Id'])
                except ValueError:pass
        for name,expected in journal.get('unit_hashes',{}).items():
            path=Path('/etc/systemd/system')/name
            if name in ('guangyue-updater.socket','guangyue-updater.service') and path.is_file() and not path.is_symlink() and oneclick.sha256(path)==expected:
                try:run('systemctl','stop',name)
                except ValueError:pass
        journal.update(phase='failed',error=oneclick.redact(str(exc)) if isinstance(exc,ValueError) else type(exc).__name__);save_journal(journal)
        raise ValueError(journal['error']) from None


def version(value):
    return tuple(int(p) for p in oneclick.checked_version(value).split('.'))


def postgres_run(args, config, **kwargs):
    """Execute only PostgreSQL maintenance tools in our own fixed container.

    Host archive/script paths become stdin, never shell fragments or arbitrary
    docker-cp destinations. File streaming keeps large snapshots out of RAM.
    subprocess.run-compatible output/check/text kwargs are retained.
    """
    marker = load()
    if marker['edition'] != 'pro' or config.get('database', {}).get('driver') != 'postgres' or config.get('site_id', 'default') != marker['site_id']:
        raise ValueError('数据库维护配置不属于当前 Docker 站点')
    args = list(args)
    if not args or args[0] not in {'psql', 'pg_dump', 'pg_restore'}: raise ValueError('只允许固定 PostgreSQL 维护工具')
    tool = args.pop(0); host_input = None
    if tool == 'pg_restore' and args and not args[-1].startswith('-'):
        host_input = Path(args.pop())
    if tool == 'psql':
        for index, arg in enumerate(list(args)):
            if arg == '--file' and index+1 < len(args) and args[index+1] != '-':
                host_input = Path(args[index+1]); args[index+1] = '-'; break
            if arg.startswith('--file=') and arg != '--file=-': host_input = Path(arg[7:]); args[index] = '--file=-'; break
        if '-c' in args:
            index = args.index('-c')
            if index+1 >= len(args) or host_input is not None or kwargs.get('input') is not None: raise ValueError('数据库输入参数冲突')
            sql = args[index+1]; del args[index:index+2]; args.append('--file=-')
            kwargs['input'] = sql if kwargs.get('text') or kwargs.get('encoding') else sql.encode()
        if not any(a in ('-d','--dbname') or a.startswith('--dbname=') for a in args): args += ['-d', 'guangyue']
    if tool == 'pg_dump':
        if any(a == '-f' or a == '--file' or a.startswith('--file=') and a != '--file=-' for a in args): raise ValueError('数据库备份输出只能由宿主文件描述符接收')
        if not any(a in ('-d','--dbname') or a.startswith('--dbname=') for a in args): args += ['-d','guangyue']
    command = ['docker', '--host', 'unix:///var/run/docker.sock', 'exec', '-i', '--user', 'postgres', pg_container(marker), tool, *args]
    env = kwargs.pop('env', None)
    kwargs['env'] = {k:v for k,v in (env if env is not None else os.environ).items() if not k.startswith(('PG','DOCKER_'))}
    if host_input is None:
        try: return subprocess.run(command, **kwargs)
        except subprocess.CalledProcessError as exc:
            raise subprocess.CalledProcessError(exc.returncode, ['docker','exec','postgres-maintenance'], output=exc.output, stderr=exc.stderr) from None
    if 'input' in kwargs or 'stdin' in kwargs: raise ValueError('数据库文件输入与 stdin 参数冲突')
    fd = os.open(host_input, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022: raise ValueError('数据库快照/脚本不是受保护的普通文件')
        with os.fdopen(fd, 'rb', closefd=False) as source:
            capture = kwargs.pop('capture_output', False); check = kwargs.pop('check', False); timeout = kwargs.pop('timeout', None)
            if capture:
                if 'stdout' in kwargs or 'stderr' in kwargs: raise ValueError('数据库输出参数冲突')
                kwargs.update(stdout=subprocess.PIPE,stderr=subprocess.PIPE)
            with subprocess.Popen(command, stdin=source, **kwargs) as process:
                try: stdout,stderr=process.communicate(timeout=timeout)
                except subprocess.TimeoutExpired:
                    process.kill();process.communicate();raise
                result=subprocess.CompletedProcess(['docker','exec','postgres-maintenance'],process.returncode,stdout,stderr)
                if check and process.returncode: raise subprocess.CalledProcessError(process.returncode,result.args,output=stdout,stderr=stderr)
                return result
    finally: os.close(fd)


def schema(config):
    site = config.get('site_id','default')
    if not re.fullmatch(r'[a-z][a-z0-9_]{0,39}',site): raise ValueError('站点标识无效')
    if config.get('database',{}).get('driver')=='postgres':
        sql='SELECT version,checksum FROM gy_'+site+'.schema_migrations ORDER BY version'
        result=postgres_run(['psql','-XAt','-c',sql],config,capture_output=True,check=True)
        return hashlib.sha256(result.stdout).hexdigest()
    connection=sqlite3.connect('file:'+str(STATE/'panel.db')+'?mode=ro',uri=True)
    try:return hashlib.sha256(json.dumps(list(connection.execute('SELECT version,checksum FROM schema_migrations ORDER BY version'))).encode()).hexdigest()
    finally:connection.close()


def checked_containers(marker):
    result=[];seen=set()
    for item in container_inspect():
        labels=item.get('Config',{}).get('Labels') or {}
        if labels.get('com.docker.compose.project')!=marker['project']:continue
        if not owned_container(item,marker):raise ValueError('Docker 项目已有外部容器')
        service=labels.get('com.docker.compose.service')
        expected=marker['images']['app' if service in ('gateway','panel') else service]
        if item.get('Image')!=expected or service in seen:raise ValueError('Docker 项目已有镜像变化或重复容器')
        seen.add(service);result.append(item)
    return result


def validate_runtime(marker,require_helper=False):
    """Check committed identities without requiring space for a new backup."""
    config=read_config()
    if (APP/'VERSION').read_text().strip()!=marker['version']:raise ValueError('宿主版本与 Docker 部署记录不一致')
    if config.get('edition','lite')!=marker['edition'] or config.get('site_id','default')!=marker['site_id']:raise ValueError('Docker 应用配置身份发生变化')
    manifest=validated_manifest(safe_json(ROOT/'DOCKER_IMAGES.json',65536),marker['version'])
    if {k:v['id'] for k,v in manifest['images'].items()}!=marker['images']:raise ValueError('Docker 镜像清单与部署不一致')
    image_check(manifest)
    checked_containers(marker)
    if require_helper and (not (HELPER/'VERSION').is_file() or version((HELPER/'VERSION').read_text().strip())<version(marker['version'])):
        raise ValueError('当前维护工具未与目标版本同步')
    return config


def preflight(bundle=None):
    if os.geteuid()!=0:raise ValueError('Docker 维护需要 root')
    marker=load();validate_runtime(marker)
    if bundle:
        plan={**marker,'version':(Path(bundle)/'VERSION').read_text().strip()}
        oneclick.verify_bundle(bundle,plan)
    needed=3*sum(p.stat().st_size for parent in (STATE,APP) for p in parent.rglob('*') if p.is_file() and not p.is_symlink())+(512<<20)
    if shutil.disk_usage(BACKUPS.parent if BACKUPS.exists() else '/root').free<needed:raise ValueError('Docker 更新备份空间不足')


def backup_hashes(directory):
    values={}
    for path in sorted(Path(directory).rglob('*')):
        if path.is_symlink():raise ValueError('程序备份包含链接')
        if path.is_file():values[path.relative_to(directory).as_posix()]=oneclick.sha256(path)
    return values


def transaction_file():return ROOT/'transaction.json'


def backup_current(marker,config):
    oneclick.private_directory(BACKUPS)
    backup=Path(tempfile.mkdtemp(prefix='docker-upgrade-',dir=BACKUPS));backup.chmod(0o700)
    shutil.copytree(STATE,backup/'state',symlinks=True);shutil.copytree(APP,backup/'app',symlinks=False)
    shutil.copyfile(CONFIG,backup/'config.json')
    for source,name in ((MARKER,'deployment.json'),(ROOT/'compose.json','compose.json'),(ROOT/'DOCKER_IMAGES.json','DOCKER_IMAGES.json')):
        shutil.copyfile(source,backup/name);(backup/name).chmod(0o600)
    atomic_json(backup/'app-checksums.json',backup_hashes(backup/'app'))
    if HELPER.exists():
        helper_hashes=backup_hashes(HELPER)
        shutil.copytree(HELPER,backup/'helper')
        atomic_json(backup/'helper-checksums.json',helper_hashes)
    units=backup/'units';units.mkdir(mode=0o700)
    for name in ('guangyue-updater.socket','guangyue-updater.service'):
        source=SYSTEM_UNITS/name
        if source.is_file() and not source.is_symlink():shutil.copyfile(source,units/name)
    atomic_json(backup/'unit-checksums.json',backup_hashes(units))
    if config.get('database',{}).get('driver')=='postgres':
        with (backup/'postgres.dump').open('xb') as output:
            postgres_run(['pg_dump','--format=custom','--schema=gy_'+marker['site_id']],config,stdout=output,stderr=subprocess.PIPE,check=True)
    return backup


def verify_backup(path):
    path=Path(path)
    if path!=Path(os.path.normpath(path)) or not path.resolve().is_relative_to(BACKUPS.resolve()) or path.is_symlink() or not path.is_dir() or path.stat().st_uid!=0 or path.stat().st_mode&0o077:
        raise ValueError('Docker 回退目录不安全')
    marker=validate_marker(safe_json(path/'deployment.json'))
    if backup_hashes(path/'app')!=safe_json(path/'app-checksums.json'):raise ValueError('Docker 回退程序校验失败')
    if (path/'helper-checksums.json').exists() and backup_hashes(path/'helper')!=safe_json(path/'helper-checksums.json'):raise ValueError('Docker 维护工具备份校验失败')
    if (path/'unit-checksums.json').exists() and backup_hashes(path/'units')!=safe_json(path/'unit-checksums.json'):raise ValueError('Docker 维护服务备份校验失败')
    if (path/'app/VERSION').read_text().strip()!=marker['version'] or oneclick.sha256(path/'compose.json')!=marker['compose_sha256']:raise ValueError('Docker 回退版本或 Compose 校验失败')
    manifest=validated_manifest(safe_json(path/'DOCKER_IMAGES.json',65536),marker['version']);image_check(manifest)
    return marker


def history_path():return ROOT/'history.json'


def candidates():
    marker=load();config=read_config();signature=schema(config)
    rows=safe_json(history_path(),missing=True) or []
    result={}
    for row in reversed(rows):
        if not isinstance(row,dict):continue
        target=row.get('version')
        try:
            if target in result or not version(marker['floor'])<=version(target)<version(marker['version']):continue
            if any(row.get(k)!=marker[k] for k in ('id','edition','site_id')):continue
            backup=Path(row['backup']);saved=verify_backup(backup)
            if saved['id']!=marker['id']:continue
        except (ValueError,OSError,KeyError):continue
        result[target]={**row,'compatible':row.get('schema')==signature}
    return sorted(result.values(),key=lambda row:version(row['version']),reverse=True)[:30]


def remember(backup,marker,signature):
    rows=safe_json(history_path(),missing=True) or []
    row={'version':marker['version'],'backup':str(backup),'created':int(time.time()),'schema':signature,
         'id':marker['id'],'edition':marker['edition'],'site_id':marker['site_id']}
    atomic_json(history_path(),([x for x in rows if x.get('backup')!=str(backup)]+[row])[-100:])


def activate(marker,composition_value,provision=False):
    # A panel sharing gateway's network namespace must be recreated whenever
    # the gateway is recreated; never leave it attached to a removed netns.
    compose_for(marker,'stop','panel')
    planned_digest=hashlib.sha256((json.dumps(composition_value,ensure_ascii=False,indent=2)+'\n').encode()).hexdigest()
    marker['compose_sha256']=planned_digest
    pending=safe_json(transaction_file(),missing=True)
    if pending:
        # Root files cannot be switched together atomically. Record both exact
        # known sides before the first switch so crash recovery can distinguish
        # an interrupted write from an administrator's unrelated edit.
        pending['switch']={'marker':marker,'compose_sha256':planned_digest}
        atomic_json(transaction_file(),pending)
    atomic_json(ROOT/'compose.json',composition_value)
    if oneclick.sha256(ROOT/'compose.json')!=planned_digest:raise ValueError('Compose 原子切换校验失败')
    atomic_json(MARKER,marker)
    compose_for(marker,'up','-d','--no-build','--pull','never','--force-recreate','gateway')
    compose_for(marker,'exec','-T','gateway','/opt/guangyue-personal/bin/guangyue-gateway','-config','/etc/guangyue-gateway.json','-wait-ready','180s',timeout=195)
    if provision:
        import update_state
        update_state.setup(marker['version'],APP/'deploy')
    compose_for(marker,'up','-d','--no-deps','--no-build','--pull','never','--force-recreate','panel')
    health(marker,marker['version'])


def stop_application(marker):
    # Freeze all writers to STATE, including automatic certificate renewals.
    # The panel's graceful stop drains its database/core workers first.
    compose_for(marker,'stop','panel')
    compose_for(marker,'stop','gateway')


def start_application(marker):
    compose_for(marker,'up','-d','--no-deps','--no-build','--pull','never','gateway')
    compose_for(marker,'exec','-T','gateway','/opt/guangyue-personal/bin/guangyue-gateway','-config','/etc/guangyue-gateway.json','-wait-ready','180s',timeout=195)
    compose_for(marker,'up','-d','--no-deps','--no-build','--pull','never','panel')


def recovery_marker(backup):
    old=verify_backup(backup)
    current=validate_marker(safe_json(MARKER))
    pending=safe_json(transaction_file())
    if not isinstance(pending,dict) or pending.get('id')!=old['id'] or pending.get('backup')!=str(backup):
        raise ValueError('恢复切换没有匹配的私有事务')
    planned=(pending.get('switch') or {}).get('marker')
    if current!=old and current!=planned:raise ValueError('部署标记已被事务之外修改，拒绝恢复覆盖')
    hashes={old['compose_sha256']}
    if planned:
        validate_marker(planned)
        if planned['id']!=old['id']:raise ValueError('恢复切换计划属于其他安装')
        hashes.add(planned['compose_sha256'])
    safe_json(ROOT/'compose.json')
    if oneclick.sha256(ROOT/'compose.json') not in hashes:raise ValueError('Compose 已被事务之外修改，拒绝恢复覆盖')
    return old,current


def restore_helper(backup):
    source=Path(backup)/'helper'
    if source.exists():
        if backup_hashes(source)!=safe_json(Path(backup)/'helper-checksums.json'):raise ValueError('恢复工具快照校验失败')
        temporary=Path(tempfile.mkdtemp(prefix='.guangyue-helper-restore-',dir=HELPER.parent))
        try:
            shutil.copytree(source,temporary/'helper')
            failed=HELPER.with_name('.guangyue-helper-failed')
            if failed.exists():
                if failed.is_symlink() or not failed.is_dir() or failed.stat().st_uid!=0:raise ValueError('维护工具失败现场类型无效')
                shutil.rmtree(failed)
            if HELPER.exists():os.rename(HELPER,failed)
            os.rename(temporary/'helper',HELPER)
            if failed.exists():shutil.rmtree(failed)
        finally:shutil.rmtree(temporary)
    units=Path(backup)/'units'
    if units.exists():
        if backup_hashes(units)!=safe_json(Path(backup)/'unit-checksums.json'):raise ValueError('维护服务恢复快照校验失败')
        for name in ('guangyue-updater.socket','guangyue-updater.service'):
            if (units/name).is_file():shutil.copyfile(units/name,SYSTEM_UNITS/name)
        run('systemctl','daemon-reload')


def restore_snapshot(backup):
    old,current=recovery_marker(backup);stop_application(current)
    # Restore the known metadata first, so database maintenance can address the
    # unchanged, owned PostgreSQL container even after a half-finished switch.
    atomic_json(ROOT/'compose.json',safe_json(Path(backup)/'compose.json'))
    atomic_json(MARKER,old)
    # PostgreSQL recovery uses the existing privileged schema-scoped routine,
    # routed to this project's local-socket container by postgres_run.
    old_config=json.loads((Path(backup)/'config.json').read_text())
    if (Path(backup)/'postgres.dump').is_file():
        import infrastructure
        infrastructure.restore_postgres(old_config,Path(backup)/'postgres.dump')
    failed=Path(backup)/'failed-state'
    if STATE.exists() and not failed.exists():
        temporary=Path(backup)/'.failed-state-pending'
        if temporary.exists():shutil.rmtree(temporary)
        shutil.copytree(STATE,temporary,symlinks=True)
        os.rename(temporary,failed)
    if STATE.exists():shutil.rmtree(STATE)
    shutil.copytree(Path(backup)/'state',STATE,symlinks=True)
    for path in (STATE,*STATE.rglob('*')):
        if not path.is_symlink():os.chown(path,old['uid'],old['gid'])
    if APP.exists():shutil.rmtree(APP)
    shutil.copytree(Path(backup)/'app',APP)
    atomic_json(CONFIG,old_config,0o640,0,old['gid'])
    atomic_json(ROOT/'DOCKER_IMAGES.json',safe_json(Path(backup)/'DOCKER_IMAGES.json',65536))
    restore_helper(backup)
    activate(old,safe_json(Path(backup)/'compose.json'))
    return old


def complete_gate(target):
    """A healthy recovery is a commit too: it may accept new orders next."""
    import maintenance_gate
    pending=safe_json(transaction_file())
    pending['stage']='committed';pending['target']=oneclick.checked_version(target)
    atomic_json(transaction_file(),pending)
    maintenance_gate.finish();atomic_json(transaction_file(),None)


def transaction(change,target,progress):
    import maintenance_gate
    preflight();marker=load();config=read_config()
    if version(target)<version(marker['floor']):raise ValueError('不能回退到 Docker 安装基线之前')
    pending=safe_json(transaction_file(),missing=True)
    if pending:raise ValueError('已有 Docker 更新或恢复事务，请先恢复')
    if maintenance_gate.marker() is not None:raise ValueError('已有维护冻结标记，请先恢复；不会覆盖前次事务')
    signature=schema(config);backup=None;gate_started=False
    atomic_json(transaction_file(),{'schema':1,'id':marker['id'],'stage':'stopping','target':target,'previous':marker['version']})
    try:
        maintenance_gate.begin()
        gate_started=True
        stop_application(marker);progress('backing_up');backup=backup_current(marker,config)
        atomic_json(transaction_file(),{'schema':1,'id':marker['id'],'stage':'changing','backup':str(backup),'target':target,'previous':marker['version']})
        progress('installing');change(marker,config)
        current=load()
        progress('restarting');health(current,target)
        # Recheck both schema availability and actual helper before publishing
        # the irreversible commit. Gate release after commit cannot lead to
        # recovery of an old snapshot over newly accepted orders/nonces.
        schema(validate_runtime(current,require_helper=True))
        remember(backup,marker,signature)
        complete_gate(target)
    except Exception:
        pending=safe_json(transaction_file())
        if isinstance(pending,dict) and pending.get('stage')=='committed':
            # A committed version may already have accepted new traffic. Its
            # recovery path only completes gate release; never restore data.
            raise ValueError('版本已提交，维护状态需由恢复流程继续完成；不会回退新订单') from None
        if not gate_started:
            # Creation may fail or find a previous freeze. Do not release a
            # marker that this operation did not successfully acquire.
            raise
        progress('recovering')
        if backup:
            restored=restore_snapshot(backup);complete_gate(restored['version'])
        else:
            start_application(marker);health(marker,marker['version']);complete_gate(marker['version'])
        raise
    prune_backups(keep_backup=backup)
    return backup


def prune_backups(keep_backup=None,override=None,dry_run=False):
    """Apply backup retention after a committed Docker change; never fails it."""
    import backup_retention
    try:
        keep=backup_retention.keep_count(read_config(),override);marker=load()
        pending=safe_json(transaction_file(),missing=True)
        rows=safe_json(history_path(),missing=True) or []
        protected=[keep_backup,pending.get('backup') if isinstance(pending,dict) else None]
        if dry_run:return backup_retention.plan(BACKUPS,keep,marker['version'],protected,rows)[1]
        removed=backup_retention.prune(BACKUPS,keep,marker['version'],protected,rows)
        if removed:atomic_json(history_path(),backup_retention.without_removed(rows,removed))
        return removed
    except (ValueError,OSError) as exc:
        if dry_run:raise
        print('Backup pruning skipped: '+(str(exc) if isinstance(exc,ValueError) else type(exc).__name__))
        return []


def upgrade(bundle,progress=lambda stage:None,*,test_assets_dir=None):
    bundle=Path(bundle);target=(bundle/'VERSION').read_text().strip();preflight(bundle)
    current=load()
    if version(target)<=version(current['version']):raise ValueError('请选择高于当前版本的 Docker 更新')
    with tempfile.TemporaryDirectory(prefix='docker-images-',dir=ROOT) as directory:
        manifest=fetch_images(target,directory,test_assets_dir)
    # Dependencies keep their immutable IDs and persistent volumes. A normal
    # panel update cannot silently replace PostgreSQL/Redis versions.
    if any(manifest['images'][k]['id']!=current['images'][k] for k in ('postgres','redis')):
        raise ValueError('此更新要求更换数据库镜像，需单独迁移，普通更新已阻止')
    def change(marker,config):
        copy_application(bundle)
        new={**marker,'version':target,'images':{k:v['id'] for k,v in manifest['images'].items()}}
        comp=with_healthchecks(safe_json(ROOT/'compose.json'));comp['services']['gateway']['image']=new['images']['app'];comp['services']['panel']['image']=new['images']['app']
        atomic_json(ROOT/'DOCKER_IMAGES.json',manifest);activate(new,comp,provision=True)
    return transaction(change,target,progress)


def rollback(target,progress=lambda stage:None):
    oneclick.checked_version(target);marker=load()
    if version(target)<version(marker['floor']):raise ValueError('不能回退到 Docker 安装基线之前')
    selected=next((row for row in candidates() if row['version']==target),None)
    if selected is None or not selected['compatible']:raise ValueError('目标版本没有与当前数据库兼容的 Docker 回退记录')
    saved=Path(selected['backup']);old=verify_backup(saved)
    def change(current,config):
        if APP.exists():shutil.rmtree(APP)
        shutil.copytree(saved/'app',APP)
        # Keep today's state, wallet/address histories, nonce reservations,
        # credentials, certificate generation and database. Only code changes.
        new={**current,'version':target,'images':old['images']}
        comp=safe_json(ROOT/'compose.json');comp['services']['gateway']['image']=old['images']['app'];comp['services']['panel']['image']=old['images']['app']
        atomic_json(ROOT/'DOCKER_IMAGES.json',safe_json(saved/'DOCKER_IMAGES.json',65536));activate(new,comp,provision=True)
    return transaction(change,target,progress)


def recover():
    import maintenance_gate
    pending=safe_json(transaction_file(),missing=True)
    if not pending:return
    # A known mid-activation composition/marker mismatch is recoverable; an
    # unknown edit is rejected by recovery_marker before touching any service.
    marker=validate_marker(safe_json(MARKER))
    if pending.get('schema')!=1 or pending.get('id')!=marker['id']:raise ValueError('Docker 恢复事务不属于当前站点')
    if pending.get('stage')=='committed':
        marker=load()
        if marker['version']!=pending.get('target'):raise ValueError('已提交版本的恢复目标不匹配')
        schema(validate_runtime(marker,require_helper=True))
        health(marker,pending['target'])
    elif pending.get('backup'):
        if maintenance_gate.marker() is None:maintenance_gate.begin()
        restored=restore_snapshot(Path(pending['backup']))
        complete_gate(restored['version']);return
    elif pending.get('stage')=='stopping':
        if maintenance_gate.marker() is None:maintenance_gate.begin()
        marker=load()
        start_application(marker);health(marker,pending['previous'])
        complete_gate(marker['version']);return
    else:raise ValueError('Docker 恢复事务缺少完整停写快照')
    maintenance_gate.finish()
    atomic_json(transaction_file(),None)


if __name__=='__main__':
    if sys.argv[1:] not in (['--package-policy-start'],['--package-policy-stop']):raise SystemExit('Fixed Docker package-policy hook only')
    try:package_policy_hook(sys.argv[1]=='--package-policy-start')
    except Exception:raise SystemExit('Docker package policy failed; existing policies preserved')
