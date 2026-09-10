import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import network
import update_state as state


class NetworkTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory(); self.addCleanup(temp.cleanup)
        root = Path(temp.name); config = root / 'config.json'
        config.write_text(json.dumps({'state_dir': str(root), 'unrelated': 'preserved'}))
        self.values = {'net.ipv4.tcp_congestion_control': 'cubic', 'net.core.default_qdisc': 'fq_codel',
                       'net.core.rmem_max': '4194304', 'net.core.wmem_max': '16777216',
                       'net.ipv4.tcp_available_congestion_control': 'reno cubic bbr'}
        self.initial = dict(self.values)
        for obj, key, value in [(state, 'ROOT', root / 'state'), (state, 'CONFIG', config),
                                (network, 'SYSCTL', root / 'sysctl.conf')]:
            p = patch.object(obj, key, value); p.start(); self.addCleanup(p.stop)
        p = patch.object(network, 'run', side_effect=self.run_command); p.start(); self.addCleanup(p.stop)

    def run_command(self, *args):
        if args[:2] == ('sysctl', '-n'): return self.values.get(args[2], '')
        if args[:2] == ('sysctl', '-w'):
            key, value = args[2].split('=', 1); self.values[key] = value; return ''
        raise AssertionError('Unexpected command')

    def test_independent_toggles_restore_and_preserve_larger_buffers(self):
        network.apply(True, True, restart=False)
        self.assertEqual(self.values['net.core.wmem_max'], '16777216')
        self.assertEqual(self.values['net.core.rmem_max'], '8388608')
        self.assertTrue(json.loads(state.CONFIG.read_text())['hy2_optimized'])
        network.apply(False, True, restart=False)
        self.assertEqual(self.values['net.ipv4.tcp_congestion_control'], 'cubic')
        self.assertEqual(self.values['net.core.default_qdisc'], 'fq_codel')
        network.apply(False, False, restart=False)
        self.assertEqual(self.values, self.initial)
        self.assertFalse(network.SYSCTL.exists())
        self.assertEqual(json.loads(state.CONFIG.read_text())['unrelated'], 'preserved')

    def test_does_not_clobber_external_changes_on_disable(self):
        network.apply(True, False, restart=False)
        self.values['net.core.default_qdisc'] = 'cake'
        network.apply(False, False, restart=False)
        self.assertEqual(self.values['net.core.default_qdisc'], 'cake')

    def test_failed_sysctl_rolls_back_both_profiles_and_files(self):
        def fail_once(*args):
            if args == ('sysctl', '-w', 'net.core.rmem_max=8388608'):
                raise OSError('private diagnostic')
            return self.run_command(*args)
        with patch.object(network, 'run', side_effect=fail_once):
            with self.assertRaises(OSError): network.apply(True, True, restart=False)
        self.assertEqual(self.values, self.initial)
        self.assertFalse(network.SYSCTL.exists())
        self.assertIsNone(state.read('network-transaction.json'))
        self.assertFalse(json.loads(state.CONFIG.read_text())['hy2_optimized'])

    def test_unsupported_bbr_refuses_before_any_write(self):
        self.values['net.ipv4.tcp_available_congestion_control'] = 'reno cubic'
        with patch.object(network, 'run', side_effect=lambda *args: '' if args[0]=='modprobe' else self.run_command(*args)):
            with self.assertRaisesRegex(ValueError, '不支持 BBR'): network.apply(True, True, restart=False)
        self.assertFalse(network.SYSCTL.exists())
        self.assertNotIn('hy2_optimized', json.loads(state.CONFIG.read_text()))

    def test_rejects_commands_unknown_keys_and_missing_booleans(self):
        for value in [{}, {'bbr':1,'hy2':True,'revision':'initial'}, {'bbr':True,'hy2':True,'revision':'initial','command':'id'}, {'bbr':True,'hy2':True,'revision':'../file'}]:
            with self.assertRaises(ValueError): network.validate(value)
        network.validate({'bbr':True,'hy2':False,'revision':'initial'})

    def test_install_rollback_removes_owned_file_and_restores_config(self):
        saved = network.apply(True, True, restart=False)
        network.restore(saved, restart=False)
        self.assertEqual(self.values, self.initial)
        self.assertFalse(network.SYSCTL.exists())
        self.assertIsNone(state.read('network.json'))


if __name__ == '__main__': unittest.main()
