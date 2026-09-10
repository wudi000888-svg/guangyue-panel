#!/usr/bin/env python3
"""Fixed, reversible host tuning. Only the root maintenance worker writes sysctls."""
import json
import os
import re
import subprocess
import sys
import time
from pathlib import Path
sys.dont_write_bytecode = True
import update_state as state
from common import deployment_lock

SYSCTL = Path('/etc/sysctl.d/99-zz-guangyue-network.conf')
KEYS = {'bbr': {'net.ipv4.tcp_congestion_control': 'bbr', 'net.core.default_qdisc': 'fq'},
        'hy2': {'net.core.rmem_max': '8388608', 'net.core.wmem_max': '8388608'}}


def run(*args):
    return subprocess.check_output(args, stderr=subprocess.DEVNULL, timeout=40).decode().strip()


def sysget(key):
    try: return run('sysctl', '-n', key)
    except (OSError, subprocess.SubprocessError): return ''


def atomic(path, content, mode=0o600):
    temp = path.with_name(path.name + '.network-new')
    fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, mode)
    with os.fdopen(fd, 'w') as output:
        output.write(content); output.flush(); os.fsync(output.fileno())
    temp.chmod(mode); os.replace(temp, path)


def snapshot():
    config = json.loads(state.CONFIG.read_text())
    return {'values': {k: sysget(k) for group in KEYS.values() for k in group},
            'config': config.get('hy2_optimized', False),
            'file': SYSCTL.read_text() if SYSCTL.exists() else None,
            'policy': state.read('network.json')}


def status():
    policy = state.read('network.json') or {}
    config = json.loads(state.CONFIG.read_text())
    values = {k: sysget(k) for group in KEYS.values() for k in group}
    hy = {}
    try: hy = json.loads((Path(config['state_dir']) / 'hy2.json').read_text())
    except (OSError, ValueError): pass
    bbr = values['net.ipv4.tcp_congestion_control'] == 'bbr'
    optimized = hy.get('ignoreClientBandwidth') is True and hy.get('congestion', {}).get('type') == 'bbr'
    hy_running = False
    try:
        started = int(run('systemctl', 'show', 'guangyue-hy2', '--property=ExecMainStartTimestampMonotonic', '--value')) / 1e6
        boot_age = float(Path('/proc/uptime').read_text().split()[0])
        run('systemctl', 'is-active', '--quiet', 'guangyue-hy2')
        hy_running = started > 0 and (Path(config['state_dir']) / 'hy2.json').stat().st_mtime <= time.time() - boot_age + started + 0.1
    except (OSError, ValueError, subprocess.SubprocessError): pass
    operation = state.read('network-operation.json')
    desired = policy.get('desired', {'bbr': bbr, 'hy2': config.get('hy2_optimized', False)})
    return {'available': True, 'revision': policy.get('revision', 'initial'), 'managed': bool(policy),
            'desired': desired, 'bbr_supported': 'bbr' in sysget('net.ipv4.tcp_available_congestion_control').split(),
            'actual': {'bbr': bbr, 'hy2': optimized and hy_running, 'hy2_running': hy_running, 'congestion_control': values['net.ipv4.tcp_congestion_control'],
                       'default_qdisc': values['net.core.default_qdisc'], 'rmem_max': values['net.core.rmem_max'], 'wmem_max': values['net.core.wmem_max']},
            'operation': operation}


def set_hy(enabled, restart):
    config = json.loads(state.CONFIG.read_text())
    changed = config.get('hy2_optimized', False) != enabled
    config['hy2_optimized'] = enabled
    owner = state.CONFIG.stat()
    atomic(state.CONFIG, json.dumps(config, indent=2) + '\n', 0o640)
    os.chown(state.CONFIG, owner.st_uid, owner.st_gid)
    if restart and changed:
        run('systemctl', 'restart', 'guangyue')
        # The panel reconciles the HY profile, validates it, and restarts HY2.
        import urllib.request
        client = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        for _ in range(35):
            try:
                with client.open('http://127.0.0.1:19100/api/health', timeout=1) as response:
                    actual = status()['actual']
                    if response.status == 200 and actual['hy2_running'] and actual['hy2'] == enabled:
                        run('systemctl', 'is-active', '--quiet', 'guangyue', 'guangyue-hy2')
                        return
            except (OSError, ValueError, subprocess.SubprocessError): pass
            time.sleep(1)
        raise ValueError('HY2 优化未生效，已尝试恢复原配置')


