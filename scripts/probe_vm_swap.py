#!/usr/bin/env python3
"""Verify VM-slot cross-tab swapping on a private Rex server, never user work."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time

REX = os.environ.get('RAWR_REX', '/Applications/Rex Beta.app/Contents/Helpers/Rex Server.rex/Contents/MacOS/rex')


def main():
    base = Path(tempfile.mkdtemp(prefix='rawr-vm-swap-', dir='/tmp'))
    endpoint = 'unix://' + str(base / 'server.sock')
    config = base / 'init.lua'; config.write_text('')
    shell_config = base / 'shell'; shell_config.mkdir()
    log = (base / 'server.log').open('w')
    server = subprocess.Popen([REX, 'server', 'run', '--listen', endpoint,
        '--state-dir', str(base/'state'), '--config', str(config)],
        stdout=log, stderr=log, env={**os.environ, 'ZDOTDIR':str(shell_config)})

    def run(*args):
        result = subprocess.run([REX, '-S', endpoint, '--autostart=false', *args],
            capture_output=True, text=True, check=True)
        return json.loads(result.stdout)

    def call(method, **args):
        return run('api', 'call', method, json.dumps(args))

    def leaf(label):
        return {'block':{'flavor':'com.superlogical.terminal.shell','label':label,
            'options':{'shell':'none','login':False,'cwd':str(base),
            'command':['/bin/sh','-c',f'echo SENTINEL={label}:$$; i=0; while :; do echo {label}:$i; i=$((i+1)); sleep 1; done']}}}

    def process(sid,bid):
        return run('block','call','-s',sid,'-b',bid,'process')

    def capture(sid,bid):
        return subprocess.check_output([REX,'-S',endpoint,'capture','-s',sid,'-b',bid,'--trim'],text=True)

    def block_window(view,bid):
        return next(w['window_id'] for w in view['windows'] for layer in w['layers']
                    for block in layer['blocks'] if block['block_id']==bid)

    def geometry(node):
        if node.get('block_id'):
            return {'rect':node['rect']}
        s=node['split']
        return {'rect':node['rect'],'split_id':s['split_id'],'direction':s['direction'],
                'ratio':s['ratio'],'before':geometry(s['before']),'after':geometry(s['after'])}

    def geometries(view):
        return {w['window_id']:[{'bounds':l['bounds'],'layout':geometry(l['layout'])} for l in w['layers']] for w in view['windows']}

    report={'scope':'private native server; sentinel shells stand in for SSH; no provider access'}
    try:
        for _ in range(100):
            try: identity=run('whoami','--json'); break
            except (subprocess.CalledProcessError,ValueError): time.sleep(.1)
        else: raise RuntimeError('private Rex failed to start')
        report['version']=identity['rex_version']
        report['schemas']={m:run('api','describe',m,'--json') for m in ('session.swap_blocks','session.new_window','session.set_window_label','session.view')}
        layout={'split':{'direction':'horizontal','ratio':.75,'before':{'split':{'direction':'vertical','ratio':.6,
            'before':leaf('work'),'after':leaf('vm-one')}},'after':leaf('exe-picker')}}
        created=call('session.create',label='rawr-vm-swap-disposable',initial_windows=[{'window_label':'Workspace','layout':layout}])
        sid=created['session_id']; workspace=created['initial_windows'][0]['window_id']
        work,old,picker=created['initial_windows'][0]['block_ids']
        new_tab=call('session.new_window',session_id=sid,window_label='Preparing vm-two',focus=False,layout=leaf('vm-two'))
        target=new_tab['block_ids'][0]; archive=new_tab['window_id']
        call('session.focus_block',session_id=sid,block_id=picker)
        time.sleep(.3)
        before=call('session.view',session_id=sid)
        processes_before={bid:process(sid,bid) for bid in (old,target,work,picker)}
        call('session.swap_blocks',session_id=sid,block_id=old,other_block_id=target)
        call('session.set_window_label',session_id=sid,window_id=archive,label='SSH · vm-one')
        after=call('session.view',session_id=sid)
        processes_after={bid:process(sid,bid) for bid in (old,target,work,picker)}
        assert block_window(after,target)==workspace
        assert block_window(after,old)==archive
        assert geometries(before)==geometries(after)
        assert before['active_window_id']==after['active_window_id']==workspace
        assert next(w for w in after['windows'] if w['window_id']==workspace)['focused_block_id']==picker
        assert all(processes_before[b]['child']['pid']==processes_after[b]['child']['pid'] for b in processes_before)
        report['picker_focused_switch']={'workspace_id':workspace,'archive_id':archive,'before':before,'after':after,
            'pids_before':{b:p['child']['pid'] for b,p in processes_before.items()},'pids_after':{b:p['child']['pid'] for b,p in processes_after.items()},
            'geometry_and_split_ids_preserved':True,'picker_focus_preserved':True}
        # Re-select the old VM from its archive. Same native blocks, no relaunch.
        call('session.swap_blocks',session_id=sid,block_id=old,other_block_id=target)
        call('session.set_window_label',session_id=sid,window_id=archive,label='SSH · vm-two')
        returned=call('session.view',session_id=sid)
        assert block_window(returned,old)==workspace and block_window(returned,target)==archive
        assert all(process(sid,b)['child']['pid']==processes_before[b]['child']['pid'] for b in processes_before)
        time.sleep(1.1)
        report['captures']={old:capture(sid,old),target:capture(sid,target)}
        assert all(':1' in text for text in report['captures'].values())
        report['round_trip_reuses_original_pids']=True
        # If swapping a focused shell, Rex follows it into the archive. Caller
        # can deliberately return focus to the unchanged picker surface.
        call('session.focus_block',session_id=sid,block_id=old)
        call('session.swap_blocks',session_id=sid,block_id=old,other_block_id=target)
        focus_followed=call('session.view',session_id=sid)
        assert focus_followed['active_window_id']==archive
        call('session.focus_block',session_id=sid,block_id=picker)
        refocused=call('session.view',session_id=sid)
        assert refocused['active_window_id']==workspace
        report['focused_shell_swap_follows_shell_to_archive']=True
        report['explicit_picker_focus_restores_workspace']=True
        call('server.stop'); server.wait(timeout=10)
        Path('docs/evidence/vm-swap-probe.json').write_text(json.dumps(report,indent=2)+'\n')
        print(json.dumps({'report':'docs/evidence/vm-swap-probe.json','version':report['version'],
            'geometry_preserved':True,'pids_preserved':True,'round_trip_reuses_original_pids':True}))
    finally:
        if server.poll() is None:
            server.terminate(); server.wait(timeout=10)
        log.close()


if __name__=='__main__': main()
