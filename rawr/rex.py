"""Only supported installed Rex CLI methods, argv arrays and structured JSON."""
import json
import os
from pathlib import Path
import shutil
import subprocess
from .state import Error

TESTED_VERSION = '0.0.1793645657-gd018d2'
BUNDLED = '/Applications/Rex Beta.app/Contents/Helpers/Rex Server.rex/Contents/MacOS/rex'


class Rex:
    def __init__(self, executable=None, server=None, client=None):
        self.executable = executable or os.environ.get('RAWR_REX') or shutil.which('rex') or BUNDLED
        self.server = server or os.environ.get('RAWR_SERVER')
        self.client = client
        self._instance = None
        self._view_revisions = {}

    def run(self,*args,allow_null=False):
        argv = [self.executable, '--autostart=false', '--timeout','8s']
        if self.server:
            argv += ['-S',self.server]
        try:
            result = subprocess.run(argv+list(args),capture_output=True,text=True,timeout=12)
        except subprocess.TimeoutExpired as exc:
            raise Error('Rex timed out; creation may have succeeded. Inspect pending runtime mapping before retrying.') from exc
        except OSError as exc:
            raise Error(f'Rex executable unavailable: {self.executable}: {exc}') from exc
        if result.returncode:
            raise Error('Rex: '+result.stderr.strip()[:1800])
        try:
            value = json.loads(result.stdout)
            if value is None and allow_null:
                return {}
            if not isinstance(value,dict):
                raise ValueError('expected object')
            return value
        except ValueError as exc:
            raise Error('Malformed Rex JSON; no retry performed. Check the installed version and diagnostics.') from exc

    def identity(self):
        value = self.run('whoami','--json')
        if value.get('rex_version') != TESTED_VERSION:
            raise Error(f'Unsupported Rex version {value.get("rex_version")}; tested {TESTED_VERSION}. Re-run capability discovery before enabling mutations.')
        if not all(isinstance(value.get(k),str) for k in ('host_id','instance_id','endpoint')):
            raise Error('Rex identity lacks host/generation/endpoint; refusing mutation.')
        endpoint = value['endpoint']
        if not endpoint.startswith('unix:'):
            raise Error('Remote Rex execution is not supported; use a local Unix endpoint. VM SSH shells are supported.')
        if self._instance != value['instance_id']:
            self._view_revisions.clear()
            self._instance=value['instance_id']
        return value

    def call(self,method,**args):
        return self.run('api','call',method,json.dumps(args,ensure_ascii=False))

    def sessions(self):
        value = self.call('session.list')
        if not isinstance(value.get('sessions'),list) or any(not isinstance(s,dict) or not isinstance(s.get('session_id'),str) for s in value['sessions']):
            raise Error('Malformed Rex session list')
        return value['sessions']

    def view(self,sid):
        value = self.call('session.view',session_id=sid)
        if value.get('session_id')!=sid or not isinstance(value.get('windows',[]),list) or type(value.get('revision')) is not int:
            raise Error('Malformed or mismatched Rex session view')
        previous=self._view_revisions.get(sid,-1)
        if value['revision'] < previous:
            raise Error('Stale Rex view revision; refusing out-of-order observation. Reinspect before retrying.')
        self._view_revisions[sid]=value['revision']
        return value

    def process(self,sid,bid):
        value=self.run('block','call','-s',sid,'-b',bid,'process')
        if value.get('last_exit') is not None and not isinstance(value['last_exit'],dict):
            raise Error('Malformed Rex process exit metadata; pane preserved')
        for key in ('child','foreground'):
            if value.get(key) is None:
                value[key]={}
            if not isinstance(value[key],dict) or (value[key].get('pid') is not None and type(value[key]['pid']) is not int):
                raise Error('Malformed Rex process identity; pane preserved')
        if value.get('ancestors') is None:
            value['ancestors']=[]
        if not isinstance(value['ancestors'],list) or any(not isinstance(p,dict) for p in value['ancestors']):
            raise Error('Malformed Rex process ancestry; pane preserved')
        return value

    def select(self,sid,bid=None):
        if bid:
            self.call('session.focus_block',session_id=sid,block_id=bid)
        clients=self.call('client.list').get('clients')
        if not isinstance(clients,list) or any(not isinstance(c,dict) or not isinstance(c.get('client_id'),str) or not isinstance(c.get('info',{}),dict) for c in clients):
            raise Error('Malformed Rex client list; session retained, app selection not attempted')
        apps=[c for c in clients if c.get('info',{}).get('kind')=='app']
        if self.client:
            apps=[c for c in apps if c['client_id']==self.client or c.get('info',{}).get('label')==self.client]
        if len(apps)==1:
            self.run('-C',apps[0]['client_id'],'do','session.select',f'session_id={sid}','--json',allow_null=True)
            return 'selected in Rex app'
        if not apps:
            return 'session ready; no connected Rex app selected'
        raise Error('Multiple Rex apps connected; specify --client to display the session.')


def blocks(view):
    result={}
    try:
        for window in view.get('windows',[]):
            for layer in window['layers']:
                for block in layer.get('blocks',[]):
                    bid=block['block_id']
                    if not isinstance(bid,str) or not bid:
                        raise Error('Malformed Rex block ID; refusing mutation')
                    if bid in result:
                        raise Error('Duplicate block ID in Rex view')
                    result[bid]={**block,'window_id':window['window_id'],'layer_kind':layer['kind']}
    except (KeyError,TypeError,AttributeError) as exc:
        raise Error('Malformed Rex layout; refusing mutation') from exc
    return result
