#!/usr/bin/env python3
"""Fresh native installation with a private TLS-ALPN gateway; never edits Nginx."""
import hashlib
import json
import os
import pwd
import secrets
import shlex
import stat
import re
import socket
import shutil
import subprocess
import time
import sys
import urllib.request
from pathlib import Path

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
import oneclick
import infrastructure

APP = Path('/opt/guangyue-personal')
STATE = Path('/var/lib/guangyue')
CONFIG = Path('/etc/guangyue-personal.json')
GATEWAY_CONFIG = Path('/etc/guangyue-gateway.json')
DEPLOYMENT = Path('/etc/guangyue-deployment.json')
ACME = STATE / 'gateway'
UNITS = ['guangyue', 'guangyue-xray', 'guangyue-hy2', 'guangyue-gateway']
POLICY = Path('/usr/sbin/policy-rc.d')
SYSTEMD = Path('/etc/systemd/system')


def unit_path(name):
    return SYSTEMD / (name if name.endswith(('.service', '.socket', '.path', '.timer')) else name + '.service')


def run(*args, timeout=90, data=None):
    environment = dict(os.environ, LC_ALL='C')
    result = subprocess.run(args, input=data, capture_output=True, text=True, timeout=timeout, env=environment)
    if result.returncode:
        raise ValueError('原生安装步骤未完成：' + Path(args[0]).name + '；详细凭据不会写入日志')
    return result.stdout


def atomic_json(path, value, mode=0o600, group=None):
    path = Path(path)
    temporary = path.with_name(path.name + '.guangyue-new')
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, mode)
    with os.fdopen(fd, 'w') as output:
        json.dump(value, output, ensure_ascii=False, indent=2); output.write('\n'); output.flush(); os.fsync(output.fileno())
    temporary.chmod(mode)
    if group is not None:
        shutil.chown(temporary, user='root', group=group)
    os.replace(temporary, path)
    oneclick.sync_directory(path.parent)


def journal_path():
    return oneclick.ROOT / 'native.json'


def save(journal):
    oneclick.private_directory(oneclick.ROOT)
    atomic_json(journal_path(), journal)


def load(plan):
    path = journal_path()
    if path.exists():
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
        with os.fdopen(fd) as source:
            info = os.fstat(source.fileno())
            if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o077 or info.st_size > 1 << 20:
                raise ValueError('原生安装日志权限不安全')
            value = json.loads(source.read((1 << 20) + 1))
        if value.get('id') != plan['id'] or value.get('version') != plan['version']:
            raise ValueError('原生安装现场属于其他安装计划，拒绝覆盖')
        return value
    value = {'id': plan['id'], 'version': plan['version'], 'phase': 'preparing', 'paths': [], 'units': [], 'unit_hashes': {}, 'config_initialized': False}
    save(value)
    return value


def resume_preflight(plan):
    if not journal_path().exists():
        raise ValueError('没有本计划的原生安装日志，不能跳过已有资源检查')
    journal = load(plan)
    for name in journal.get('units', []):
        path = unit_path(name)
        if path.exists() and (path.is_symlink() or journal.get('unit_hashes', {}).get(name) != oneclick.sha256(path)):
            raise ValueError('受管服务已经被外部修改，不能续装覆盖：' + name)
    for path in (APP, STATE, CONFIG, GATEWAY_CONFIG, DEPLOYMENT):
        if (path.exists() or path.is_symlink()) and (path.is_symlink() or str(path) not in journal.get('paths', [])):
            raise ValueError('原生续装发现不属于本计划的资源：' + path.name)


