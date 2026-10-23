#!/usr/bin/env python3
"""Bounded retention for private upgrade snapshots in /root/guangyue-backups.

Every upgrade or code rollback stores a stopped snapshot (state, application,
configuration and, for Pro, a PostgreSQL dump). Without pruning these grow
until the free-space preflight blocks the next upgrade. After a committed
version change only the newest N snapshots are kept, plus every snapshot that
recovery may still need:

* the snapshot created by the operation that just committed;
* the newest snapshot of the currently running version;
* the newest snapshot of the immediately previous (highest lower) version;
* any snapshot named by an update/recovery journal.

Only root-owned real directories created by the upgraders (``upgrade-*`` and
``docker-upgrade-*``) are considered; anything else in the directory is left
untouched. Invalid retention settings fail closed: nothing is removed.
"""
import argparse
import json
import os
import shutil
import stat
import sys
from pathlib import Path
sys.dont_write_bytecode = True

BACKUPS = Path('/root/guangyue-backups')
PREFIXES = ('upgrade-', 'docker-upgrade-')
DEFAULT_KEEP = 5
MAX_KEEP = 100
CONFIG_KEY = 'backup_retention'


def keep_count(config=None, override=None):
    """Return the configured retention count; reject anything but 1..100."""
    value = override if override is not None else (config or {}).get(CONFIG_KEY, DEFAULT_KEEP)
    if isinstance(value, bool) or not isinstance(value, int) or not 1 <= value <= MAX_KEEP:
        raise ValueError('备份保留数量必须是 1 到 %d 的整数' % MAX_KEEP)
    return value


def parse_version(value):
    try:
        parts = value.split('.')
        if len(parts) != 3 or not all(p.isdigit() and len(p) <= 6 for p in parts): return None
        return tuple(int(p) for p in parts)
    except AttributeError:
        return None


def backup_version(path):
    target = Path(path) / 'app/VERSION'
    try:
        if target.is_symlink() or not target.is_file() or target.stat().st_size > 64: return None
        value = target.read_text().strip()
    except (OSError, UnicodeError):
        return None
    return value if parse_version(value) else None


def entries(root=BACKUPS):
    root = Path(root)
    try:
        info = root.lstat()
    except FileNotFoundError:
        return []
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid():
        raise ValueError('备份目录类型或所有者无效')
    result = []
    for child in root.iterdir():
        if not child.name.startswith(PREFIXES): continue
        value = child.lstat()
        if stat.S_ISDIR(value.st_mode) and value.st_uid == os.geteuid():
            result.append(child)
    return result


def _normal(path):
    return Path(os.path.normpath(str(path))) if path else None


def plan(root, keep, current, protected=(), history=()):
    """Return (kept, removable) lists; removable is ordered oldest first."""
    keep = keep_count(override=keep)
    created = {}
    for row in history or ():
        if isinstance(row, dict) and isinstance(row.get('backup'), str) and isinstance(row.get('created'), int) and not isinstance(row.get('created'), bool):
            created[str(_normal(row['backup']))] = row['created']
    items = []
    for path in entries(root):
        stamp = created.get(str(_normal(path)))
        if stamp is None: stamp = int(path.lstat().st_mtime)
        items.append((stamp, path.name, path, backup_version(path)))
    items.sort(key=lambda item: (item[0], item[1]), reverse=True)  # newest first
    retained = {item[2] for item in items[:keep]}
    guarded = {_normal(p) for p in protected if p}
    retained |= {item[2] for item in items if _normal(item[2]) in guarded}
    now = parse_version(current)
    if now is None:
        raise ValueError('当前版本无效，跳过备份清理')
    same = [item for item in items if item[3] == current]
    if same: retained.add(same[0][2])
    lower = [parse_version(item[3]) for item in items if item[3] and parse_version(item[3]) < now]
    if lower:
        previous = '.'.join(str(n) for n in max(lower))
        retained.add(next(item[2] for item in items if item[3] == previous))
    kept = [item[2] for item in items if item[2] in retained]
    removable = [item[2] for item in reversed(items) if item[2] not in retained]
    return kept, removable


def prune(root, keep, current, protected=(), history=(), log=print):
    """Remove snapshots outside the retention plan; return the removed paths."""
    _, removable = plan(root, keep, current, protected, history)
    removed = []
    for path in removable:
        try:
            # Re-check immediately before deletion; never follow a swapped link.
            info = path.lstat()
            if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid(): continue
            shutil.rmtree(path)
            removed.append(path)
        except OSError as exc:
            log('Backup pruning could not remove ' + path.name + ': ' + type(exc).__name__)
    if removed:
        log('Pruned %d old upgrade backup(s); kept newest %d plus protected snapshots.' % (len(removed), keep))
    return removed


def without_removed(history, removed):
    names = {str(_normal(p)) for p in removed}
    return [row for row in history or [] if not (isinstance(row, dict) and str(_normal(row.get('backup'))) in names)]


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description='List or prune old upgrade backups. Read-only unless --apply is given.')
    parser.add_argument('--keep', type=int, help='override the configured number of newest backups to keep (1-100)')
    parser.add_argument('--apply', action='store_true', help='delete the listed backups')
    args = parser.parse_args()
    if os.geteuid() != 0: raise ValueError('root is required')
    sys.path.insert(0, str(Path(__file__).resolve().parent))
    import deployment_mode
    from common import deployment_lock
    with deployment_lock():
        if deployment_mode.docker():
            import docker_deploy
            removed = docker_deploy.prune_backups(override=args.keep, dry_run=not args.apply)
        else:
            import upgrade
            removed = upgrade.prune_backups(json.loads(upgrade.CONFIG.read_text()), override=args.keep, dry_run=not args.apply)
    if not args.apply:
        print('\n'.join(['Would remove: ' + str(p) for p in removed] or ['Nothing to prune.']))
        if removed: print('Add --apply to delete them.')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError) as exc:
        raise SystemExit('Backup pruning failed: ' + (str(exc) if isinstance(exc, ValueError) else type(exc).__name__))
