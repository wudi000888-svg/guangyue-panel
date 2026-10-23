import json
import os
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import backup_retention as retention
import docker_deploy as docker
from test_docker_deploy import TemporaryDockerTest, marker


class RetentionTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory(); self.addCleanup(directory.cleanup)
        self.root = Path(directory.name) / 'backups'; self.root.mkdir(mode=0o700)
        self.clock = 1_700_000_000

    def snapshot(self, version, prefix='upgrade-', name=None):
        """Create a snapshot; each call is one second newer than the last."""
        self.clock += 1
        path = self.root / (name or '%s%d-%s' % (prefix, self.clock, version))
        (path / 'app').mkdir(parents=True)
        if version: (path / 'app/VERSION').write_text(version + '\n')
        (path / 'state').mkdir(); (path / 'state/panel.db').write_bytes(b'x' * 16)
        os.utime(path, (self.clock, self.clock))
        return path


class KeepCountTests(unittest.TestCase):
    def test_default_override_and_invalid_values_fail_closed(self):
        self.assertEqual(retention.keep_count({}), 5)
        self.assertEqual(retention.keep_count({'backup_retention': 8}), 8)
        self.assertEqual(retention.keep_count({'backup_retention': 8}, 2), 2)
        for value in (0, -1, 101, True, '5', 2.5, None):
            with self.subTest(value=value), self.assertRaises(ValueError):
                retention.keep_count({'backup_retention': value} if value is not None else {'backup_retention': None})


class PlanTests(RetentionTest):
    def test_keeps_newest_n_and_removes_oldest_first(self):
        paths = [self.snapshot('0.%d.0' % n) for n in range(30, 38)]
        kept, removable = retention.plan(self.root, 3, current='0.38.0')
        self.assertEqual(kept, paths[:4:-1])
        self.assertEqual(removable, paths[:5])

    def test_running_and_previous_versions_are_protected_beyond_n(self):
        # After rolling back to 0.30.0 the running version's snapshot is old.
        running = self.snapshot('0.30.0')
        previous = self.snapshot('0.29.0')
        newer = [self.snapshot('0.%d.0' % n) for n in range(31, 36)]
        kept, removable = retention.plan(self.root, 2, current='0.30.0')
        self.assertIn(running, kept); self.assertIn(previous, kept)
        self.assertEqual(set(kept), {running, previous, newer[-1], newer[-2]})
        self.assertEqual(removable, newer[:3])

    def test_only_newest_snapshot_of_a_protected_version_is_retained(self):
        old = self.snapshot('0.40.0'); new = self.snapshot('0.40.0')
        filler = [self.snapshot('0.%d.0' % n) for n in (41, 42)]
        kept, removable = retention.plan(self.root, 1, current='0.40.0')
        self.assertEqual(set(kept), {new, filler[-1]})
        self.assertEqual(removable, [old, filler[0]])

    def test_journal_and_just_created_backups_are_protected(self):
        journal = self.snapshot('0.20.0'); created = self.snapshot('0.21.0')
        for n in range(22, 30): self.snapshot('0.%d.0' % n)
        kept, removable = retention.plan(self.root, 1, current='0.40.0', protected=[str(journal) + '/', created, None])
        self.assertIn(journal, kept); self.assertIn(created, kept)
        self.assertNotIn(journal, removable)

    def test_history_creation_time_orders_snapshots_without_timestamps(self):
        a = self.snapshot('0.30.0', 'docker-upgrade-', 'docker-upgrade-zzz')
        b = self.snapshot('0.31.0', 'docker-upgrade-', 'docker-upgrade-aaa')
        os.utime(a, (self.clock + 50, self.clock + 50))  # mtime would say a is newest
        history = [{'version': '0.30.0', 'backup': str(a), 'created': 10}, {'version': '0.31.0', 'backup': str(b), 'created': 20}]
        c = self.snapshot('0.32.0', 'docker-upgrade-', 'docker-upgrade-mmm')
        _, removable = retention.plan(self.root, 1, current='0.40.0', history=history + [{'backup': str(c), 'created': 30}])
        # c is newest; 0.32.0 is the previous version too. a (created 10) goes first.
        self.assertEqual(removable, [a, b])

    def test_unrelated_entries_links_and_files_are_never_candidates(self):
        for n in range(5): self.snapshot('0.%d.0' % (30 + n))
        (self.root / 'manual-copy').mkdir()
        (self.root / 'upgrade-file').write_text('not a directory')
        outside = self.root.parent / 'outside'; (outside / 'app').mkdir(parents=True)
        (self.root / 'upgrade-link').symlink_to(outside, target_is_directory=True)
        _, removable = retention.plan(self.root, 1, current='0.40.0')
        names = {p.name for p in removable}
        self.assertFalse(names & {'manual-copy', 'upgrade-file', 'upgrade-link'})
        self.assertEqual(len(removable), 4)

    def test_partial_snapshot_without_version_is_prunable_but_counts_by_age(self):
        partial = self.snapshot(None)
        newest = self.snapshot('0.39.0')
        _, removable = retention.plan(self.root, 1, current='0.40.0')
        self.assertEqual(removable, [partial]); self.assertTrue(newest.exists())

    def test_invalid_current_version_and_missing_root(self):
        self.snapshot('0.30.0')
        with self.assertRaises(ValueError): retention.plan(self.root, 1, current='latest')
        self.assertEqual(retention.plan(self.root / 'missing', 1, current='0.40.0'), ([], []))

    def test_root_must_be_a_real_directory(self):
        link = self.root.parent / 'link'; link.symlink_to(self.root, target_is_directory=True)
        with self.assertRaises(ValueError): retention.plan(link, 1, current='0.40.0')


