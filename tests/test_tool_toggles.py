"""Native tool visibility preserves retained processes and deliberate geometry."""
import copy
from itertools import permutations
from pathlib import Path
import tempfile
import unittest

from rawr.project import Projects
from rawr.rex import blocks
from rawr.state import Error,Store
from test_vm_switch import SwapRex,shape


class ToggleRex(SwapRex):
    """Native detach removes empty containers but never the hosted process."""
    def __init__(self):
        super().__init__()
        self.fail_move_after_success=False

    def _compact(self,sid):
        view=self.data[sid]
        for window in view['windows']:
            window['layers']=[layer for layer in window['layers'] if layer.get('layout')]
            present=[bid for layer in window['layers'] for bid in self._ids(layer['layout'])]
            if window.get('focused_block_id') not in present:
                window['focused_block_id']=present[0] if present else None
        view['windows']=[window for window in view['windows'] if window['layers']]
        if view.get('active_window_id') not in {w['window_id'] for w in view['windows']}:
            view['active_window_id']=view['windows'][0]['window_id'] if view['windows'] else None

    def call(self,method,**args):
        if method=='session.detach_block':
            self.calls.append((method,copy.deepcopy(args)))
            sid=args['session_id'];view=self.data[sid]
            for window in view['windows']:
                for layer in window['layers']:
                    layer['layout']=self._remove(layer['layout'],args['block_id'])
            self._compact(sid);view['revision']+=1
            return {'revision':view['revision']}
        result=super().call(method,**args)
        if method=='session.new_window':
            self.session_blocks.setdefault(args['session_id'],set()).update(result['block_ids'])
        if method=='session.move_block' and self.fail_move_after_success:
            self.fail_move_after_success=False
            raise Error('Rex timed out after successful native move')
        return result

    def close_surface(self,sid,bid):
        super().close_surface(sid,bid)
        self._compact(sid)


