"""Host selection preserves the current native VM slot and archives live work."""
import copy
from concurrent.futures import ThreadPoolExecutor
import os
from pathlib import Path
import shutil
import tempfile
import unittest
import uuid

from rawr.project import Projects, recipe, split
from rawr.rex import blocks
from rawr.services import exe_shell_argv
from rawr.state import Error, Store
from test_project import FakeRex


class SwapRex(FakeRex):
    """Adds documented cross-tab APIs; there is still no send/kill/close API."""
    def __init__(self):
        super().__init__()
        self.new_window_count = 0
        self.swap_count = 0
        self.fail_window = False
        self.timeout_swap = False
        self.balance_on_new_windows = False
        self.client_balance_count = 0

    def settle_client(self):
        if self.pending_client_balance:
            self.client_balance_count += 1
        super().settle_client()

    def _surface(self, specification):
        result = super()._surface(specification)
        argv = specification.get('options', {}).get('command')
        if argv:
            child = self.processes[result['block_id']]['child']
            child['path'] = os.path.realpath(shutil.which(argv[0]) or argv[0])
            self.processes[result['block_id']]['foreground'] = copy.deepcopy(child)
        return result

    def _swap(self, node, first, second):
        if 'block_id' in node:
            if node['block_id'] == first:
                node['block_id'] = second
            elif node['block_id'] == second:
                node['block_id'] = first
            return
        self._swap(node['split']['before'], first, second)
        self._swap(node['split']['after'], first, second)

    def call(self, method, **args):
        if method not in ('session.new_window', 'session.swap_blocks', 'session.set_window_label', 'session.move_block'):
            return super().call(method, **args)
        self.calls.append((method, copy.deepcopy(args)))
        view = self.data[args['session_id']]
        if method == 'session.move_block':
            for window in view['windows']:
                for layer in window['layers']:
                    layer['layout'] = self._remove(layer['layout'], args['block_id'])
            # Native Rex removes an empty source window when a pane moves out.
            for window in view['windows']:
                window['layers'] = [l for l in window['layers'] if l['layout'] is not None]
            view['windows'] = [w for w in view['windows'] if w['layers']]
            for window in view['windows']:
                for layer in window['layers']:
                    layer['layout'] = self._insert(layer['layout'],args['anchor_block_id'],
                        {'block_id':args['block_id']},args['direction'],args['side'],args['ratio'])
            view['revision'] += 1
            return {'revision':view['revision']}
        if method == 'session.new_window':
            if self.fail_window:
                self.fail_window = False
                raise Error('Fixture target SSH launch failed')
            self.new_window_count += 1
            layout = self._layout(args['layout'])
            ids = self._ids(layout)
            wid = f'archive-window-{self.new_window_count}'
            window = {'window_id':wid, 'label':args.get('window_label','Window'),
                'focused_block_id':ids[0], 'layers':[{'kind':'tiled','layout':layout}]}
            view['windows'].append(window)
            if args.get('focus',True):
                view['active_window_id'] = wid
            view['revision'] += 1
            if self.balance_on_new_windows:
                self.pending_client_balance.add(args['session_id'])
            return {'window_id':wid, 'block_ids':ids, 'revision':view['revision']}
        if method == 'session.set_window_label':
            window = next(w for w in view['windows'] if w['window_id']==args['window_id'])
            window['label'] = args['label']
            view['revision'] += 1
            return {'revision':view['revision']}
        first, second = args['block_id'], args['other_block_id']
        placed = blocks(self.view(args['session_id']))
        if first not in placed or second not in placed or first==second:
            raise Error('Fixture swap requires two distinct visible native blocks')
        self.swap_count += 1
        active = next(w for w in view['windows'] if w['window_id']==view['active_window_id'])
        previous_focus = active.get('focused_block_id')
        for window in view['windows']:
            for layer in window['layers']:
                self._swap(layer['layout'],first,second)
            if window.get('focused_block_id') == first:
                window['focused_block_id'] = second
            elif window.get('focused_block_id') == second:
                window['focused_block_id'] = first
        if previous_focus in (first,second):
            destination = placed[second if previous_focus==first else first]['window_id']
            view['active_window_id'] = destination
            next(w for w in view['windows'] if w['window_id']==destination)['focused_block_id'] = previous_focus
        view['revision'] += 1
        # The Mac client can react to new_window after the native swap returns.
        # Deliver that queued balance before the caller's next observed view.
        if self.balance_on_new_windows:
            self.settle_client()
        if self.timeout_swap:
            self.timeout_swap = False
            raise Error('Rex timed out after successful native swap')
        return {'revision':view['revision']}


