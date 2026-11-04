"""Main-area migration preserves original work and SSH as native surfaces."""
import copy
from pathlib import Path
import tempfile
import unittest
import uuid
from unittest.mock import patch

from rawr.project import Projects, recipe, split
from rawr.rex import blocks
from rawr.services import exe_shell_argv
from rawr.state import Error, Store
from rawr.vm_main import migrate_main_slot
from test_vm_switch import SwapRex


class PromotionRex(SwapRex):
    def __init__(self):
        super().__init__()
        self.closed = []
        self.change_placeholder_label = False

    def call(self, method, **args):
        if method == 'block.close':
            self.calls.append((method, copy.deepcopy(args)))
            bid = args['block_id']
            surface = self.surfaces[bid]
            # The fixture exposes a close operation ONLY for the known inert
            # placeholder. Any attempt to close existing work fails the test.
            assert surface['label'].startswith('rawr:') and surface['label'].endswith(':main-promotion')
            assert self.processes[bid]['child']['path'] == '/usr/bin/true'
            self.closed.append(bid)
            view = self.data[args['session_id']]
            for window in view['windows']:
                for layer in window['layers']:
                    layer['layout'] = self._remove(layer['layout'], bid)
                window['layers'] = [layer for layer in window['layers'] if layer['layout'] is not None]
            view['windows'] = [window for window in view['windows'] if window['layers']]
            self.processes.pop(bid)
            self.surfaces.pop(bid)
            view['revision'] += 1
            return {'revision': view['revision']}
        result = super().call(method, **args)
        if method == 'session.swap_blocks' and self.change_placeholder_label and self.swap_count == 2:
            placeholder = next(bid for bid, block in self.surfaces.items()
                               if block['label'].endswith(':main-promotion'))
            self.surfaces[placeholder]['label'] = 'somebody else'
        return result


class VMMainTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory(prefix='rawr-main-λ ')
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.store = Store(self.root/'config', self.root/'runtime')
        self.rex = PromotionRex()
        self.manager = Projects(self.store, self.rex)

    def fixture(self):
        project = self.manager.create('Main VM', self.root, tools=('files', 'exedev'), vm='one')
        slot = 'vm-' + uuid.uuid5(uuid.NAMESPACE_URL, 'one').hex[:12]
        project['roles'][slot] = recipe('vm-shell', self.root, exe_shell_argv({'vm_name':'one'}))
        project['setup'] = [{'name':'Workspace', 'layout':split('horizontal',.2,{'role':'files'},
            split('horizontal',.7,split('vertical',.6,{'role':'work-main'},{'role':slot}),{'role':'exedev'}))}]
        project['revision'] += 1
        with self.store.lock(project['id']):
            self.store.write(project, 0)
        sid = self.manager.open(project['id'])['session_id']
        runtime = self.store.observed(project['id'])
        runtime.update(vm_slot_role=slot, vm_slot_name='one')
        self.store.record(project['id'], runtime)
        return project, sid, slot, runtime

    def test_promotes_existing_vm_and_archives_original_work_without_relaunch(self):
        project, sid, slot, before = self.fixture()
        work = before['roles']['work-main']['block_id']
        vm = before['roles'][slot]['block_id']
        pids = {bid: process['child']['pid'] for bid, process in self.rex.processes.items()}
        result = migrate_main_slot(self.manager, project['id'])
        self.assertTrue(result['migrated'])
        runtime = self.store.observed(project['id'])
        saved = self.store.resolve(project['id'])
        self.assertEqual(runtime['vm_slot_role'], 'work-main')
        self.assertEqual(runtime['roles']['work-main']['block_id'], vm)
        self.assertNotIn(slot, runtime['roles'])
        self.assertNotIn(slot, saved['roles'])
        self.assertEqual(saved['roles']['work-main']['kind'], 'vm-shell')
        self.assertEqual(runtime['roles'][result['work_archive_role']]['block_id'], work)
        self.assertEqual(runtime['last_work'], vm)
        live = blocks(self.rex.view(sid))
        self.assertEqual(live[work]['window_id'], result['work_archive_window_id'])
        self.assertEqual({bid: process['child']['pid'] for bid, process in self.rex.processes.items()}, pids)
        self.assertEqual(len(self.rex.closed), 1)
        self.assertNotIn(self.rex.closed[0], pids)
        layout = saved['setup'][0]['layout']
        self.assertEqual(layout, split('horizontal', .2, {'role':'files'},
            split('horizontal', .7, {'role':'work-main'}, {'role':'exedev'})))
        self.assertNotIn('vm_main_pending', runtime)
        self.assertEqual(saved['revision'], project['revision'] + 1)
        self.assertEqual(runtime['roles']['exedev']['block_id'],
            next(w for w in self.rex.view(sid)['windows'] if w['window_id']==live[vm]['window_id'])['focused_block_id'])
        again = migrate_main_slot(self.manager, project['id'])
        self.assertFalse(again['migrated'])
        self.assertEqual(len(self.rex.closed), 1)

    def test_original_main_position_is_kept_when_vm_was_moved_elsewhere(self):
        project, sid, slot, runtime = self.fixture()
        work, vm = runtime['roles']['work-main']['block_id'], runtime['roles'][slot]['block_id']
        files = runtime['roles']['files']['block_id']
        # Current native layout is intentionally different from saved topology.
        self.rex.call('session.move_block', session_id=sid, block_id=vm,
                      anchor_block_id=files, direction='vertical', side='after', ratio=.4)
        result = migrate_main_slot(self.manager, project['id'])
        view = self.rex.view(sid)
        live = blocks(view)
        window = next(w for w in view['windows'] if w['window_id']==live[vm]['window_id'])
        # VM replaced the original main leaf; the old VM branch next to Files
        # was removed, leaving Files intact instead of taking the main position.
        root = window['layers'][0]['layout']['split']
        self.assertEqual(root['before']['block_id'], files)
        self.assertEqual(root['after']['split']['before']['block_id'], vm)
        self.assertEqual(live[work]['window_id'], result['work_archive_window_id'])

    def test_timeout_keeps_both_programs_and_blocks_blind_retry(self):
        project, sid, slot, runtime = self.fixture()
        pids = {bid: process['child']['pid'] for bid, process in self.rex.processes.items()}
        saved = (self.store.config/(project['id']+'.json')).read_bytes()
        self.rex.timeout_swap = True
        with self.assertRaisesRegex(Error, 'migration incomplete'):
            migrate_main_slot(self.manager, project['id'])
        self.assertEqual((self.store.config/(project['id']+'.json')).read_bytes(), saved)
        self.assertIn('vm_main_pending', self.store.observed(project['id']))
        counts = self.rex.new_window_count, self.rex.swap_count
        with self.assertRaisesRegex(Error, '(?i)pending|interrupted'):
            migrate_main_slot(self.manager, project['id'])
        self.assertEqual((self.rex.new_window_count, self.rex.swap_count), counts)
        for bid, pid in pids.items():
            self.assertEqual(self.rex.processes[bid]['child']['pid'], pid)

    def test_placeholder_ownership_change_never_closes_any_surface(self):
        project, sid, slot, runtime = self.fixture()
        self.rex.change_placeholder_label = True
        with self.assertRaisesRegex(Error, 'ownership changed'):
            migrate_main_slot(self.manager, project['id'])
        self.assertEqual(self.rex.closed, [])
        self.assertEqual(self.store.resolve(project['id'])['roles']['work-main']['kind'], 'shell')
        self.assertIn('vm_main_pending', self.store.observed(project['id']))

    def test_failed_state_commit_retains_valid_old_setup_and_pending_journal(self):
        project, sid, slot, runtime = self.fixture()
        saved = (self.store.config/(project['id']+'.json')).read_bytes()
        with patch.object(self.store, 'write', side_effect=Error('fixture disk conflict')):
            with self.assertRaisesRegex(Error, 'disk conflict'):
                migrate_main_slot(self.manager, project['id'])
        self.assertEqual((self.store.config/(project['id']+'.json')).read_bytes(), saved)
        self.assertIn('vm_main_pending', self.store.observed(project['id']))
        self.assertEqual(len(self.rex.closed), 1)
        for binding in runtime['roles'].values():
            self.assertIn(binding['block_id'], self.rex.processes)

    def test_without_live_vm_does_not_change_work_or_saved_setup(self):
        project = self.manager.create('No VM yet', self.root)
        self.manager.open(project['id'])
        saved = (self.store.config/(project['id']+'.json')).read_bytes()
        processes = copy.deepcopy(self.rex.processes)
        result = migrate_main_slot(self.manager, project['id'])
        self.assertFalse(result['migrated'])
        self.assertEqual((self.store.config/(project['id']+'.json')).read_bytes(), saved)
        self.assertEqual(self.rex.processes, processes)


if __name__ == '__main__':
    unittest.main()