class ToolToggleTests(unittest.TestCase):
    def setUp(self):
        temp=tempfile.TemporaryDirectory(prefix='rawr-toggle-雪 ')
        self.addCleanup(temp.cleanup)
        self.root=Path(temp.name)
        self.store=Store(self.root/'config',self.root/'runtime')
        self.rex=ToggleRex();self.projects=Projects(self.store,self.rex)

    def create(self):
        p=self.projects.create('same-name',self.root,tools=('files','github','exedev'))
        opened=self.projects.open(p['id'])
        return p,opened['session_id'],self.store.observed(p['id'])

    def saved(self,p):
        return (self.store.config/(p['id']+'.json')).read_bytes()

    def inventory(self,sid):
        return {b['block_id']:b for b in self.rex.call('session.list_blocks',session_id=sid)['blocks']}

    def test_each_tool_hide_show_retains_same_process_and_saved_bytes(self):
        p,sid,runtime=self.create();saved=self.saved(p)
        before=copy.deepcopy(self.rex.processes)
        original=shape(self.rex.view(sid)['windows'][0]['layers'][0]['layout'])
        for role in ('files','github','exedev'):
            with self.subTest(role=role):
                bid=runtime['roles'][role]['block_id']
                hidden=self.projects.toggle(p['id'],role)
                self.assertFalse(hidden['visible'])
                self.assertNotIn(bid,blocks(self.rex.view(sid)))
                self.assertEqual(self.inventory(sid)[bid]['placement'],'detached')
                shown=self.projects.toggle(p['id'],role)
                self.assertTrue(shown['visible']);self.assertTrue(shown['reused'])
                self.assertEqual(shown['block_id'],bid)
                self.assertEqual(shape(self.rex.view(sid)['windows'][0]['layers'][0]['layout']),original)
                self.assertEqual(self.rex.processes,before)
                self.assertEqual(self.saved(p),saved)
                self.assertEqual(len(self.rex.view(sid)['windows']),1)

    def test_all_tools_roundtrip_in_mixed_hide_and_show_orders(self):
        for hide_order in permutations(('files','github','exedev')):
            with self.subTest(hide_order=hide_order):
                p,sid,runtime=self.create();saved=self.saved(p)
                original=shape(self.rex.view(sid)['windows'][0]['layers'][0]['layout'])
                pids={b:self.rex.process(sid,b)['child']['pid'] for b in blocks(self.rex.view(sid))}
                for role in hide_order:self.projects.toggle(p['id'],role)
                self.assertEqual(set(blocks(self.rex.view(sid))),{runtime['roles']['work-main']['block_id']})
                for role in ('github','files','exedev'):self.projects.toggle(p['id'],role)
                self.assertEqual(shape(self.rex.view(sid)['windows'][0]['layers'][0]['layout']),original)
                self.assertEqual({b:self.rex.process(sid,b)['child']['pid'] for b in pids},pids)
                self.assertEqual(self.saved(p),saved)

    def test_show_preserves_live_resize_and_new_extra_work_while_hidden(self):
        p,sid,runtime=self.create();saved=self.saved(p)
        self.projects.toggle(p['id'],'files')
        main=runtime['roles']['work-main']['block_id'];github=runtime['roles']['github']['block_id']
        extra=self.rex.add_extra(sid,main)
        self.rex.call('session.resize_pane',session_id=sid,direction='horizontal',
            before_block_id=main,after_block_id=github,ratio=.63)
        live_before=shape(self.rex.view(sid)['windows'][0]['layers'][0]['layout'])
        prior=copy.deepcopy(self.rex.processes)
        self.projects.toggle(p['id'],'files')
        after=self.rex.view(sid)['windows'][0]['layers'][0]['layout']['split']
        self.assertEqual(after['ratio'],.2)
        self.assertEqual(shape(after['after']),live_before)
        self.assertIn(extra,blocks(self.rex.view(sid)))
        self.assertEqual(self.rex.processes,prior)
        self.assertEqual(self.saved(p),saved)

    def test_later_hide_remembers_native_resize_with_another_tool_hidden(self):
        p,sid,runtime=self.create();saved=self.saved(p)
        github=runtime['roles']['github']['block_id']
        exedev=runtime['roles']['exedev']['block_id']
        self.projects.toggle(p['id'],'files')
        self.rex.call('session.resize_pane',session_id=sid,direction='vertical',
            before_block_id=github,after_block_id=exedev,ratio=.73)
        changed=shape(self.rex.view(sid)['windows'][0]['layers'][0]['layout'])
        self.projects.toggle(p['id'],'github')
        self.projects.toggle(p['id'],'github')
        self.assertEqual(shape(self.rex.view(sid)['windows'][0]['layers'][0]['layout']),changed)
        self.projects.toggle(p['id'],'files')
        after=self.rex.view(sid)['windows'][0]['layers'][0]['layout']['split']
        self.assertEqual(after['ratio'],.2)
        self.assertEqual(shape(after['after']),changed)
        self.assertEqual(self.saved(p),saved)

    def test_open_tool_and_restore_rediscover_hidden_tools_without_duplicates(self):
        p,sid,runtime=self.create();programs=copy.deepcopy(self.rex.processes)
        self.projects.toggle(p['id'],'files')
        opened=self.projects.tool(p['id'],'files')
        self.assertFalse(opened['created'])
        self.assertEqual(opened['block_id'],runtime['roles']['files']['block_id'])
        for role in ('github','exedev'):self.projects.toggle(p['id'],role)
        result=self.projects.restore(p['id'])
        self.assertEqual(result['result'],'complete')
        self.assertEqual(self.rex.processes,programs)
        self.assertEqual(len(blocks(self.rex.view(sid))),4)
        self.assertFalse(self.store.observed(p['id']).get('hidden_tools'))

    def test_show_follows_stable_main_role_after_vm_replaces_local_work(self):
        p,sid,runtime=self.create()
        original=shape(self.rex.view(sid)['windows'][0]['layers'][0]['layout'])
        files=runtime['roles']['files']['block_id']
        old_main=runtime['roles']['work-main']['block_id']
        original_pids={b:self.rex.process(sid,b)['child']['pid'] for b in blocks(self.rex.view(sid))}
        self.projects.toggle(p['id'],'files')
        self.projects.switch_vm(p['id'],{'vm_name':'selected','ssh_dest':'selected.exe.xyz'})
        switched=self.store.observed(p['id'])
        self.assertEqual(switched['vm_slot_role'],'work-main')
        self.assertNotEqual(switched['roles']['work-main']['block_id'],old_main)
        shown=self.projects.toggle(p['id'],'files')
        self.assertEqual(shown['block_id'],files)
        self.assertEqual(shape(self.rex.view(sid)['windows'][0]['layers'][0]['layout']),original)
        self.assertEqual({b:self.rex.process(sid,b)['child']['pid'] for b in original_pids},original_pids)
        self.assertEqual(self.store.resolve(p['id'])['setup'],p['setup'])

    def test_changed_tool_program_is_never_hidden_or_replaced(self):
        p,sid,runtime=self.create();bid=runtime['roles']['files']['block_id']
        process=self.rex.processes[bid]
        process['child'].update(path='/bin/sleep',name='sleep')
        process['foreground']=copy.deepcopy(process['child'])
        before=self.rex.view(sid);programs=copy.deepcopy(self.rex.processes)
        with self.assertRaisesRegex(Error,'unverified|preserved'):
            self.projects.toggle(p['id'],'files')
        self.assertEqual(self.rex.view(sid),before)
        self.assertEqual(self.rex.processes,programs)

    def test_hidden_changed_program_or_host_is_preserved(self):
        for change in ('program','host'):
            with self.subTest(change=change):
                p,sid,runtime=self.create();bid=runtime['roles']['github']['block_id']
                self.projects.toggle(p['id'],'github')
                if change=='host':self.rex.surfaces[bid]['host_id']='foreign-host'
                else:
                    self.rex.processes[bid]['child'].update(path='/bin/sleep',name='sleep')
                    self.rex.processes[bid]['foreground']=copy.deepcopy(self.rex.processes[bid]['child'])
                count=len(self.rex.processes)
                with self.assertRaisesRegex(Error,'preserved|reused'):
                    self.projects.toggle(p['id'],'github')
                self.assertEqual(len(self.rex.processes),count)
                self.assertEqual(self.inventory(sid)[bid]['placement'],'detached')

    def test_unrecognized_detached_role_is_never_relaunched(self):
        for operation in ('toggle','tool','restore'):
            with self.subTest(operation=operation):
                p,sid,runtime=self.create();bid=runtime['roles']['files']['block_id']
                self.rex.call('session.detach_block',session_id=sid,block_id=bid)
                programs=copy.deepcopy(self.rex.processes)
                if operation=='restore':
                    result=self.projects.restore(p['id'])
                    self.assertIn(result['result'],('partial','failed'))
                else:
                    with self.assertRaisesRegex(Error,'detached|preserved|inspect'):
                        getattr(self.projects,operation)(p['id'],'files')
                self.assertEqual(self.rex.processes,programs)
                self.assertEqual(self.inventory(sid)[bid]['placement'],'detached')

    def test_interrupted_show_journal_blocks_blind_retry(self):
        p,sid,runtime=self.create();self.projects.toggle(p['id'],'files')
        prior=copy.deepcopy(self.rex.processes)
        self.rex.fail_move_after_success=True
        with self.assertRaisesRegex(Error,'timed out'):
            self.projects.toggle(p['id'],'files')
        self.assertTrue(self.store.observed(p['id']).get('tool_toggle_pending'))
        mutations=len([m for m,_ in self.rex.calls if m in ('session.new_split','session.new_window','session.move_block','session.detach_block')])
        for operation in ('toggle','tool','restore'):
            with self.assertRaisesRegex(Error,'Interrupted|preserved'):
                getattr(self.projects,operation)(p['id'],*(() if operation=='restore' else ('files',)))
        self.assertEqual(len([m for m,_ in self.rex.calls if m in ('session.new_split','session.new_window','session.move_block','session.detach_block')]),mutations)
        self.assertEqual(self.rex.processes,prior)

    def test_session_dispatch_uses_exact_unique_association(self):
        p1,sid1,_=self.create();p2,sid2,_=self.create()
        self.assertNotEqual(sid1,sid2)
        self.assertEqual(self.projects.project_for_session(sid1),p1['id'])
        self.assertEqual(self.projects.project_for_session(sid2),p2['id'])
        with self.assertRaisesRegex(Error,'unique project association'):
            self.projects.project_for_session('unknown-session')
        runtime=self.store.observed(p2['id']);runtime['session_id']=sid1
        self.store.record(p2['id'],runtime)
        with self.assertRaisesRegex(Error,'unique project association'):
            self.projects.project_for_session(sid1)


if __name__=='__main__':unittest.main()