class PruneTests(RetentionTest):
    def test_prune_deletes_without_following_links_inside_snapshots(self):
        outside = self.root.parent / 'precious'; outside.mkdir(); (outside / 'keep').write_text('x')
        old = self.snapshot('0.30.0'); (old / 'state/link').symlink_to(outside, target_is_directory=True)
        for n in range(31, 35): self.snapshot('0.%d.0' % n)
        removed = retention.prune(self.root, 2, current='0.40.0', log=lambda _: None)
        self.assertIn(old, removed); self.assertFalse(old.exists())
        self.assertTrue((outside / 'keep').is_file())
        self.assertEqual(len(list(self.root.iterdir())), 2)

    def test_without_removed_filters_history(self):
        a, b = self.snapshot('0.30.0'), self.snapshot('0.31.0')
        rows = [{'backup': str(a)}, {'backup': str(b)}, 'junk']
        self.assertEqual(retention.without_removed(rows, [a]), [{'backup': str(b)}, 'junk'])


class NativeIntegrationTests(RetentionTest):
    def setUp(self):
        super().setUp()
        import upgrade
        self.upgrade = upgrade
        self.ROOT = self.root.parent / 'updater'
        for target, name, value in [(retention, 'BACKUPS', self.root), (upgrade.updates, 'ROOT', self.ROOT)]:
            p = patch.object(target, name, value); p.start(); self.addCleanup(p.stop)

    def test_prune_after_commit_updates_history_and_keeps_journal_backup(self):
        snaps = [self.snapshot('0.%d.0' % n) for n in range(30, 38)]
        self.upgrade.updates.write('history.json', [{'version': p.name.split('-')[-1], 'backup': str(p), 'created': self.clock - 100 + i} for i, p in enumerate(snaps)])
        self.upgrade.updates.write('transaction.json', {'backup': str(snaps[0]), 'phase': 'committed'})
        with patch.object(self.upgrade.updates, 'current', return_value='0.38.0'):
            removed = self.upgrade.prune_backups({'backup_retention': 3}, snaps[-1])
        self.assertEqual(removed, snaps[1:5])
        self.assertTrue(snaps[0].exists())
        history = self.upgrade.updates.read('history.json')
        self.assertEqual([row['backup'] for row in history], [str(snaps[0])] + [str(p) for p in snaps[5:]])

    def test_dry_run_lists_without_deleting_and_invalid_config_skips(self):
        snaps = [self.snapshot('0.%d.0' % n) for n in range(30, 34)]
        with patch.object(self.upgrade.updates, 'current', return_value='0.40.0'):
            listed = self.upgrade.prune_backups({}, override=1, dry_run=True)
            self.assertEqual(listed, snaps[:3])
            self.assertTrue(all(p.exists() for p in snaps))
            with patch('builtins.print') as output:
                self.assertEqual(self.upgrade.prune_backups({'backup_retention': 0}), [])
            self.assertIn('skipped', output.call_args.args[0])
        self.assertTrue(all(p.exists() for p in snaps))

    def test_transaction_prunes_only_after_successful_commit(self):
        config = self.root.parent / 'config.json'; config.write_text('{}')
        calls = []
        with patch.object(self.upgrade, 'CONFIG', config), patch.object(self.upgrade.deployment_mode, 'private_gateway', return_value=False), \
                patch.object(self.upgrade.updates, 'baseline'), patch.object(self.upgrade.updates, 'require_floor'), \
                patch.object(self.upgrade.updates, 'current', return_value='0.41.0'), patch.object(self.upgrade.updates, 'schema', return_value='s'), \
                patch.object(self.upgrade.updates, 'remember'), patch.object(self.upgrade, 'run'), \
                patch.object(self.upgrade, 'backup_current', return_value=self.root / 'upgrade-new'), patch.object(self.upgrade, 'prepare_start'), \
                patch.object(self.upgrade, 'restore_current'), \
                patch.object(self.upgrade, 'prune_backups', side_effect=lambda *a, **k: calls.append(a)):
            self.upgrade.transaction(lambda backup: None, '0.42.0', {'backup_retention': 4})
            self.assertEqual(calls, [({'backup_retention': 4}, self.root / 'upgrade-new')])
            def fail(backup): raise ValueError('boom')
            with self.assertRaises(ValueError): self.upgrade.transaction(fail, '0.42.0', {})
            self.assertEqual(len(calls), 1)