def resume_binding_owners(plan):
    """Allow only live listeners whose PID belongs to unchanged owned units."""
    resume_preflight(plan)
    if not shutil.which('ss'):
        return set()
    journal = load(plan); listeners = {}
    expected = {
        'guangyue.service': [(socket.SOCK_STREAM, p) for p in (19100, 19101, 19186)] + [(socket.SOCK_DGRAM, 19186)],
        'guangyue-xray.service': [(socket.SOCK_STREAM, 18443), (socket.SOCK_STREAM, 19185)],
        'guangyue-hy2.service': [(socket.SOCK_DGRAM, 443), (socket.SOCK_STREAM, 19199)],
        'guangyue-gateway.service': [(socket.SOCK_STREAM, 443)],
    }
    for name in journal.get('units', []):
        unit = unit_path(name)
        if unit.name not in expected or not unit.is_file() or journal.get('unit_hashes', {}).get(name) != oneclick.sha256(unit):
            continue
        group = run('systemctl', 'show', name, '--property=ControlGroup', '--value').strip()
        if not re.fullmatch(r'/system\.slice/guangyue[a-z0-9_.@-]*\.service', group):
            continue
        for base in (Path('/sys/fs/cgroup'), Path('/sys/fs/cgroup/systemd')):
            processes = base / group.lstrip('/') / 'cgroup.procs'
            if processes.is_file():
                pids = {int(value) for value in processes.read_text().split() if value.isdigit()}
                for listener in expected[unit.name]:
                    listeners.setdefault(listener, set()).update(pids)
    return owned_listener_bindings(run('ss', '-H', '-lntup').splitlines(), listeners)


def owned_listener_bindings(lines, listeners):
    grouped = {}
    for line in lines:
        columns = line.split()
        if len(columns) < 6 or columns[0] not in ('tcp', 'udp'):
            continue
        try:
            port = int(columns[4].rsplit(':', 1)[1])
        except (ValueError, IndexError):
            continue
        kind = socket.SOCK_STREAM if columns[0] == 'tcp' else socket.SOCK_DGRAM
        grouped.setdefault((kind, port), []).append({int(value) for value in re.findall(r'pid=(\d+)', line)})
    allowed = set()
    for (kind, port), owners in grouped.items():
        pids = listeners.get((kind, port), set())
        if owners and all(values and values <= pids for values in owners):
            allowed.update((family, kind, port) for family in (socket.AF_INET, socket.AF_INET6))
    return allowed


def own_path(path, journal):
    path = Path(path)
    if path.is_symlink():
        raise ValueError('受管路径存在不可信链接：' + path.name)
    if path.exists() and str(path) not in journal['paths']:
        raise ValueError('受管路径已存在，拒绝覆盖：' + path.name)
    if str(path) not in journal['paths']:
        journal['paths'].append(str(path)); save(journal)


def own_unit(name, journal):
    path = unit_path(name)
    if name not in journal['units']:
        state = run('systemctl', 'show', name, '--property=LoadState', '--value').strip()
        if state != 'not-found' or path.exists() or path.is_symlink():
            raise ValueError('服务名已被使用，不会替换：' + name)
        journal['units'].append(name); save(journal)
    elif path.exists() and journal.get('unit_hashes', {}).get(name) != oneclick.sha256(path):
        raise ValueError('本项目服务已被外部修改，不会覆盖或停止：' + name)
    own_path(path, journal)
    return path


def policy_content(journal):
    content = '#!/bin/sh\n# Guangyue temporary package policy ' + journal['id'] + '\nexit 101\n'
    return content, hashlib.sha256(content.encode()).hexdigest()


def remove_owned_policy(journal):
    expected = journal.get('package_policy')
    if expected and POLICY.is_file() and not POLICY.is_symlink() and POLICY.stat().st_uid == os.geteuid() and oneclick.sha256(POLICY) == expected:
        POLICY.unlink()


def create_owned_policy(journal):
    """Called only after the fixed APT hook verifies the frontend lock."""
    content, digest = policy_content(journal)
    if journal.get('package_policy') != digest:
        raise ValueError('依赖策略未获得当前安装回执授权')
    if POLICY.exists() or POLICY.is_symlink():
        raise ValueError('已有 policy-rc.d；不会覆盖系统策略，请使用 Docker 或自行准备原生依赖')
    fd = os.open(POLICY, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o755)
    with os.fdopen(fd, 'w') as output:
        output.write(content); output.flush(); os.fsync(output.fileno())


