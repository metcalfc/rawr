#!/usr/bin/env python3
"""Only creates/stops an isolated Rex server; never targets the normal server."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time

REX = os.environ.get('RAWR_REX', '/Applications/Rex Beta.app/Contents/Helpers/Rex Server.rex/Contents/MacOS/rex')

def main():
    base = Path(tempfile.mkdtemp(prefix='rawr-probe-', dir='/tmp'))
    endpoint = 'unix://' + str(base / 'server.sock')
    config = base / 'init.lua'
    config.write_text('')
    log = (base / 'server.log').open('w')
    server = None
    def start():
        return subprocess.Popen([REX, 'server', 'run', '--listen', endpoint,
                                 '--state-dir', str(base / 'state'), '--config', str(config)],
                                stdout=log, stderr=log)
    def run(*args):
        out = subprocess.run([REX, '-S', endpoint, '--autostart=false', *args],
                             capture_output=True, text=True, check=True)
        return json.loads(out.stdout)
    def wait():
        for _ in range(100):
            try:
                return run('whoami', '--json')
            except (subprocess.CalledProcessError, json.JSONDecodeError):
                time.sleep(.1)
        raise RuntimeError('isolated server did not start: ' + (base / 'server.log').read_text())
    def call(method, **args):
        return run('api', 'call', method, json.dumps(args))
    report = {'isolated_directory': str(base)}
    try:
        server = start()
        identity = wait()
        report['version'] = identity['rex_version']
        report['identity_fields'] = sorted(identity)
        leaf = lambda name: {'block': {'flavor': 'com.superlogical.terminal.shell', 'label': name,
            'options': {'shell': 'none', 'command': ['/bin/sh', '-c', 'echo SENTINEL_PID=$$; i=0; while :; do echo tick:$i; i=$((i+1)); sleep 1; done'], 'cwd': str(base)}}}
        created = call('session.create', label='rawr-capability-disposable', initial_windows=[{'window_label': 'Probe', 'layout': leaf('sentinel-a')}])
        sid = created['session_id']
        aid = created['initial_windows'][0]['block_ids'][0]
        second = call('session.new_split', session_id=sid, anchor_id=aid, direction='horizontal', side='after', ratio=.7, layout=leaf('sentinel-b'), focus=False)
        bid = second['block_ids'][0]
        call('session.focus_block', session_id=sid, block_id=aid)
        before = run('block', 'call', '-s', sid, '-b', aid, 'process')
        call('session.resize_pane', session_id=sid, direction='horizontal', before_block_id=aid, after_block_id=bid, ratio=.63)
        call('session.detach_block', session_id=sid, block_id=bid)
        call('session.move_block', session_id=sid, block_id=bid, anchor_block_id=aid, direction='vertical', side='after', ratio=.6)
        call('session.new_layer', session_id=sid, bounds=[.1,.1,.6,.5], layout=leaf('float'), focus=False)
        time.sleep(1.2)
        report['before_restart'] = call('session.view', session_id=sid)
        after = run('block', 'call', '-s', sid, '-b', aid, 'process')
        report['process_before_move'] = before
        report['process_after_move'] = after
        report['capture_before_restart'] = subprocess.check_output([REX,'-S',endpoint,'capture','-s',sid,'-b',aid,'--trim'],text=True)
        call('server.stop')
        server.wait(timeout=10)
        server = start()
        restored_id = wait()
        report['instance_changed'] = identity['instance_id'] != restored_id['instance_id']
        report['host_preserved'] = identity['host_id'] == restored_id['host_id']
        report['after_restart'] = call('session.view', session_id=sid)
        report['process_after_restart'] = run('block','call','-s',sid,'-b',aid,'process')
        report['capture_after_restart'] = subprocess.check_output([REX,'-S',endpoint,'capture','-s',sid,'-b',aid,'--trim'],text=True)
        call('server.stop')
        server.wait(timeout=10)
        Path('docs/evidence/capability-probe.json').write_text(json.dumps(report,indent=2)+'\n')
        print(json.dumps({'report':'docs/evidence/capability-probe.json','version':report['version'],'host_preserved':report['host_preserved'],'instance_changed':report['instance_changed']}))
    finally:
        if server and server.poll() is None:
            server.terminate()
            server.wait(timeout=10)
        log.close()

if __name__ == '__main__':
    main()