class DockerIntegrationTests(TemporaryDockerTest):
    def test_docker_prune_uses_marker_version_and_transaction_backup(self):
        m = marker(); m['version'] = '0.40.0'; self.json(docker.MARKER, m)
        self.json(docker.CONFIG, {'state_dir': str(docker.STATE), 'backup_retention': 1}); docker.CONFIG.chmod(0o640)
        snaps = []
        for n, v in enumerate(['0.36.0', '0.37.0', '0.38.0', '0.39.0']):
            p = docker.BACKUPS / ('docker-upgrade-%d' % n); (p / 'app').mkdir(parents=True); (p / 'app/VERSION').write_text(v)
            os.utime(p, (1000 + n, 1000 + n)); snaps.append(p)
        self.json(docker.history_path(), [{'version': '0.36.0', 'backup': str(snaps[0]), 'created': 1000}])
        self.json(docker.transaction_file(), {'backup': str(snaps[1])})
        with patch.object(docker, 'load', return_value=m):
            self.assertEqual(docker.prune_backups(dry_run=True), [snaps[0], snaps[2]])
            removed = docker.prune_backups(keep_backup=snaps[3])
        # 0.39.0 is newest and the previous version; 0.37.0 is named by the journal.
        self.assertEqual(removed, [snaps[0], snaps[2]])
        self.assertEqual(sorted(p.name for p in docker.BACKUPS.iterdir()), ['docker-upgrade-1', 'docker-upgrade-3'])
        self.assertEqual(docker.safe_json(docker.history_path()), [])

    def test_docker_transaction_prunes_after_commit_only(self):
        calls = []
        with patch.object(docker, 'preflight'), patch.object(docker, 'load', return_value=marker()), patch.object(docker, 'read_config', return_value={}), \
                patch.object(docker, 'schema', return_value='s'), patch.object(docker, 'stop_application'), \
                patch.object(docker, 'backup_current', return_value=docker.BACKUPS / 'docker-upgrade-new'), patch.object(docker, 'health'), \
                patch.object(docker, 'validate_runtime'), patch.object(docker, 'remember'), patch.object(docker, 'complete_gate'), \
                patch.object(docker, 'restore_snapshot', return_value=marker()), \
                patch.object(docker, 'prune_backups', side_effect=lambda **k: calls.append(k)):
            docker.transaction(lambda m, c: None, '0.41.1', lambda stage: None)
            self.assertEqual(calls, [{'keep_backup': docker.BACKUPS / 'docker-upgrade-new'}])
            docker.atomic_json(docker.transaction_file(), None)
            def fail(m, c): raise ValueError('boom')
            with self.assertRaises(ValueError): docker.transaction(fail, '0.41.1', lambda stage: None)
        self.assertEqual(len(calls), 1)


if __name__ == '__main__':
    unittest.main()
