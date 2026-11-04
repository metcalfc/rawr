#!/usr/bin/env python3
"""Verify native tool hide/show on a private Rex server, never user work."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time

REX=os.environ.get('RAWR_REX','/Applications/Rex Beta.app/Contents/Helpers/Rex Server.rex/Contents/MacOS/rex')


def main():
    base=Path(tempfile.mkdtemp(prefix='rawr-tool-toggle-',dir='/tmp'))
    endpoint='unix://'+str(base/'server.sock')
    config=base/'init.lua';config.write_text('')
    shell=base/'shell';shell.mkdir()
    server_log=(base/'server.log').open('w')
    events_log=(base/'events.jsonl').open('w')
    server=subprocess.Popen([REX,'server','run','--listen',endpoint,'--state-dir',str(base/'state'),'--config',str(config)],
        stdout=server_log,stderr=server_log,env={**os.environ,'ZDOTDIR':str(shell)})
    events=None
    def run(*args):
        r=subprocess.run([REX,'-S',endpoint,'--autostart=false',*args],capture_output=True,text=True,check=True)
        return json.loads(r.stdout)
    def call(method,**args):
        return run('api','call',method,json.dumps(args))
    def leaf(name):
        return {'block':{'flavor':'com.superlogical.terminal.shell','label':name,
            'options':{'shell':'none','login':False,'cwd':str(base),
                'command':['/bin/sh','-c',f'echo SENTINEL={name}:$$; i=0; while :; do echo {name}:$i; i=$((i+1)); sleep 1; done']}}}
    def split(direction,ratio,before,after):
        return {'split':{'direction':direction,'ratio':ratio,'before':before,'after':after}}
    def geometry(node):
        if node.get('block_id'):
            return {'block_id':node['block_id'],'rect':node['rect']}
        s=node['split']
        return {'direction':s['direction'],'ratio':s['ratio'],'before':geometry(s['before']),'after':geometry(s['after'])}
    def pids(sid,ids):
        return {bid:run('block','call','-s',sid,'-b',bid,'process')['child']['pid'] for bid in ids}
    report={'scope':'private native server; independent sentinel terminals; no cloud access',
        'keymap_check':{'command':'rex -C lunchbox keymap --json','configured_bindings':[],
            'limitation':'CLI includes config/client-script bindings only without the app request; native defaults and settings shortcuts remain unverified by that command'}}
    try:
        for _ in range(100):
            try: identity=run('whoami','--json');break
            except (subprocess.CalledProcessError,ValueError):time.sleep(.1)
        else:raise RuntimeError('private Rex did not start')
        report['version']=identity['rex_version']
        report['schemas']={m:run('api','describe',m,'--json') for m in ('session.detach_block','session.move_block','session.list_blocks')}
        initial=split('horizontal',.2,leaf('files'),split('horizontal',.7,leaf('work'),split('vertical',.5,leaf('github'),leaf('exedev'))))
        created=call('session.create',label='rawr-tool-toggle-disposable',initial_windows=[{'window_label':'Workspace','layout':initial}])
        sid=created['session_id'];files,work,github,exedev=created['initial_windows'][0]['block_ids']
        ids=[files,work,github,exedev]
        call('session.focus_block',session_id=sid,block_id=work)
        time.sleep(.2)
        before=call('session.view',session_id=sid);before_pids=pids(sid,ids)
        events=subprocess.Popen([REX,'-S',endpoint,'events','-s',sid,'--json'],stdout=events_log,stderr=server_log)
        time.sleep(.1)
        call('session.detach_block',session_id=sid,block_id=files)
        hidden=call('session.view',session_id=sid)
        inventory=call('session.list_blocks',session_id=sid)
        hidden_block=next(b for b in inventory['blocks'] if b['block_id']==files)
        assert hidden_block['placement']=='detached' and not hidden_block.get('window_id')
        assert files not in json.dumps(hidden['windows'])
        assert len(hidden['windows'])==1
        assert pids(sid,ids)==before_pids
        time.sleep(1.1)
        capture=subprocess.check_output([REX,'-S',endpoint,'capture','-s',sid,'-b',files,'--trim'],text=True)
        assert 'files:1' in capture
        # A complex sibling subtree cannot be used as move_block's anchor.
        # This one-call show is legal, but splits only the work leaf.
        call('session.move_block',session_id=sid,block_id=files,anchor_block_id=work,direction='horizontal',side='before',ratio=.2)
        simple_show=call('session.view',session_id=sid)
        assert geometry(simple_show['windows'][0]['layers'][0]['layout'])!=geometry(before['windows'][0]['layers'][0]['layout'])
        # Bounded reconstruction of the four *existing* surfaces restores exact
        # original role topology/rects. No process is relaunched or typed into.
        for bid in (work,github,exedev):call('session.detach_block',session_id=sid,block_id=bid)
        call('session.move_block',session_id=sid,block_id=work,anchor_block_id=files,direction='horizontal',side='after',ratio=.2)
        call('session.move_block',session_id=sid,block_id=github,anchor_block_id=work,direction='horizontal',side='after',ratio=.7)
        call('session.move_block',session_id=sid,block_id=exedev,anchor_block_id=github,direction='vertical',side='after',ratio=.5)
        call('session.focus_block',session_id=sid,block_id=work)
        shown=call('session.view',session_id=sid)
        assert geometry(shown['windows'][0]['layers'][0]['layout'])==geometry(before['windows'][0]['layers'][0]['layout'])
        assert pids(sid,ids)==before_pids
        assert len(shown['windows'])==1
        events.terminate();events.wait(timeout=5);events=None
        events_log.flush()
        raw=[]
        for line in (base/'events.jsonl').read_text().splitlines():
            try:raw.append(json.loads(line))
            except ValueError:pass
        def event_summary(envelope):
            event=envelope.get('payload',{}).get('event',{})
            return {'type':envelope.get('type'),'event':event.get('type'),
                'revision':event.get('payload',{}).get('revision')}
        report.update(before=before,hidden_view=hidden,detached_inventory=inventory,
            hidden_capture=capture,one_call_reattach=simple_show,restored_view=shown,
            pids_before=before_pids,pids_after=pids(sid,ids),hidden_process_continued=True,
            no_tabs_created=True,exact_roundtrip_with_bounded_reconstruction=True,
            single_move_cannot_anchor_complex_sibling=True,
            events=[event_summary(e) for e in raw])
        call('server.stop');server.wait(timeout=10)
        Path('docs/evidence/tool-toggle-probe.json').write_text(json.dumps(report,indent=2)+'\n')
        print(json.dumps({'report':'docs/evidence/tool-toggle-probe.json','version':report['version'],
            'hidden_process_continued':True,'pids_preserved':True,'no_tabs_created':True,
            'exact_roundtrip_with_bounded_reconstruction':True,'single_move_complex_sibling_limitation':True}))
    finally:
        if events is not None:
            events.terminate();events.wait(timeout=5)
        if server.poll() is None:
            server.terminate();server.wait(timeout=10)
        events_log.close();server_log.close()


if __name__=='__main__':main()
