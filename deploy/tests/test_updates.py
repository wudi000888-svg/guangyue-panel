import hashlib
import io
import json
import os
import shutil
import sqlite3
import sys
import tarfile
import tempfile
import time
import unittest
from pathlib import Path
from unittest.mock import patch
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import update_state as state
import update_agent as agent
import upgrade


class UpdateTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(); self.addCleanup(self.temp.cleanup); self.root = Path(self.temp.name)
        self.app = self.root / 'app'; self.app.mkdir(); (self.app / 'VERSION').write_text('0.18.0\n')
        self.data = self.root / 'data'; self.data.mkdir()
        self.config = self.root / 'config.json'; self.config.write_text(json.dumps({'edition': 'lite', 'role': 'standalone', 'site_id': 'test', 'state_dir': str(self.data), 'database': {'driver': 'sqlite'}}))
        db = sqlite3.connect(self.data / 'panel.db'); db.execute('CREATE TABLE schema_migrations(version TEXT, checksum TEXT)'); db.execute("INSERT INTO schema_migrations VALUES('001','fixed')"); db.commit(); db.close()
        for obj, key, value in [(state, 'ROOT', self.root / 'updates'), (state, 'APP', self.app), (state, 'CONFIG', self.config), (upgrade, 'APP', self.app), (upgrade, 'CONFIG', self.config)]:
            p = patch.object(obj, key, value); p.start(); self.addCleanup(p.stop)
        state.baseline('0.17.0')

    def request(self, **kw):
        return dict(action='update', version='0.19.0', expected_version='0.18.0', request_id='11111111-2222-4333-a444-555555555555', **kw)

    def cache(self):
        state.write('catalog.json', {'checked_at': int(time.time()), 'releases': [{'version': '0.19.0', 'editions': ['lite']}]})

    def test_numeric_floor_is_persistent_and_checked_on_both_paths(self):
        self.assertGreater(state.version('0.10.0'), state.version('0.9.99'))
        self.assertEqual(state.baseline('0.99.0')['version'], '0.17.0')
        for action in ['update', 'rollback']:
            request = self.request(); request.update(action=action, version='0.16.99')
            with self.assertRaises(ValueError): agent.submit(request, launch=False)
        self.assertFalse((state.ROOT / 'operation.json').exists())
        with self.assertRaises(ValueError): upgrade.rollback('0.16.0')
        for invalid in ['0.17.0-beta', '../0.17.0', '01.17.0', '0.17.0\n', '0.17']:
            with self.assertRaises(ValueError): state.version(invalid)

    def test_replay_busy_and_stale_version_do_not_launch_another_operation(self):
        self.cache(); first = agent.submit(self.request(), launch=False)
        self.assertEqual(agent.submit(self.request(), launch=False), first)
        second = self.request(); second['request_id'] = '22222222-2222-4333-a444-555555555555'
        with self.assertRaises(ValueError): agent.submit(second, launch=False)
        agent.progress('failed'); second['expected_version'] = '0.17.0'
        with self.assertRaises(ValueError): agent.submit(second, launch=False)
        self.assertEqual(state.read('operation.json')['request_id'], first['request_id'])

    def test_catalog_filters_unstable_and_mismatched_assets_and_preserves_offline_cache(self):
        def release(v, **extra):
            return dict(tag_name='v'+v, assets=[{'name': agent.asset_name(v, 'lite'), 'state': 'uploaded'}, {'name':'SHA256SUMS','state':'uploaded'}], **extra)
        data = [release('0.19.0'), release('0.20.0', prerelease=True), release('0.21.0', draft=True), {'tag_name': 'v0.22.0', 'assets': []}]
        with patch.object(agent, 'download', side_effect=lambda u,p,m:p.write_text(json.dumps(data))):
            self.assertEqual([r['version'] for r in agent.catalog()['releases']], ['0.19.0'])
        saved = state.read('catalog.json'); saved['checked_at'] = 1; state.write('catalog.json', saved)
        with patch.object(agent, 'download', side_effect=OSError('private transport details')):
            cached = agent.catalog(); self.assertEqual(cached['releases'][0]['version'], '0.19.0'); self.assertIn('warning', cached)
            self.assertNotIn('private', cached['warning'])

    def test_download_origin_restricted(self):
        for url in ['http://github.com/a', 'https://github.com.attacker.test/a', 'https://user@github.com/a', 'https://127.0.0.1/a', 'https://github.com:444/a']:
            with self.assertRaises(ValueError): agent.trusted_url(url)
        agent.trusted_url('https://release-assets.githubusercontent.com/a?signature=public-fixture')

    def archive(self, entries):
        path = self.root / 'test.tar.gz'
        with tarfile.open(path, 'w:gz') as tar:
            for name, kind, content in entries:
                info = tarfile.TarInfo(name); info.type = kind
                if kind == tarfile.REGTYPE: info.size = len(content)
                else: info.linkname = content.decode()
                tar.addfile(info, io.BytesIO(content) if kind == tarfile.REGTYPE else None)
        return path

    def test_archive_rejects_traversal_links_and_duplicate_overwrites(self):
        for entries in [[('../outside', tarfile.REGTYPE, b'x')], [('release/link', tarfile.SYMTYPE, b'/etc/passwd')], [('release/link', tarfile.LNKTYPE, b'release/file')], [('release/a',tarfile.REGTYPE,b'a'),('release/a',tarfile.REGTYPE,b'b')]]:
            with self.assertRaises(ValueError): agent.extract(self.archive(entries), self.root / 'out', 'release')
            shutil.rmtree(self.root/'out', ignore_errors=True)
        self.assertFalse((self.root/'outside').exists())

    def test_outer_checksum_rejected_before_extract(self):
        def fetch(url,path,maximum):
            if path.name=='SHA256SUMS':path.write_text('0'*64+'  '+agent.asset_name('0.19.0','lite')+'\n')
            else:path.write_bytes(b'corrupt')
        with patch.object(agent,'download',side_effect=fetch),patch.object(agent,'extract') as extract:
            with self.assertRaises(ValueError):agent.fetch_bundle('0.19.0','lite',self.root)
            extract.assert_not_called()

    def test_rollback_schema_gate_precedes_any_mutation(self):
        with patch.object(state,'candidates',return_value=[{'version':'0.17.0','compatible':False}]),patch.object(upgrade,'transaction') as mutation:
            with self.assertRaises(ValueError):upgrade.rollback('0.17.0')
            mutation.assert_not_called()

    def test_manual_rollback_keeps_current_configuration_database_and_baseline(self):
        old = self.root/'backup';(old/'app').mkdir(parents=True);(old/'app/VERSION').write_text('0.17.0')
        (old/'app-checksums.json').write_text(json.dumps(upgrade.app_hashes(old/'app')))
        original = self.config.read_bytes(); database = (self.data/'panel.db').read_bytes()
        def run_change(change,target,config,progress):change(self.root/'newbackup')
        with patch.object(state,'candidates',return_value=[{'version':'0.17.0','compatible':True,'backup':str(old)}]),patch.object(upgrade,'transaction',side_effect=run_change),patch.object(upgrade,'restore_units'):
            upgrade.rollback('0.17.0')
        self.assertEqual(self.config.read_bytes(),original);self.assertEqual((self.data/'panel.db').read_bytes(),database);self.assertEqual(state.baseline()['version'],'0.17.0')

    def test_interrupted_operation_recovers_from_root_journal(self):
        state.write('operation.json',dict(self.request(),stage='installing'))
        state.write('transaction.json',{'backup':'/root/guangyue-backups/upgrade-fixture','target':'0.19.0','phase':'changing'})
        from contextlib import nullcontext
        with patch.object(agent,'deployment_lock',return_value=nullcontext()),patch.object(upgrade,'restore_current') as restore:
            agent.recover();restore.assert_called_once_with(Path('/root/guangyue-backups/upgrade-fixture'))
        self.assertIsNone(state.read('transaction.json'));self.assertEqual(state.read('operation.json')['stage'],'failed');self.assertEqual(state.baseline()['version'],'0.17.0')

    def test_symlink_cannot_redirect_root_state_write(self):
        victim=self.root/'victim';victim.write_text('original');(state.ROOT/'operation.json.new').symlink_to(victim)
        with self.assertRaises(OSError):state.write('operation.json',{})
        self.assertEqual(victim.read_text(),'original')