def package_policy_hook(start):
    """Fixed root-only APT hook, with no caller-supplied file or shell command."""
    if os.geteuid() != 0:
        raise ValueError('依赖策略钩子仅可由 root 执行')
    path = journal_path()
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd) as source:
        info = os.fstat(source.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o077 or info.st_size > 1 << 20:
            raise ValueError('依赖策略回执权限无效')
        journal = json.loads(source.read((1 << 20) + 1))
    if start:
        import fcntl
        fd = os.open('/var/lib/dpkg/lock-frontend', os.O_RDWR | os.O_NOFOLLOW)
        try:
            try:
                fcntl.lockf(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                pass
            else:
                fcntl.lockf(fd, fcntl.LOCK_UN)
                raise ValueError('APT 未持有前端锁，拒绝创建临时系统策略')
        finally:
            os.close(fd)
        create_owned_policy(journal)
    else:
        remove_owned_policy(journal)


def prepare_packages(edition, journal):
    if journal.get('package_policy'):
        # APT can finish installing everything before an interrupted parent
        # reaches its finally block. Check that no APT process still owns the
        # lock before removing only our exact root-owned shim, even if there
        # are now no packages left to install.
        oneclick.apt_locks_available()
        remove_owned_policy(journal)
        journal.pop('package_policy', None); save(journal)
    packages = ['openssl'] + (['postgresql', 'postgresql-client', 'redis-server'] if edition == 'pro' else [])
    missing = []
    for package in packages:
        result = subprocess.run(['dpkg-query', '-W', '-f=${Status}', package], capture_output=True, text=True)
        if result.returncode or result.stdout.strip() != 'install ok installed':
            missing.append(package)
    if not missing:
        return
    oneclick.apt_locks_available()
    run('apt-get', 'update', '-qq', timeout=240)
    simulation = run('apt-get', '-s', '--no-install-recommends', '--no-upgrade', 'install', *missing, timeout=90)
    if any(line.startswith('Remv ') or line.startswith('Inst ') and '[' in line.split('(', 1)[0] for line in simulation.splitlines()):
        raise ValueError('依赖安装需要升级或删除既有系统包，已停止；请使用 Docker 或自行补齐依赖')
    print('仅安装缺失依赖：' + ', '.join(missing) + '；不全量升级系统、不启动默认数据库。', flush=True)
    remove_owned_policy(journal)
    if POLICY.exists() or POLICY.is_symlink():
        raise ValueError('已有 policy-rc.d；不会覆盖系统策略，请使用 Docker 或自行准备原生依赖')
    journal['package_policy'] = policy_content(journal)[1]; save(journal)
    hook = shlex.quote(sys.executable) + ' -I ' + shlex.quote(str(Path(__file__).resolve()))
    try:
        run('env', 'DEBIAN_FRONTEND=noninteractive', 'apt-get', '-y', '--no-install-recommends', '--no-upgrade', '-o', 'DPkg::Lock::Timeout=30', '-o', 'DPkg::Pre-Invoke::=' + hook + ' --package-policy-start', '-o', 'DPkg::Post-Invoke::=' + hook + ' --package-policy-stop', 'install', *missing, timeout=600)
    finally:
        remove_owned_policy(journal)
        journal.pop('package_policy', None); save(journal)


def provision_private_database(journal):
    profile = infrastructure.PROFILE
    own_path(profile, journal)
    if profile.exists():
        return infrastructure.read_profile(profile)
    major_dirs = [p for p in Path('/usr/lib/postgresql').iterdir() if p.name.isdigit()]
    if not major_dirs:
        raise ValueError('未找到 PostgreSQL 运行时')
    major = str(max(int(p.name) for p in major_dirs))
    if int(major) < 14:
        raise ValueError('Pro 需要 PostgreSQL 14 或以上')
    pg, redis = infrastructure.PG_PORT, infrastructure.REDIS_PORT
    import socket
    for port in (pg, redis):
        with socket.socket() as probe:
            try:
                probe.bind(('127.0.0.1', port))
            except OSError:
                raise ValueError('本项目数据库预留端口已占用，不会改动现有数据库') from None
    cluster = Path('/etc/postgresql') / major / 'guangyue'
    own_path(cluster, journal)
    if not cluster.exists():
        run('pg_createcluster', major, 'guangyue', '--port', str(pg), '--start-conf=auto', '--', '--auth-local=peer', '--auth-host=scram-sha-256')
    conf = cluster / 'conf.d/guangyue.conf'
    conf.write_text("listen_addresses = '127.0.0.1'\nshared_buffers = '128MB'\nmax_connections = 40\nwork_mem = '4MB'\nmaintenance_work_mem = '64MB'\npassword_encryption = 'scram-sha-256'\nlog_statement = 'none'\nlog_min_error_statement = 'panic'\n")
    shutil.chown(conf, user='root', group='postgres'); conf.chmod(0o640)
    budget = Path('/etc/systemd/system') / ('postgresql@' + major + '-guangyue.service.d')
    own_path(budget, journal); budget.mkdir(mode=0o755, exist_ok=True)
    (budget / 'guangyue.conf').write_text('[Service]\nMemoryHigh=512M\nMemoryMax=768M\nTasksMax=128\n')
    journal['pg_unit'] = 'postgresql@' + major + '-guangyue'; save(journal)
    run('systemctl', 'daemon-reload'); run('pg_ctlcluster', major, 'guangyue', 'start')
    pg_password, redis_password = secrets.token_urlsafe(36), secrets.token_urlsafe(36)
    # Save generated credentials before changing the dedicated database. Resume
    # never generates a replacement seed or resets an existing administrator.
    credentials = journal.get('database_credentials') or {'pg': pg_password, 'redis': redis_password}
    journal['database_credentials'] = credentials; save(journal)
    pg_password, redis_password = credentials['pg'], credentials['redis']
    exists = run('runuser', '-u', 'postgres', '--', 'psql', '-X', '-A', '-t', '-p', str(pg), '-d', 'postgres', '-c', "SELECT 1 FROM pg_roles WHERE rolname='guangyue'").strip()
    if not exists:
        run('runuser', '-u', 'postgres', '--', 'psql', '-X', '-v', 'ON_ERROR_STOP=1', '-p', str(pg), '-d', 'postgres', data="CREATE ROLE guangyue LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD '" + pg_password + "';\n")
    database_exists = run('runuser', '-u', 'postgres', '--', 'psql', '-X', '-A', '-t', '-p', str(pg), '-d', 'postgres', '-c', "SELECT 1 FROM pg_database WHERE datname='guangyue'").strip()
    if not database_exists:
        run('runuser', '-u', 'postgres', '--', 'psql', '-X', '-v', 'ON_ERROR_STOP=1', '-p', str(pg), '-d', 'postgres', data='CREATE DATABASE guangyue OWNER guangyue;\n')
    redis_conf = Path('/etc/guangyue-redis.conf'); own_path(redis_conf, journal)
    redis_conf.write_text(f'bind 127.0.0.1\nport {redis}\nprotected-mode yes\nrequirepass {redis_password}\nmaxmemory 64mb\nmaxmemory-policy allkeys-lru\nsave ""\nappendonly no\ndaemonize no\nloglevel warning\nlogfile /dev/null\n')
    shutil.chown(redis_conf, user='root', group='redis'); redis_conf.chmod(0o640)
    unit = own_unit('guangyue-redis', journal)
    unit.write_text('[Unit]\nDescription=Guangyue private ephemeral cache\nAfter=network.target\n[Service]\nUser=redis\nGroup=redis\nExecStart=/usr/bin/redis-server /etc/guangyue-redis.conf\nRestart=on-failure\nNoNewPrivileges=true\nPrivateTmp=true\nProtectSystem=strict\nProtectHome=true\nMemoryMax=128M\nTasksMax=32\nUMask=0077\n[Install]\nWantedBy=multi-user.target\n'); unit.chmod(0o644)
    journal.setdefault('unit_hashes', {})['guangyue-redis'] = oneclick.sha256(unit); save(journal)
    value = {'database': {'driver': 'postgres', 'dsn': f'postgres://guangyue:{pg_password}@127.0.0.1:{pg}/guangyue?sslmode=disable', 'max_connections': 8}, 'redis_url': f'redis://:{redis_password}@127.0.0.1:{redis}/0'}
    atomic_json(profile, value)
    run('systemctl', 'daemon-reload'); run('systemctl', 'enable', '--now', 'guangyue-redis'); run('systemctl', 'enable', journal['pg_unit'])
    journal.pop('database_credentials', None); save(journal)
    return value


def ensure_started_database(journal):
    if journal.get('pg_unit'):
        run('systemctl', 'start', journal['pg_unit'])
    if 'guangyue-redis' in journal['units']:
        run('systemctl', 'start', 'guangyue-redis')


def stop_owned(journal):
    for name in reversed(journal.get('units', [])):
        path = unit_path(name)
        expected = journal.get('unit_hashes', {}).get(name)
        if expected and path.is_file() and not path.is_symlink() and oneclick.sha256(path) == expected:
            subprocess.run(['systemctl', 'stop', name], capture_output=True)
    if journal.get('pg_unit'):
        subprocess.run(['systemctl', 'stop', journal['pg_unit']], capture_output=True)
    # Keep only this installation's private data and diagnostics for --resume;
    # never remove packages, other units, Nginx fragments or shared database data.


def native_health(expected):
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    for _ in range(35):
        try:
            with opener.open('http://127.0.0.1:19100/api/health', timeout=2) as response:
                if response.status == 200 and json.load(response).get('version') == expected:
                    run('systemctl', 'is-active', '--quiet', *UNITS)
                    return
        except (OSError, ValueError):
            time.sleep(1)
    raise ValueError('面板或代理核心未通过启动检查，自己的服务已停止，现场保留供续装')


def install(plan, bundle, *, cert_file=None, key_file=None):
    oneclick.validate_plan(plan); oneclick.verify_bundle(bundle, plan)
    if cert_file is not None or key_file is not None:
        if cert_file is None or key_file is None or os.environ.get('GITHUB_ACTIONS') != 'true' or os.environ.get('GY_ONECLICK_INTEGRATION') != '1':
            raise ValueError('固定证书参数仅供受控 GitHub Actions 集成测试；生产使用自动 ACME')
    journal = load(plan)
    if journal.get('phase') == 'installed':
        native_health(plan['version'])
        return journal['result']
    try:
        for name in journal.get('units', []):
            path = unit_path(name)
            if path.exists() and (path.is_symlink() or journal.get('unit_hashes', {}).get(name) != oneclick.sha256(path)):
                raise ValueError('受管服务已经被外部修改，不会停止或覆盖：' + name)
        # Recover an interrupted activation only when the on-disk unit is still
        # exactly the one this receipt created. Never stop an externally edited
        # unit or a foreign process that subsequently occupied public 443.
        stop_owned(journal)
        prepare_packages(plan['edition'], journal)
        # Preflight is repeated after dependency preparation and before any
        # listener starts, so a new foreign 443 service is never stopped.
        oneclick.port_preflight(True)
        oneclick.nginx_preflight()
        try:
            user = pwd.getpwnam('guangyue')
            if not journal.get('user_created') and user.pw_dir != str(STATE):
                raise ValueError('已有同名系统用户不属于本项目')
        except KeyError:
            run('useradd', '--system', '--user-group', '--home-dir', str(STATE), '--shell', '/usr/sbin/nologin', 'guangyue')
            user = pwd.getpwnam('guangyue'); journal['user_created'] = True; save(journal)
        for path in (APP, STATE, ACME):
            own_path(path, journal); path.mkdir(mode=0o755 if path == APP else 0o700, exist_ok=True)
            path.chmod(0o755 if path == APP else 0o700)
            if path != APP:
                os.chown(path, user.pw_uid, user.pw_gid)
        tls = STATE / 'tls'
        if tls.is_symlink():
            raise ValueError('TLS 状态目录存在不可信链接')
        tls.mkdir(mode=0o700, exist_ok=True); tls.chmod(0o700)
        os.chown(tls, user.pw_uid, user.pw_gid)
        if journal.get('bundle_installed') is not True:
            for name in ('bin', 'web', 'deploy', 'licenses'):
                if (APP / name).exists():
                    shutil.rmtree(APP / name)
                shutil.copytree(Path(bundle) / name, APP / name)
            for src, dest in [('guangyue-linux-amd64', 'guangyue'), ('guangyue-gateway-linux-amd64', 'guangyue-gateway'), ('hysteria-node-linux-amd64', 'hysteria'), ('xray-linux-amd64', 'xray'), ('mihomo-linux-amd64', 'mihomo')]:
                shutil.copyfile(APP / 'bin' / src, APP / 'bin' / dest); (APP / 'bin' / dest).chmod(0o755)
            shutil.copyfile(Path(bundle) / 'VERSION', APP / 'VERSION')
            journal['bundle_installed'] = True; save(journal)
        own_path(CONFIG, journal)
        if not CONFIG.exists():
            run(str(APP / 'bin/guangyue'), '-init')
            journal['config_initialized'] = True; save(journal)
        config = json.loads(CONFIG.read_text())
        config.update(public_url='https://' + plan['panel_domain'], vless_host=plan['node_domain'], hy2_host=plan['node_domain'], reality_sni=plan['reality_sni'], reality_target=plan['reality_sni'] + ':443', cert=str(STATE / 'tls/current/fullchain.pem'), cert_key=str(STATE / 'tls/current/privkey.pem'), hy2_optimized=False, private_gateway=True)
        if plan['edition'] == 'pro':
            config.update(provision_private_database(journal))
            ensure_started_database(journal)
        else:
            config.update(database={'driver': 'sqlite'}, redis_url='')
        config.update(edition=plan['edition'], site_id=plan['site_id'], role='controller' if plan['edition'] == 'pro' else 'standalone')
        atomic_json(CONFIG, config, 0o640, 'guangyue')
        socket_dir = Path('/run/guangyue-reality'); own_path(socket_dir, journal)
        socket_dir.mkdir(mode=0o700, exist_ok=True); os.chown(socket_dir, user.pw_uid, user.pw_gid)
        tmpfile = Path('/etc/tmpfiles.d/guangyue-reality.conf'); own_path(tmpfile, journal)
        tmpfile.write_text('d /run/guangyue-reality 0700 guangyue guangyue -\n'); tmpfile.chmod(0o644)
        own_path(GATEWAY_CONFIG, journal)
        gateway = {'panel_domain': plan['panel_domain'], 'node_domain': plan['node_domain'], 'reality_sni': plan['reality_sni'], 'accept_tos': True, 'acme_email': plan.get('acme_email', ''), 'state_dir': str(ACME), 'tls_dir': str(STATE / 'tls'), 'panel_upstream': 'http://127.0.0.1:19100', 'reality_upstream': '127.0.0.1:18443', 'reality_socket_dir': str(socket_dir), 'listen': ':443'}
        if cert_file is not None:
            # The fixture key is never a production CLI/config override. Keep
            # it inside the same private UID boundary as normal ACME material.
            fixture = STATE / 'gateway-fixture'
            if fixture.is_symlink():
                raise ValueError('测试证书目录存在不安全链接')
            fixture.mkdir(mode=0o700, exist_ok=True); fixture.chmod(0o700)
            os.chown(fixture, user.pw_uid, user.pw_gid)
            for source, name in ((cert_file, 'fullchain.pem'), (key_file, 'privkey.pem')):
                target = fixture / name
                if target.is_symlink():
                    raise ValueError('测试证书目标存在不安全链接')
                shutil.copyfile(source, target); target.chmod(0o600)
                os.chown(target, user.pw_uid, user.pw_gid)
            gateway.update(cert_file=str(fixture / 'fullchain.pem'), key_file=str(fixture / 'privkey.pem'))
        atomic_json(GATEWAY_CONFIG, gateway, 0o640, 'guangyue')
        for name in UNITS:
            path = own_unit(name, journal)
            source = Path(bundle) / 'deploy' / (name + '.service')
            if not source.is_file():
                raise ValueError('安装包缺少私有网关服务组件')
            text = infrastructure.service_text(source, plan['edition']) if name == 'guangyue' else source.read_text()
            path.write_text(text); path.chmod(0o644)
            journal.setdefault('unit_hashes', {})[name] = oneclick.sha256(path); save(journal)
        run('systemctl', 'daemon-reload')
        run(str(APP / 'bin/guangyue-gateway'), '-config', str(GATEWAY_CONFIG), '-check')
        journal['phase'] = 'certificate'; save(journal)
        print('通过 TCP 443 自动申请证书，已有 Nginx 的 HTTP/80 不变…', flush=True)
        run('systemctl', 'start', 'guangyue-gateway')
        run(str(APP / 'bin/guangyue-gateway'), '-config', str(GATEWAY_CONFIG), '-wait-ready', '180s', timeout=195)
        journal['phase'] = 'starting'; save(journal)
        run('runuser', '-u', 'guangyue', '--', str(APP / 'bin/guangyue'), '-prepare')
        run('runuser', '-u', 'guangyue', '--', str(APP / 'bin/xray'), 'run', '-test', '-c', str(STATE / 'xray.json'))
        run('systemctl', 'start', 'guangyue', 'guangyue-xray', 'guangyue-hy2')
        native_health(plan['version'])
        own_path(DEPLOYMENT, journal)
        atomic_json(DEPLOYMENT, dict(plan, private_gateway=True))
        import update_state
        for path in (update_state.ROOT, update_state.HELPER):
            own_path(path, journal)
        maintenance = ('guangyue-updater.socket', 'guangyue-updater.service', 'guangyue-gateway-cert.path', 'guangyue-gateway-cert.service', 'guangyue-gateway-cert.timer')
        for name in maintenance:
            own_unit(name, journal)
        try:
            update_state.setup(plan['version'], Path(bundle) / 'deploy')
        finally:
            # Record even partially provisioned maintenance units so a failed
            # setup can stop exactly what this installation created.
            for name in maintenance:
                path = unit_path(name)
                if path.is_file() and not path.is_symlink():
                    journal.setdefault('unit_hashes', {})[name] = oneclick.sha256(path)
            save(journal)
        run('systemctl', 'enable', *UNITS)
        result = {'version': plan['version'], 'public_url': config['public_url'], 'credentials_file': str(STATE / 'initial-owner.json'), 'stage': 'installed'}
        journal.update(phase='installed', result=result, error=''); save(journal)
        return result
    except Exception as exc:
        stop_owned(journal)
        journal.update(phase='failed', error=oneclick.redact(str(exc)) if isinstance(exc, ValueError) else type(exc).__name__); save(journal)
        raise ValueError(journal['error']) from None


if __name__ == '__main__':
    if sys.argv[1:] not in (['--package-policy-start'], ['--package-policy-stop']):
        raise SystemExit('Fixed package-policy hook only')
    try:
        package_policy_hook(sys.argv[1] == '--package-policy-start')
    except Exception:
        raise SystemExit('Package service-start policy hook failed; original policies were preserved')
