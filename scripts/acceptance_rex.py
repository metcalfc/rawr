#!/usr/bin/env python3
"""Real Rex safety acceptance against a private disposable server.

Provider requests are deliberately replaced with read-only fixture programs.
No user's session or real VM lifecycle is touched.
"""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
sys.path.insert(0,str(Path(__file__).resolve().parents[1]))
from rawr.project import Projects, recipe
from rawr.rex import Rex, blocks
from rawr.state import Store


def main():
    base=Path(tempfile.mkdtemp(prefix='rawr-acceptance-',dir='/tmp'))
    endpoint='unix://'+str(base/'server.sock')
    executable=os.environ.get('RAWR_REX','/Applications/Rex Beta.app/Contents/Helpers/Rex Server.rex/Contents/MacOS/rex')
    config=base/'init.lua'; config.write_text('')
    shell_config=base/'shell-config'; shell_config.mkdir()
    (shell_config/'.zshrc').write_text("PROMPT='rawr-fixture% '\n")
    root=base/'project 雪 ; $(literal)'; root.mkdir()
    (root/'hello.txt').write_text('Project anchored preview\n')
    subprocess.run(['git','init','-q',str(root)],check=True)
    log=(base/'server.log').open('w')
    def start():
        return subprocess.Popen([executable,'server','run','--listen',endpoint,'--state-dir',str(base/'server-state'),'--config',str(config)],stdout=log,stderr=log,env={**os.environ,'ZDOTDIR':str(shell_config)})
    server=start(); rex=Rex(executable,endpoint); store=Store(base/'projects',base/'runtime'); app=Projects(store,rex)
    report={'isolated_directory':str(base),'provider_tests':'fixture suite only; no real cloud mutation','checks':{}}
    def record(name,value=True):
        report['checks'][name]=value
    try:
        for _ in range(100):
            try: identity=rex.identity(); break
            except Exception: time.sleep(.1)
        else: raise RuntimeError('isolated server failed')
        report['version']=identity['rex_version']
        p=app.create('acceptance',root,('files','github','exedev'),repository='metcalfc/herdr-exe',vm=None)
        # Explicit launch recipe, not inferred from an arbitrary process.
        with store.lock(p['id']):
            p['roles']['work-main']=recipe('command',root,['/bin/sh','-c','echo SENTINEL_PID=$$; i=0; while :; do echo sentinel:$i; i=$((i+1)); sleep 1; done'])
            p['revision']+=1; store.write(p,0)
        first=app.open(p['id']); sid=first['session_id']
        second=app.open(p['id']); assert sid==second['session_id'] and not second['created']
        assert len(rex.sessions())==1
        record('P01_open_twice_one_session')
        literals=['two words','雪','$(touch SHOULD_NOT_EXIST); `echo literal`']
        literal_result=rex.call('session.new_window',session_id=sid,focus=False,window_label='literal argv fixture',layout={'block':{'flavor':'com.superlogical.terminal.shell','options':{'shell':'none','login':False,'cwd':str(root),'command':[sys.executable,'-c','import os,sys,json; print(json.dumps([os.getcwd(),sys.argv[1:]],ensure_ascii=False))',*literals],'exit':{'on_completion':False}}}})
        literal_id=literal_result['block_ids'][0]
        time.sleep(.2)
        literal_capture=subprocess.check_output([executable,'-S',endpoint,'capture','-s',sid,'-b',literal_id,'--trim','--unwrap'],text=True)
        assert json.loads(literal_capture)==[str(root.resolve()),literals],literal_capture
        assert not (root/'SHOULD_NOT_EXIST').exists()
        rex.call('block.close',session_id=sid,block_id=literal_id)
        record('P04_literal_cwd_and_argv')
        time.sleep(1)
        status=app.plan(p['id']); report['initial_plan']=status
        assert not status['missing'],status
        runtime=store.observed(p['id']); main_id=runtime['roles']['work-main']['block_id']; files=runtime['roles']['files']['block_id']; github=runtime['roles']['github']['block_id']; exe=runtime['roles']['exedev']['block_id']
        before=rex.process(sid,main_id)
        screen=rex.run('api','call','completely.invalid','{}') if False else None
        capture_before=subprocess.check_output([executable,'-S',endpoint,'capture','-s',sid,'-b',main_id,'--trim'],text=True)
        saved=app.save(p['id']); record('save_revision',saved['revision'])
        saved_file=store.config/(p['id']+'.json'); saved_bytes=saved_file.read_bytes()
        rex.call('session.move_block',session_id=sid,block_id=files,anchor_block_id=main_id,direction='vertical',side='after',ratio=.65)
        changed=rex.view(sid)
        app.open(p['id']); assert rex.view(sid)['windows'][0]['layers']==changed['windows'][0]['layers']
        assert saved_file.read_bytes()==saved_bytes
        record('P02_S01_open_keeps_temporary_layout_and_saved_bytes')
        restored=app.restore(p['id']); report['clean_restore']=restored
        assert restored['result']=='complete',restored
        after=rex.process(sid,main_id); assert before['child']['pid']==after['child']['pid']
        capture_after=subprocess.check_output([executable,'-S',endpoint,'capture','-s',sid,'-b',main_id,'--trim'],text=True)
        assert 'SENTINEL_PID=' in capture_after and 'sentinel:2' in capture_after
        record('S02_R01_native_layout_and_sentinel_preserved')
        report['sentinel']={'block_id':main_id,'before':before,'after':after,'capture_before':capture_before,'capture_after':capture_after}
        temporary=rex.call('session.new_split',session_id=sid,anchor_id=main_id,direction='vertical',side='after',ratio=.6,layout={'block':{'flavor':'com.superlogical.terminal.shell','label':'Unsaved long running work','options':{'shell':'none','command':['/bin/sh','-c','echo EXTRA_PID=$$; while :; do echo extra-alive; sleep 1; done'],'cwd':str(root)}}},focus=False)['block_ids'][0]
        time.sleep(.5); temp_before=rex.process(sid,temporary)
        partial=app.restore(p['id']); assert partial['result']=='partial' and temporary in blocks(rex.view(sid)),partial
        assert rex.process(sid,temporary)['child']['pid']==temp_before['child']['pid']
        assert saved_file.read_bytes()==saved_bytes
        record('R01_extra_work_preserved_partial'); report['extra_restore']=partial
        # Explicit close in disposable session tests reopen's non-respawn semantics.
        rex.call('block.close',session_id=sid,block_id=github)
        n=len(blocks(rex.view(sid))); app.open(p['id']); assert len(blocks(rex.view(sid)))==n
        record('R03_closed_tool_stays_closed_on_open')
        opened=app.tool(p['id'],'github'); assert opened['created']
        reopened=app.tool(p['id'],'github'); assert not reopened['created'],reopened
        record('R03_explicit_tool_open_exactly_once')
        # Restart Python/controller by using a distinct instance, preserving all native panes.
        app2=Projects(Store(base/'projects',base/'runtime'),Rex(executable,endpoint))
        assert not app2.open(p['id'])['created']; record('R05_cli_restart_rediscovery')
        # Same names are never session ownership keys.
        p2=app.create('acceptance',root)
        second_session=app.open(p2['id'])['session_id']; assert second_session!=sid
        record('P03_duplicate_names_distinct_sessions')
        app.add_tool(p2['id'],'command',['/nonexistent/rawr-fixture'],'missing-tool')
        before_failure=len(blocks(rex.view(second_session))); revision_before=store.resolve(p2['id'])['revision']
        try: app.tool(p2['id'],'missing-tool')
        except Exception as exc:
            assert 'executable unavailable' in str(exc),exc
        else: raise AssertionError('missing executable unexpectedly launched')
        assert len(blocks(rex.view(second_session)))==before_failure
        assert store.resolve(p2['id'])['revision']==revision_before
        record('R09_I02_missing_executable_preserves_work_and_revision')
        # Keep-open shell left by a tool is preserved and replaced only by explicit open.
        rex.call('block.close',session_id=sid,block_id=exe)
        view=rex.view(sid); report['final_layout']=view
        before_focus=focused_id(view); app.plan(p['id'])
        record('R08_status_preserves_focus',before_focus==focused_id(rex.view(sid)))
        rex.call('server.stop'); server.wait(timeout=10); server=start()
        for _ in range(100):
            try:
                new_identity=rex.identity()
                if new_identity['instance_id']!=identity['instance_id']: break
            except Exception: pass
            time.sleep(.1)
        assert not app2.open(p['id'])['created']; assert len(rex.sessions())==2
        restart_plan=app2.plan(p['id']); report['restart_plan']=restart_plan
        assert restart_plan['result']=='partial'
        assert any('fresh shell' in reason for reason in restart_plan['issues'].values())
        record('R06_surface_restart_not_program_restore')
        restored_files=store.observed(p['id'])['roles']['files']['block_id']
        for _ in range(50):
            shell_process=rex.process(sid,restored_files)
            if shell_process.get('foreground',{}).get('name') in ('zsh','bash','sh'):
                break
            time.sleep(.1)
        assert shell_process.get('foreground',{}).get('name') in ('zsh','bash','sh'),shell_process
        subprocess.run([executable,'-S',endpoint,'send','-s',sid,'-b',restored_files,'UNSENT_USER_INPUT'],check=True,capture_output=True)
        before_input=subprocess.check_output([executable,'-S',endpoint,'capture','-s',sid,'-b',restored_files,'--trim'],text=True)
        count_before=len(blocks(rex.view(sid)))
        replacement=app2.tool(p['id'],'files')
        assert replacement['created'] and replacement['result']=='partial',replacement
        after_input=subprocess.check_output([executable,'-S',endpoint,'capture','-s',sid,'-b',restored_files,'--trim'],text=True)
        assert before_input==after_input and 'UNSENT_USER_INPUT' in after_input
        assert len(blocks(rex.view(sid)))==count_before+1
        record('R02_restored_shell_user_input_preserved_no_relaunch_text')
        rex.call('server.stop'); server.wait(timeout=10)
        Path('docs/evidence/acceptance-rex.json').write_text(json.dumps(report,indent=2)+'\n')
        print(json.dumps({'report':'docs/evidence/acceptance-rex.json','checks':report['checks']}))
    finally:
        if server.poll() is None:
            server.terminate(); server.wait(timeout=10)
        log.close()


def focused_id(view):
    return next((w.get('focused_block_id') for w in view.get('windows',[]) if w['window_id']==view.get('active_window_id')),None)


if __name__=='__main__': main()
