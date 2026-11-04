#!/usr/bin/env python3
"""Verify VM swaps with the real Mac client and Autobalance splits OFF.

Creates/closes only its own disposable session on the normal server, briefly
shows it in the connected app, then returns to the walkthrough session. Local
cat processes stand in for SSH; no cloud resource is contacted or changed.
"""
import json
from pathlib import Path
import sys
import tempfile
import time
from unittest.mock import patch

sys.path.insert(0,str(Path(__file__).resolve().parents[1]))
from rawr.project import Projects,recipe,split
from rawr.rex import Rex
from rawr.state import Store


def shape(node):
    if node.get('block_id'):
        return 'pane'
    s=node['split']
    return [s['direction'],s['ratio'],shape(s['before']),shape(s['after'])]


def main():
    original_store=Store('.acceptance/config','.acceptance/runtime')
    original=original_store.observed(Path('.acceptance/demo-id').read_text().strip())
    rex=Rex();identity=rex.identity()
    base=Path(tempfile.mkdtemp(prefix='rawr-size-check-',dir='/tmp'))
    store=Store(base/'config',base/'runtime');app=Projects(store,rex)
    p=app.create('rawr-size-check-disposable',base,vm='one')
    for role in ('work-main','files','github','exedev','vm-shell'):
        p['roles'][role]=recipe('vm-shell' if role=='vm-shell' else 'command',base,['/bin/cat'])
    p['roles']['vm-shell']['vm_name']='one'
    p['setup']=[{'name':'Size check','layout':split('horizontal',.18,{'role':'files'},
        split('horizontal',.73,split('vertical',.62,{'role':'work-main'},{'role':'vm-shell'}),
            split('vertical',.47,{'role':'github'},{'role':'exedev'})))}]
    p['revision']+=1
    with store.lock(p['id']):
        store.write(p,0)
    sid=None
    calls=[]
    native_call=rex.call
    def call(method,**args):
        calls.append(method)
        return native_call(method,**args)
    report={'rex_version':identity['rex_version'],'scope':'Connected Mac client; disposable session; local cat fixtures, no provider calls',
        'required_native_setting':'Autobalance splits OFF; verified through native Settings UI','samples':[]}
    try:
        sid=app.open(p['id'])['session_id']
        time.sleep(.5)
        initial=rex.view(sid);expected=shape(initial['windows'][0]['layers'][0]['layout'])
        assert expected==['horizontal',.18,'pane',['horizontal',.73,['vertical',.62,'pane','pane'],['vertical',.47,'pane','pane']]],expected
        old_pids={role:rex.process(sid,b['block_id'])['child']['pid'] for role,b in store.observed(p['id'])['roles'].items()}
        rex.call=call
        def samples(phase):
            began=time.monotonic()
            for target in (0,.2,.7,1.5,3):
                time.sleep(max(0,target-(time.monotonic()-began)))
                v=rex.view(sid)
                actual=shape(v['windows'][0]['layers'][0]['layout'])
                report['samples'].append({'phase':phase,'seconds_after_return':round(time.monotonic()-began,3),'shape':actual,'revision':v['revision']})
                assert actual==expected,(phase,actual)
        with patch('rawr.services.exe_shell_argv',return_value=['/bin/cat']):
            report['new_host']=app.switch_vm(p['id'],{'vm_name':'two'})
            assert report['new_host']['created']
            samples('new-host')
            report['cached_host']=app.switch_vm(p['id'],{'vm_name':'one'})
            assert not report['cached_host']['created']
            samples('cached-host')
        current=store.observed(p['id'])
        for role,binding in current['roles'].items():
            if role in old_pids:
                assert rex.process(sid,binding['block_id'])['child']['pid']==old_pids[role]
        assert 'session.resize_pane' not in calls
        report.update(all_sizes_preserved=True,no_resize_calls=True,original_processes_preserved=True,
            saved_setup_unchanged=store.resolve(p['id'])['setup']==p['setup'])
        Path('docs/evidence/vm-size-fix.json').write_text(json.dumps(report,indent=2)+'\n')
        print(json.dumps({'report':'docs/evidence/vm-size-fix.json','all_sizes_preserved':True,'no_resize_calls':True,'original_processes_preserved':True}))
    finally:
        rex.call=native_call
        rex.select(original['session_id'],original['roles']['exedev']['block_id'])
        if sid:
            rex.call('session.destroy',session_id=sid)


if __name__=='__main__':
    main()