def shape(node):
    """Keep exact native split geometry while ignoring which block occupies it."""
    if node.get('block_id'):
        return 'slot'
    s=node['split']
    return s['direction'],s['ratio'],shape(s['before']),shape(s['after'])


class VMSwitchTests(unittest.TestCase):
    one = {'vm_name':'one','ssh_dest':'one.exe.xyz','status':'running'}
    two = {'vm_name':'two','ssh_dest':'vm+two@relay.example','status':'running'}

    def setUp(self):
        temp = tempfile.TemporaryDirectory(prefix='rawr-vm-switch-λ ')
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.store = Store(self.root/'config', self.root/'runtime')
        self.rex = SwapRex()
        self.projects = Projects(self.store,self.rex)

    def legacy_slot(self):
        """The previous release saved a per-host role in the bottom-center slot."""
        p=self.projects.create('VM project',self.root,tools=('exedev',),vm='one')
        role='vm-'+uuid.uuid5(uuid.NAMESPACE_URL,'one').hex[:12]
        p['roles'][role]=recipe('vm-shell',self.root,exe_shell_argv(self.one))
        p['setup']=[{'name':'Workspace','layout':split('horizontal',.75,
            split('vertical',.6,{'role':'work-main'},{'role':role}),{'role':'exedev'})}]
        p['revision']+=1
        with self.store.lock(p['id']):
            self.store.write(p,0)
        opened=self.projects.open(p['id'])
        runtime=self.store.observed(p['id'])
        self.rex.call('session.focus_block',session_id=opened['session_id'],block_id=runtime['roles']['exedev']['block_id'])
        return p,opened['session_id'],role,runtime['roles'][role]['block_id']

    def workspace(self,sid):
        return self.rex.view(sid)['windows'][0]

    def test_switch_replaces_exact_legacy_slot_and_archives_running_shell(self):
        p,sid,slot,old=self.legacy_slot()
        before=self.workspace(sid); old_pid=self.rex.process(sid,old)['child']['pid']
        result=self.projects.switch_vm(p['id'],self.two,focus=False)
        self.assertEqual(result['result'],'complete')
        runtime=self.store.observed(p['id']); current=runtime['roles'][slot]['block_id']
        self.assertEqual(runtime['vm_slot_role'],slot)
        self.assertEqual(runtime['vm_slot_name'],'two')
        self.assertNotEqual(current,old)
        self.assertEqual(shape(before['layers'][0]['layout']),shape(self.workspace(sid)['layers'][0]['layout']))
        self.assertEqual(self.workspace(sid)['focused_block_id'],runtime['roles']['exedev']['block_id'])
        live=blocks(self.rex.view(sid))
        self.assertEqual(live[current]['window_id'],before['window_id'])
        archive_role=runtime['vm_archives']['one']
        self.assertEqual(runtime['roles'][archive_role]['block_id'],old)
        self.assertEqual(live[old]['window_id'],runtime['vm_archive_windows'][archive_role])
        archive=next(w for w in self.rex.view(sid)['windows'] if w['window_id']==live[old]['window_id'])
        self.assertIn('one',archive['label'])
        self.assertEqual(self.rex.process(sid,old)['child']['pid'],old_pid)
        saved=self.store.resolve(p['id'])
        self.assertEqual(saved['setup'],p['setup'])
        self.assertEqual(saved['vm'],'two')
        self.assertEqual(saved['roles'][slot]['argv'][-1],'vm+two@relay.example')

    def test_switch_back_and_repeated_selection_reuse_original_processes(self):
        p,sid,slot,old=self.legacy_slot()
        old_pid=self.rex.process(sid,old)['child']['pid']
        self.projects.switch_vm(p['id'],self.two,focus=False)
        runtime=self.store.observed(p['id']); other=runtime['roles'][slot]['block_id']
        other_pid=self.rex.process(sid,other)['child']['pid']
        self.projects.switch_vm(p['id'],self.one,focus=False)
        runtime=self.store.observed(p['id'])
        self.assertEqual(runtime['roles'][slot]['block_id'],old)
        self.assertEqual(runtime['roles'][runtime['vm_archives']['two']]['block_id'],other)
        before=(self.rex.new_window_count,self.rex.swap_count,len(self.rex.processes))
        self.projects.switch_vm(p['id'],self.one,focus=False)
        self.assertEqual((self.rex.new_window_count,self.rex.swap_count,len(self.rex.processes)),before)
        self.assertEqual(self.rex.process(sid,old)['child']['pid'],old_pid)
        self.assertEqual(self.rex.process(sid,other)['child']['pid'],other_pid)
        self.assertEqual(self.rex.new_window_count,1)

    def test_native_autobalance_conflict_preserves_work_without_fighting_preference(self):
        p,sid,slot,old=self.legacy_slot()
        runtime=self.store.observed(p['id'])
        main=runtime['roles']['work-main']['block_id']
        picker=runtime['roles']['exedev']['block_id']
        extra=self.rex.add_extra(sid,picker)
        self.rex.call('session.resize_pane',session_id=sid,direction='horizontal',
            before_block_id=main,after_block_id=picker,ratio=.27)
        self.rex.call('session.resize_pane',session_id=sid,direction='vertical',
            before_block_id=main,after_block_id=old,ratio=.64)
        self.rex.call('session.resize_pane',session_id=sid,direction='vertical',
            before_block_id=picker,after_block_id=extra,ratio=.58)
        before=copy.deepcopy(self.workspace(sid))
        prior_pids={bid:process['child']['pid'] for bid,process in self.rex.processes.items()}
        saved=copy.deepcopy(self.store.resolve(p['id'])['setup'])
        self.rex.balance_on_new_windows=True
        call_start=len(self.rex.calls)
        with self.assertRaisesRegex(Error,'Autobalance splits'):
            self.projects.switch_vm(p['id'],self.two,focus=False)
        self.assertEqual(self.rex.client_balance_count,1)
        self.assertNotEqual(shape(self.workspace(sid)['layers'][0]['layout']),
            shape(before['layers'][0]['layout']))
        self.assertEqual(self.store.resolve(p['id'])['setup'],saved)
        self.assertEqual(self.store.resolve(p['id'])['vm'],'one')
        self.assertEqual({bid:self.rex.process(sid,bid)['child']['pid'] for bid in prior_pids},prior_pids)
        self.assertIn(extra,blocks(self.rex.view(sid)))
        self.assertNotIn('session.resize_pane',[method for method,_ in self.rex.calls[call_start:]])
        counts=(self.rex.new_window_count,self.rex.swap_count,len(self.rex.processes))
        with self.assertRaisesRegex(Error,'Interrupted VM switch'):
            self.projects.switch_vm(p['id'],self.two,focus=False)
        self.assertEqual((self.rex.new_window_count,self.rex.swap_count,len(self.rex.processes)),counts)

    def test_new_host_keeps_live_sizes_without_any_border_mutation(self):
        p,sid,slot,old=self.legacy_slot()
        runtime=self.store.observed(p['id'])
        main=runtime['roles']['work-main']['block_id']
        picker=runtime['roles']['exedev']['block_id']
        extra=self.rex.add_extra(sid,picker)
        self.rex.call('session.resize_pane',session_id=sid,direction='horizontal',
            before_block_id=main,after_block_id=picker,ratio=.27)
        self.rex.call('session.resize_pane',session_id=sid,direction='vertical',
            before_block_id=main,after_block_id=old,ratio=.64)
        self.rex.call('session.resize_pane',session_id=sid,direction='vertical',
            before_block_id=picker,after_block_id=extra,ratio=.58)
        before=copy.deepcopy(self.workspace(sid))
        prior_pids={bid:process['child']['pid'] for bid,process in self.rex.processes.items()}
        saved=copy.deepcopy(self.store.resolve(p['id'])['setup'])
        call_start=len(self.rex.calls)
        result=self.projects.switch_vm(p['id'],self.two,focus=False)
        self.assertEqual(result['result'],'complete')
        self.assertEqual(self.rex.new_window_count,1)
        self.assertNotIn('session.resize_pane',[method for method,_ in self.rex.calls[call_start:]])
        self.assertEqual(shape(self.workspace(sid)['layers'][0]['layout']),
            shape(before['layers'][0]['layout']))
        self.assertEqual(self.store.resolve(p['id'])['setup'],saved)
        self.assertEqual({bid:self.rex.process(sid,bid)['child']['pid'] for bid in prior_pids},prior_pids)
        self.assertIn(extra,blocks(self.rex.view(sid)))

    def test_cached_host_swap_does_not_resize_and_stays_stable_after_client_settles(self):
        p,sid,slot,old=self.legacy_slot()
        self.projects.switch_vm(p['id'],self.two,focus=False)
        runtime=self.store.observed(p['id'])
        current=runtime['roles'][slot]['block_id']
        main=runtime['roles']['work-main']['block_id']
        picker=runtime['roles']['exedev']['block_id']
        self.rex.call('session.resize_pane',session_id=sid,direction='horizontal',
            before_block_id=main,after_block_id=picker,ratio=.31)
        self.rex.call('session.resize_pane',session_id=sid,direction='vertical',
            before_block_id=main,after_block_id=current,ratio=.66)
        before=copy.deepcopy(self.workspace(sid))
        pids={bid:process['child']['pid'] for bid,process in self.rex.processes.items()}
        saved=copy.deepcopy(self.store.resolve(p['id'])['setup'])
        call_start=len(self.rex.calls)
        result=self.projects.switch_vm(p['id'],self.one,focus=False)
        self.assertEqual(result['result'],'complete')
        calls=[method for method,_ in self.rex.calls[call_start:]]
        self.assertIn('session.swap_blocks',calls)
        self.assertNotIn('session.new_window',calls)
        self.assertNotIn('session.resize_pane',calls)
        # Deliver any client work remaining after callback completion rather
        # than checking only the snapshot the callback itself verified.
        self.rex.settle_client()
        self.assertEqual(shape(self.workspace(sid)['layers'][0]['layout']),
            shape(before['layers'][0]['layout']))
        self.assertEqual({bid:self.rex.process(sid,bid)['child']['pid'] for bid in pids},pids)
        self.assertEqual(self.store.resolve(p['id'])['setup'],saved)
        self.assertEqual(self.store.observed(p['id'])['roles'][slot]['block_id'],old)

    def test_concurrent_same_host_selection_does_not_duplicate_shell_or_tab(self):
        p,sid,slot,old=self.legacy_slot()
        with ThreadPoolExecutor(max_workers=2) as pool:
            results=list(pool.map(lambda _:self.projects.switch_vm(p['id'],self.two,focus=False),range(2)))
        self.assertTrue(all(r['result']=='complete' for r in results))
        self.assertEqual(self.rex.new_window_count,1)
        self.assertEqual(self.rex.swap_count,1)
        self.assertEqual(len(self.rex.processes),4)
        runtime=self.store.observed(p['id'])
        self.assertEqual(runtime['vm_slot_name'],'two')
        self.assertEqual(runtime['roles'][runtime['vm_archives']['one']]['block_id'],old)

    def test_failed_target_launch_leaves_link_layout_and_current_shell_untouched(self):
        p,sid,slot,old=self.legacy_slot()
        before=self.rex.view(sid); processes=copy.deepcopy(self.rex.processes)
        self.rex.fail_window=True
        with self.assertRaisesRegex(Error,'launch failed'):
            self.projects.switch_vm(p['id'],self.two,focus=False)
        self.assertEqual(self.store.resolve(p['id'])['vm'],'one')
        self.assertEqual(self.store.resolve(p['id'])['setup'],p['setup'])
        self.assertEqual(self.rex.view(sid),before)
        self.assertEqual(self.rex.processes,processes)
        self.assertEqual(self.rex.swap_count,0)

    def test_timeout_after_swap_keeps_work_and_blocks_blind_retry(self):
        p,sid,slot,old=self.legacy_slot()
        old_pid=self.rex.process(sid,old)['child']['pid']
        self.rex.timeout_swap=True
        with self.assertRaises(Error):
            self.projects.switch_vm(p['id'],self.two,focus=False)
        before=(self.rex.new_window_count,self.rex.swap_count,len(self.rex.processes))
        with self.assertRaisesRegex(Error,'(?i)pending|ambiguous|interrupted|verify'):
            self.projects.switch_vm(p['id'],self.two,focus=False)
        self.assertEqual((self.rex.new_window_count,self.rex.swap_count,len(self.rex.processes)),before)
        self.assertEqual(self.rex.process(sid,old)['child']['pid'],old_pid)
        self.assertEqual(self.store.resolve(p['id'])['setup'],p['setup'])

    def test_archived_shells_survive_restore_without_relaunch(self):
        p,sid,slot,old=self.legacy_slot()
        self.projects.switch_vm(p['id'],self.two,focus=False)
        runtime=self.store.observed(p['id']); archive_role=runtime['vm_archives']['one']
        old_window=runtime['vm_archive_windows'][archive_role]
        pids={bid:process['child']['pid'] for bid,process in self.rex.processes.items()}
        result=self.projects.restore(p['id'])
        self.assertIn(result['result'],('complete','partial'))
        self.assertEqual(blocks(self.rex.view(sid))[old]['window_id'],old_window)
        self.assertEqual({bid:process['child']['pid'] for bid,process in self.rex.processes.items()},pids)
        self.assertEqual(self.store.resolve(p['id'])['setup'],p['setup'])
        runtime=self.store.observed(p['id'])
        good,issues,_=self.projects.classify(self.store.resolve(p['id']),runtime,self.rex.identity(),self.rex.view(sid))
        self.assertIn(slot,good)
        self.assertIn(archive_role,good)
        self.assertNotIn(slot,issues)

    def test_first_selected_host_replaces_main_and_archives_local_work(self):
        p=self.projects.create('VM project',self.root,tools=('exedev',))
        opened=self.projects.open(p['id'])
        before_runtime=self.store.observed(p['id'])
        old_main=before_runtime['roles']['work-main']['block_id']
        old_pid=self.rex.process(opened['session_id'],old_main)['child']['pid']
        before_shape=shape(self.workspace(opened['session_id'])['layers'][0]['layout'])
        self.projects.switch_vm(p['id'],self.two,focus=False)
        runtime=self.store.observed(p['id'])
        self.assertEqual(runtime['vm_slot_role'],'work-main')
        self.assertEqual(runtime['vm_slot_name'],'two')
        current=runtime['roles']['work-main']['block_id']
        self.assertNotEqual(current,old_main)
        self.assertIn(current,blocks(self.rex.view(opened['session_id'])))
        self.assertEqual(self.store.resolve(p['id'])['vm'],'two')
        self.assertEqual(self.store.resolve(p['id'])['setup'],p['setup'])
        self.assertEqual(shape(self.workspace(opened['session_id'])['layers'][0]['layout']),before_shape)
        self.assertEqual(len(self.rex.view(opened['session_id'])['windows']),2)
        live=blocks(self.rex.view(opened['session_id']))
        self.assertNotEqual(live[old_main]['window_id'],live[current]['window_id'])
        self.assertEqual(self.rex.process(opened['session_id'],old_main)['child']['pid'],old_pid)


if __name__=='__main__': unittest.main()