def restore(saved, restart=True):
    if saved['file'] is None: SYSCTL.unlink(missing_ok=True)
    else: atomic(SYSCTL, saved['file'], 0o644)
    for key, value in saved['values'].items():
        if value: run('sysctl', '-w', key + '=' + value)
    set_hy(saved['config'], restart)
    state.write('network.json', saved['policy'])


def apply(bbr, hy2, restart=True):
    before = snapshot()
    if bbr and 'bbr' not in sysget('net.ipv4.tcp_available_congestion_control').split():
        try: run('modprobe', 'tcp_bbr')
        except (OSError, subprocess.SubprocessError): pass
        if 'bbr' not in sysget('net.ipv4.tcp_available_congestion_control').split():
            raise ValueError('当前内核不支持 BBR，请使用支持 BBR 的宿主机内核')
    state.write('network-transaction.json', before)
    previous = before['policy'] or {}
    original = dict(previous.get('original', {}))
    managed = dict(previous.get('values', {}))
    try:
        for group, enabled in [('bbr', bbr), ('hy2', hy2)]:
            for key, target in KEYS[group].items():
                current = before['values'][key]
                if enabled:
                    if not current: raise ValueError('当前宿主机不提供所需网络参数')
                    if key not in original: original[key] = current
                    managed[key] = str(max(int(current), int(target))) if group == 'hy2' else target
                elif key in original:
                    # Do not overwrite an administrator's subsequent external change.
                    if current == managed.get(key): run('sysctl', '-w', key + '=' + original[key])
                    original.pop(key); managed.pop(key, None)
        text = '# Managed by Guangyue Panel. Disable in panel to restore previous values.\n'
        text += ''.join(k + ' = ' + v + '\n' for k, v in sorted(managed.items()))
        if managed: atomic(SYSCTL, text, 0o644)
        else: SYSCTL.unlink(missing_ok=True)
        for key, value in managed.items(): run('sysctl', '-w', key + '=' + value)
        set_hy(hy2, restart)
        state.write('network.json', {'desired': {'bbr': bbr, 'hy2': hy2}, 'values': managed,
                                    'original': original, 'revision': str(time.time_ns())})
        state.write('network-transaction.json', None)
    except Exception:
        restore(before, restart)
        state.write('network-transaction.json', None)
        raise
    return before


def validate(request):
    if not isinstance(request, dict) or set(request) != {'bbr', 'hy2', 'revision'} or any(type(request[x]) is not bool for x in ['bbr', 'hy2']) or not isinstance(request['revision'], str) or not re.fullmatch(r'initial|[0-9]{1,24}', request['revision']):
        raise ValueError('网络优化请求无效')


def worker():
    with deployment_lock():
        op = state.read('network-operation.json')
        journal = state.read('network-transaction.json')
        if journal:
            restore(journal)
            state.write('network-transaction.json', None)
            state.write('network-operation.json', dict(op or {}, stage='failed', error='上次网络操作被中断，已恢复原设置'))
            return
        if op and op['stage'] == 'applying':
            state.write('network-operation.json', dict(op, stage='failed', error='上次网络操作被中断，请核对生效状态'))
            return
        if not op or op['stage'] != 'queued': raise ValueError('没有待执行的网络设置')
        request = op['request']; validate(request)
        try:
            if status()['revision'] != request['revision']: raise ValueError('网络设置已变化，请刷新后重试')
            state.write('network-operation.json', dict(op, stage='applying'))
            apply(request['bbr'], request['hy2'])
            state.write('network-operation.json', dict(op, stage='succeeded'))
        except Exception as exc:
            state.write('network-operation.json', dict(op, stage='failed', error=str(exc) if isinstance(exc, ValueError) else '网络优化未完成，请检查服务器运行状态'))


if __name__ == '__main__':
    if os.geteuid() != 0 or sys.argv[1:] != ['apply']: raise SystemExit('Root maintenance worker only')
    worker()
