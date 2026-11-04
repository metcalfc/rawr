"""Bounded reconciliation. This module has no terminal-input or process-stop API."""
import copy
import os
from pathlib import Path
import shutil
import sys
import time
import uuid
from .state import Error, leaves, validate
from .rex import blocks

LAUNCHER = Path(__file__).resolve().parents[1]/'bin/rawr'


def recipe(kind, root, argv=None):
    return {'kind':kind,'argv':argv or [],'cwd':str(root),'host':'local',
            'reopen':'shell-placeholder' if kind=='shell' else 'explicit'}


def split(direction,ratio,before,after):
    return {'direction':direction,'ratio':ratio,'before':before,'after':after}


def default_setup(roles):
    node={'role':'work-main'}
    right=[r for r in ('github','exedev') if r in roles]
    if right:
        right_node={'role':right[0]}
        if len(right)>1:
            right_node=split('vertical',.5,right_node,{'role':right[1]})
        node=split('horizontal',.70,node,right_node)
    if 'files' in roles:
        node=split('horizontal',.20,{'role':'files'},node)
    return [{'name':'Workspace','layout':node}]


def focused(view):
    return next((w.get('focused_block_id') for w in view.get('windows',[])
                 if w['window_id']==view.get('active_window_id')),None)


def equivalent(a,b):
    if 'role' in a or 'role' in b:
        return a==b
    return a['direction']==b['direction'] and abs(a['ratio']-b['ratio'])<=.005 and equivalent(a['before'],b['before']) and equivalent(a['after'],b['after'])


def observed_layout(node,role_by_id):
    if node.get('block_id'):
        return {'role':role_by_id.get(node['block_id'],'extra:'+node['block_id'])}
    s=node['split']
    return split(s['direction'],s['ratio'],observed_layout(s['before'],role_by_id),observed_layout(s['after'],role_by_id))


class Projects:
    def __init__(self,store,rex):
        self.store=store; self.rex=rex

    def create(self,name,root,tools=(),repository=None,vm=None):
        root=Path(root).expanduser().resolve()
        if not root.is_dir() or not os.access(root,os.R_OK|os.X_OK):
            raise Error(f'Project root is inaccessible: {root}')
        if not name or len(name)>80:
            raise Error('Project name must contain 1–80 characters')
        roles={'work-main':recipe('shell',root)}
        for kind in tools:
            if kind not in ('files','github','exedev'):
                raise Error(f'Unknown tool {kind}')
            roles[kind]=recipe(kind,root)
        p={'schema':1,'id':str(uuid.uuid4()),'name':name,'root':str(root),'host':'local',
           'revision':0,'repository':repository,'vm':vm,'roles':roles,'setup':default_setup(roles)}
        with self.store.lock(p['id']):
            self.store.write(p,None)
        return p

    def command(self,p,role):
        r=p['roles'][role]
        if r['kind'] in ('files','github','exedev'):
            argv=[sys.executable,str(LAUNCHER),'--config-dir',str(self.store.config),'--state-dir',str(self.store.runtime),
                  '--rex',self.rex.executable]
            if self.rex.server:
                argv+=['--server',self.rex.server]
            if self.rex.client:
                argv+=['--client',self.rex.client]
            return argv+['tool',r['kind'],p['id']]
        return r['argv']

    def preflight(self,p,roles):
        for role in roles:
            r=p['roles'][role]
            if not Path(r['cwd']).is_dir() or not os.access(r['cwd'],os.R_OK|os.X_OK):
                raise Error(f'{role}: working directory inaccessible: {r["cwd"]}')
            argv=self.command(p,role)
            if argv and not shutil.which(argv[0]):
                raise Error(f'{role}: executable unavailable: {argv[0]}')

    def block_node(self,p,role,label):
        argv=self.command(p,role)
        options={'cwd':p['roles'][role]['cwd'],'exit':{'on_completion':False}}
        if argv:
            # macOS login wrappers hide descendants from Rex's process scanner.
            # Direct tools must remain inspectable; ordinary work shells retain
            # Rex's normal login-shell behavior.
            options.update(command=argv,shell='none',login=False)
        if p['roles'][role]['kind'] in ('files','github','exedev'):
            # Direct launch skips shell profiles; prepend only directories of
            # declared tool dependencies, never a captured environment.
            options['path_prepend']=sorted({str(Path(found).parent) for dependency in ('gh','git','ssh','open')
                                            if (found:=shutil.which(dependency))})
        return {'block':{'flavor':'com.superlogical.terminal.shell','label':label,'options':options}}

    def layout(self,p,node,labels):
        if 'role' in node:
            return self.block_node(p,node['role'],labels[node['role']])
        return {'split':{'direction':node['direction'],'ratio':node['ratio'],
                         'before':self.layout(p,node['before'],labels),
                         'after':self.layout(p,node['after'],labels)}}

    def snapshot(self,p):
        identity=self.rex.identity()
        runtime=self.store.observed(p['id'])
        if runtime.get('tool_toggle_pending') or runtime.get('vm_main_pending'):
            raise Error('Interrupted native pane operation: all programs were preserved. Inspect the runtime journal and native block inventory before explicit association; no relaunch was attempted.')
        if 'vm_switch_pending' in runtime:
            raise Error('Interrupted VM switch: all existing shells are preserved. Inspect the vm_switch_pending record and native tabs before explicitly associating the intended workspace. No launch or swap was retried.')
        if runtime.get('layout_pending'):
            raise Error('Interrupted layout operation: existing blocks may be detached. No relaunch attempted. Inspect `rex block ls -s SESSION --json`, reattach saved IDs using native move, then explicitly associate the session and save.')
        if runtime.get('host_id') and runtime['host_id']!=identity['host_id']:
            raise Error('Rex host identity changed. Saved setup is intact; explicitly associate the intended session before reconnecting.')
        if runtime.get('pending'):
            self.recover_pending(p,runtime,identity)
        sid=runtime.get('session_id')
        if not sid or sid not in {s['session_id'] for s in self.rex.sessions()}:
            return identity,runtime,None
        view=self.rex.view(sid)
        if view.get('label')!=runtime.get('session_label'):
            raise Error('Session association changed (label/ID mismatch); explicitly associate before mutating it.')
        for b in blocks(view).values():
            if b.get('host_id')!=identity['host_id']:
                raise Error('Session contains a remote block; local path ownership cannot be verified.')
        return identity,runtime,view

    def recover_pending(self,p,runtime,identity):
        pending=runtime['pending']
        if pending['kind']=='split' and runtime.get('session_id') in {s['session_id'] for s in self.rex.sessions()}:
            view=self.rex.view(runtime['session_id'])
            found=[b for b in blocks(view).values() if b.get('label')==pending['label'] and b.get('host_id')==identity['host_id']]
            if len(found)==1 and runtime.get('instance_id')==identity['instance_id']:
                # Exact private creation token + already bound session + same host/generation.
                role=pending['role']; bid=found[0]['block_id']
                runtime.setdefault('roles',{})[role]={'block_id':bid,'label':pending['label'],
                    'instance_id':identity['instance_id'],'pid':self.child_pid(runtime['session_id'],bid)}
                runtime.pop('pending'); self.store.record(p['id'],runtime)
                return
        raise Error('Ambiguous prior Rex creation. No retry performed. Inspect Rex and runtime pending record; use `associate ID --session FULL_ID` after verifying the intended session. No ownership is inferred from titles.')

    def child_pid(self,sid,bid):
        return self.rex.process(sid,bid).get('child',{}).get('pid')

    def healthy(self,p,role,binding,identity,live,observed_process=None,require_recipe=False):
        bid=binding.get('block_id')
        b=live.get(bid)
        if not b:
            return False,'missing'
        if b.get('label')!=binding.get('label'):
            return False,'association label changed; pane preserved'
        r=p['roles'][role]
        if (role=='work-main' and not require_recipe) or r['kind']=='shell':
            return True,None
        if binding.get('instance_id')!=identity['instance_id']:
            return False,'Rex restored a surface with a fresh shell; pane preserved'
        if binding.get('pid') is None:
            return False,'launch verification incomplete; inspect and explicitly associate before retrying'
        process=observed_process
        if process is None:
            process=self.rex.process(binding['session_id'],bid) if binding.get('session_id') else None
        if process is None:
            return False,'unverified program'
        if process.get('last_exit') is not None:
            return False,'declared program exited; pane preserved'
        if binding.get('pid')!=process.get('child',{}).get('pid'):
            return False,'terminal program changed; pane preserved'
        child=process.get('child',{})
        signature={k:child.get(k) for k in ('path','name','script','argv0','invoked_path') if child.get(k) is not None}
        if binding.get('program') and binding['program']!=signature:
            return False,'terminal program identity changed; pane preserved'
        argv=self.command(p,role)
        all_processes=[process.get('foreground',{}),process.get('child',{}),*process.get('ancestors',[])]
        expected=os.path.realpath(shutil.which(argv[0]) or argv[0]) if argv else ''
        script=argv[1] if len(argv)>1 and Path(argv[1]).is_absolute() and Path(argv[1]).is_file() else None
        def matches(proc):
            executable=any(proc.get(k) and os.path.realpath(proc[k])==expected
                           for k in ('path','invoked_path','argv0'))
            script_matches=bool(script and proc.get('script') and
                                os.path.realpath(proc['script'])==os.path.realpath(script))
            # Homebrew's Python launcher execs Python.app and rewrites argv0.
            # For our own built-in views Rex's exact script field identifies the
            # directly launched entrypoint; the child PID and saved native
            # process fingerprint must also remain unchanged above.
            builtin_script=r['kind'] in ('files','github','exedev') and script_matches
            return (executable or builtin_script) and (not script or script_matches)
        match=any(matches(proc) for proc in all_processes)
        return (True,None) if match else (False,'different or unverified foreground program; pane preserved')

    def classify(self,p,runtime,identity,view):
        live=blocks(view); good={}; issues={}
        for role,binding in runtime.get('roles',{}).items():
            if role not in p['roles']:
                continue
            binding={**binding,'session_id':view['session_id']}
            okay,reason=self.healthy(p,role,binding,identity,live)
            if okay:
                good[role]=binding['block_id']
            else:
                issues[role]=reason
        extra=sorted(set(live)-set(good.values()))
        return good,issues,extra

    def binding(self,p,runtime,identity,role,bid,label):
        # Native process metadata can arrive just after creation. Bind only an
        # observed declared program, rather than blessing a startup wrapper.
        binding={'block_id':bid,'label':label,'pid':None,'instance_id':identity['instance_id']}
        for attempt in range(20):
            proc=self.rex.process(runtime['session_id'],bid)
            child=proc.get('child',{})
            candidate={**binding,'pid':child.get('pid'),'session_id':runtime['session_id']}
            okay,_=self.healthy(p,role,candidate,identity,{bid:{'label':label}},observed_process=proc,require_recipe=True)
            if candidate['pid'] is not None and okay:
                binding['pid']=candidate['pid']
                binding['program']={k:child.get(k) for k in ('path','name','script','argv0','invoked_path')
                                    if child.get(k) is not None}
                break
            time.sleep(.05)
        runtime.setdefault('roles',{})[role]=binding

    def new_view(self,p,runtime,identity):
        roles=[r for tab in p['setup'] for r in leaves(tab['layout'])]
        self.preflight(p,roles)
        token=uuid.uuid4().hex
        labels={r:f'rawr:{token[:12]}:{r}' for r in roles}
        runtime.clear(); runtime.update(host_id=identity['host_id'],instance_id=identity['instance_id'],roles={},
            pending={'kind':'session','token':token})
        self.store.record(p['id'],runtime)
        label=f'{p["name"]} [rawr:{p["id"][:8]}:{token[:8]}]'
        result=self.rex.call('session.create',label=label,initial_windows=[{'window_label':t['name'],
            'layout':self.layout(p,t['layout'],labels)} for t in p['setup']])
        sid=result.get('session_id')
        if not isinstance(sid,str):
            raise Error('Malformed creation response; pending record retained, do not retry blindly.')
        runtime.update(session_id=sid,session_label=label)
        # Persist returned IDs before slower inspection. A crash can still be diagnosed.
        windows=result.get('initial_windows')
        valid_windows=isinstance(windows,list) and all(isinstance(w,dict) and isinstance(w.get('block_ids'),list) for w in windows)
        returned=[bid for w in windows for bid in w['block_ids']] if valid_windows else []
        if not valid_windows or any(not isinstance(bid,str) or not bid for bid in returned) or len(returned)!=len(roles) or len(set(returned))!=len(returned):
            self.store.record(p['id'],runtime)
            raise Error('Malformed Rex creation block list; pending creation requires inspection.')
        for role,bid in zip(roles,returned):
            runtime['roles'][role]={'block_id':bid,'label':labels[role],'instance_id':identity['instance_id']}
        runtime.pop('pending'); self.store.record(p['id'],runtime)
        for role,bid in zip(roles,returned):
            self.binding(p,runtime,identity,role,bid,labels[role])
        runtime['last_work']=runtime['roles']['work-main']['block_id']
        self.store.record(p['id'],runtime)
        return self.rex.view(sid)

    def remember_focus(self,p,runtime,view):
        current=focused(view)
        working={b['block_id'] for role,b in runtime.get('roles',{}).items()
                 if p['roles'].get(role,{}).get('kind') in ('shell','command','vm-shell')}
        if current in working:
            runtime['last_work']=current

    def open(self,value):
        p=self.store.resolve(value)
        with self.store.lock(p['id']):
            p=self.store.resolve(p['id']); identity,runtime,view=self.snapshot(p)
            if view is None:
                view=self.new_view(p,runtime,identity)
                created=True
            else:
                created=False; self.remember_focus(p,runtime,view)
            self.store.record(p['id'],runtime)
            live=blocks(view); bid=runtime.get('last_work')
            if bid not in live:
                bid=focused(view)
            message=self.rex.select(view['session_id'],bid)
            return {'result':'complete','created':created,'session_id':view['session_id'],'display':message}

    def add_tool(self,value,kind,argv=None,role=None):
        p=self.store.resolve(value); role=role or kind
        with self.store.lock(p['id']):
            p=self.store.resolve(p['id'])
            if role in p['roles']:
                raise Error('Role already declared')
            old=p['revision']; p['roles'][role]=recipe(kind,p['root'],argv)
            p['revision']+=1
            self.store.write(p,old)
        return p

    def launch_role(self,p,runtime,identity,view,role,good):
        self.preflight(p,[role])
        previous=runtime.get('roles',{}).get(role,{}).get('block_id')
        if previous and previous not in blocks(view):
            from .toggles import inventory
            if inventory(self,view['session_id']).get(previous,{}).get('placement')=='detached':
                raise Error('The existing tool is detached, not closed. Inspect or show its retained surface before relaunching.')
        anchor=good.get('work-main') or next(iter(good.values()),None)
        if not anchor:
            raise Error('No unambiguous project anchor remains. Existing panes preserved; explicitly associate or open a separate project view.')
        label=f'rawr:{uuid.uuid4().hex[:12]}:{role}'
        runtime['pending']={'kind':'split','role':role,'label':label}
        self.store.record(p['id'],runtime)
        result=self.rex.call('session.new_split',session_id=view['session_id'],anchor_id=anchor,
            direction='horizontal',side='before' if role=='files' else 'after',ratio=.20 if role=='files' else .70,
            layout=self.block_node(p,role,label),focus=False)
        ids=result.get('block_ids')
        if not isinstance(ids,list) or len(ids)!=1 or not isinstance(ids[0],str) or not ids[0]:
            raise Error('Malformed Rex split response; pending record retained')
        bid=ids[0]
        runtime['roles'][role]={'block_id':bid,'label':label,'instance_id':identity['instance_id']}
        runtime.pop('pending'); self.store.record(p['id'],runtime)
        self.binding(p,runtime,identity,role,bid,label)
        self.store.record(p['id'],runtime)
        return bid

    def tool(self,value,role):
        p=self.store.resolve(value)
        with self.store.lock(p['id']):
            p=self.store.resolve(p['id'])
            if role not in p['roles']:
                raise Error('Undeclared role; use `declare` first')
            identity,runtime,view=self.snapshot(p)
            if view is None:
                view=self.new_view(p,runtime,identity)
            if runtime.get('hidden_tools',{}).get(role):
                from .toggles import show
                retained=show(self,p,runtime,identity,view,role)
                if retained:
                    return {'result':'complete','created':False,'block_id':retained,'visible':True}
                view=self.rex.view(view['session_id'])
            self.remember_focus(p,runtime,view)
            good,issues,extra=self.classify(p,runtime,identity,view)
            if 'launch verification incomplete' in issues.get(role,''):
                raise Error(issues[role])
            bid=good.get(role)
            created=not bool(bid)
            if created:
                bid=self.launch_role(p,runtime,identity,view,role,good)
            self.store.record(p['id'],runtime)
            self.rex.select(view['session_id'],bid)
            return {'result':'partial' if issues.get(role) not in (None,'missing') else 'complete',
                    'created':created,'block_id':bid,'preserved_previous':issues.get(role)}

    def save(self,value,expected=None):
        p=self.store.resolve(value)
        with self.store.lock(p['id']):
            p=self.store.resolve(p['id'])
            if expected is not None and p['revision']!=expected:
                raise Error('Stale save revision; inspect and save again')
            identity,runtime,view=self.snapshot(p)
            if view is None:
                raise Error('Project is closed; open it before saving')
            good,issues,extra=self.classify(p,runtime,identity,view)
            inverse={v:k for k,v in good.items()}; notices=[]
            candidate=copy.deepcopy(p); candidate_runtime=copy.deepcopy(runtime)
            live=blocks(view)
            def node(n):
                if n.get('block_id'):
                    bid=n['block_id']
                    if bid not in inverse:
                        role='shell-'+uuid.uuid4().hex[:8]
                        inverse[bid]=role
                        candidate['roles'][role]=recipe('shell',p['root'])
                        candidate_runtime.setdefault('roles',{})[role]={'block_id':bid,'label':live[bid].get('label',''),
                            'instance_id':identity['instance_id'],'pid':self.child_pid(view['session_id'],bid)}
                        notices.append(f'{role}: shell placeholder; current process/conversation will not be recreated')
                    return {'role':inverse[bid]}
                s=n['split']
                return split(s['direction'],s['ratio'],node(s['before']),node(s['after']))
            setup=[]
            for window in view['windows']:
                if len(window['layers'])!=1 or window['layers'][0]['kind']!='tiled':
                    raise Error('Saving floating/empty layers is unsupported; tile or close the float, then save. Previous setup retained.')
                setup.append({'name':window.get('label','Workspace'),'layout':node(window['layers'][0]['layout'])})
            candidate['setup']=setup; candidate['revision']+=1
            validate(candidate)
            # Re-check revision after process reads; never commit a stale layout view.
            if self.rex.view(view['session_id'])['revision']!=view['revision']:
                raise Error('Rex layout changed during save; retry once arrangement is stable')
            self.store.write(candidate,p['revision'])
            self.remember_focus(candidate,candidate_runtime,view)
            self.store.record(p['id'],candidate_runtime)
            return {'result':'complete','revision':candidate['revision'],'notices':notices,
                    'process_restoration':'Only shell placeholders and declared recipes; no process or conversation snapshots.'}

    def plan(self,value):
        p=self.store.resolve(value)
        # Pure read: pending records must not be recovered/written by plan/status.
        identity=self.rex.identity(); runtime=self.store.observed(p['id'])
        if runtime.get('pending') or runtime.get('layout_pending') or runtime.get('tool_toggle_pending') or runtime.get('vm_main_pending') or 'vm_switch_pending' in runtime:
            return {'result':'failed','conflicts':['Ambiguous pending creation; inspect runtime mapping'],'actions':[]}
        if runtime.get('host_id') and runtime['host_id']!=identity['host_id']:
            return {'result':'failed','conflicts':['Rex host identity changed'],'actions':[]}
        sid=runtime.get('session_id')
        if sid not in {s['session_id'] for s in self.rex.sessions()}:
            return {'result':'complete','revision':p['revision'],'actions':['create saved project session'],'conflicts':[]}
        view=self.rex.view(sid)
        if view.get('label')!=runtime.get('session_label'):
            return {'result':'failed','actions':[],'conflicts':['Session association changed']}
        good,issues,extra=self.classify(p,runtime,identity,view)
        wanted=[r for tab in p['setup'] for r in leaves(tab['layout'])]
        workspace,archived=self.workspace_windows(p,runtime,view)
        live=blocks(view)
        archived_ids={b['block_id'] for w in archived for layer in w['layers'] for b in layer['blocks']}
        extra=sorted(set(live)-{good[r] for r in wanted if r in good}-archived_ids)
        missing=[r for r in wanted if r not in good]
        hidden=[]
        if runtime.get('hidden_tools'):
            from .toggles import inventory
            retained=inventory(self,sid)
            hidden=[r for r in missing if r in runtime['hidden_tools'] and retained.get(runtime.get('roles',{}).get(r,{}).get('block_id'),{}).get('placement')=='detached']
        conflicts=[f'{r}: {why}' for r,why in issues.items() if why!='missing']
        if any('launch verification incomplete' in reason for reason in issues.values()):
            return {'result':'failed','actions':[],'conflicts':conflicts,'roles':good,'missing':[]}
        if extra:
            conflicts.append('Preserve extra/occupied panes; exact geometry will remain unapplied: '+', '.join(extra))
        if any(len(w['layers'])!=1 for w in workspace):
            conflicts.append('Live floats preserved; tiled restore cannot replace them')
        groups=[]
        for tab in p['setup']:
            ids=[good[r] for r in leaves(tab['layout']) if r in good]
            windows={live[bid]['window_id'] for bid in ids}
            if len(windows)>1:
                conflicts.append('Saved tab roles now span multiple live tabs; grouping preserved')
            groups += list(windows)
        if len(groups)!=len(set(groups)):
            conflicts.append('Multiple saved tabs now share a live tab; grouping preserved')
        actual=[observed_layout(w['layers'][0]['layout'],{b:r for r,b in good.items()}) for w in workspace if w.get('layers') and w['layers'][0].get('layout')]
        desired=[t['layout'] for t in p['setup']]
        matches=len(actual)==len(desired) and all(equivalent(a,b) for a,b in zip(actual,desired))
        return {'result':'partial' if conflicts else 'complete','revision':p['revision'],'session_id':sid,
                'observed_revision':view['revision'],'missing':missing,'extra':extra,'conflicts':conflicts,
                'hidden_tools':hidden,
                'layout_matches_saved':matches,
                'archived_vm_tabs':[w.get('label','VM') for w in archived],
                'actions':[f'show retained {r}' if r in hidden else f'launch {r} in a new native pane' for r in missing]+['reconcile supported tiled layout' if not conflicts else 'preserve live geometry'],
                'roles':good,'issues':issues}

    def restore(self,value):
        p=self.store.resolve(value)
        with self.store.lock(p['id']):
            p=self.store.resolve(p['id']); identity,runtime,view=self.snapshot(p)
            if view is None:
                view=self.new_view(p,runtime,identity)
                self.rex.select(view['session_id'],runtime['roles']['work-main']['block_id'])
                return {'result':'complete','session_id':view['session_id'],'created':True}
            self.remember_focus(p,runtime,view)
            old_focus=focused(view); outcome=self.plan(p['id']); changes=[]
            if outcome['result']=='failed':
                return outcome
            try:
                good=outcome['roles']
                for role in outcome['missing']:
                    retained=None
                    if runtime.get('hidden_tools',{}).get(role):
                        from .toggles import show
                        retained=show(self,p,runtime,identity,self.rex.view(view['session_id']),role)
                    good[role]=retained or self.launch_role(p,runtime,identity,view,role,good)
                    changes.append(f'opened {role}')
                # Reinspect: existing user work wins over desired geometry.
                current=self.plan(p['id'])
                if current['conflicts']:
                    return {**current,'result':'partial','changes':changes}
                view=self.rex.view(view['session_id'])
                good=current['roles']
                sid=view['session_id']
                runtime['layout_pending']={'blocks':list(good.values()),'observed_revision':view['revision']}
                self.store.record(p['id'],runtime)
                for tab in p['setup']:
                    roles=leaves(tab['layout']); anchor=good[roles[0]]
                    for role in roles[1:]:
                        self.rex.call('session.detach_block',session_id=sid,block_id=good[role])
                    def build(n):
                        if 'role' in n:
                            return good[n['role']]
                        before=good[leaves(n['before'])[0]]; after=good[leaves(n['after'])[0]]
                        self.rex.call('session.move_block',session_id=sid,block_id=after,anchor_block_id=before,
                            direction=n['direction'],side='after',ratio=n['ratio'])
                        build(n['before']); build(n['after'])
                        return before
                    build(tab['layout'])
                # The Mac client may balance newly inserted splits asynchronously.
                # Once topology exists, set borders using Rex's native resize API.
                time.sleep(.15)
                def resize(n):
                    if 'role' in n:
                        return
                    self.rex.call('session.resize_pane',session_id=sid,direction=n['direction'],
                        before_block_id=good[leaves(n['before'])[0]],after_block_id=good[leaves(n['after'])[0]],ratio=n['ratio'])
                    resize(n['before']); resize(n['after'])
                for tab in p['setup']:
                    resize(tab['layout'])
                time.sleep(.15)
                final=self.rex.view(sid)
                # Verify role topology and ratios, not just command exit codes.
                def topology(n):
                    if n.get('block_id'):
                        return {'role':next(r for r,b in good.items() if b==n['block_id'])}
                    s=n['split']; return split(s['direction'],s['ratio'],topology(s['before']),topology(s['after']))
                actual=[topology(w['layers'][0]['layout']) for w in self.workspace_windows(p,runtime,final)[0]]
                wanted=[t['layout'] for t in p['setup']]
                if len(actual)!=len(wanted) or not all(equivalent(a,b) for a,b in zip(actual,wanted)):
                    raise Error('Rex returned a different layout; exact restore verification failed')
                runtime.pop('layout_pending',None)
                return {'result':'complete','session_id':sid,'changes':changes+['restored native tiled topology and ratios']}
            except Error as exc:
                return {'result':'partial','changes':changes,'conflicts':[str(exc)],'recovery':'Existing programs retained, including any detached blocks. Inspect `rex block ls -s SESSION --json`; reattach with native move before retrying.'}
            finally:
                self.store.record(p['id'],runtime)
                try:
                    if old_focus and old_focus in blocks(self.rex.view(view['session_id'])):
                        self.rex.call('session.focus_block',session_id=view['session_id'],block_id=old_focus)
                except Error:
                    pass

    def associate(self,value,sid):
        p=self.store.resolve(value)
        with self.store.lock(p['id']):
            identity=self.rex.identity(); view=self.rex.view(sid); live=blocks(view)
            # Explicit recovery associates a session and one working surface only.
            # It never guesses tool identity or starts programs in its terminals.
            if any(b.get('host_id')!=identity['host_id'] for b in live.values()):
                raise Error('Cannot associate remote blocks')
            bid=focused(view)
            if bid not in live:
                raise Error('Select a working pane in the intended session first')
            runtime={'session_id':sid,'session_label':view['label'],'host_id':identity['host_id'],
                'instance_id':identity['instance_id'],'last_work':bid,'roles':{'work-main':{'block_id':bid,
                'label':live[bid].get('label',''),'instance_id':identity['instance_id'],'pid':self.child_pid(sid,bid)}}}
            self.store.record(p['id'],runtime)
            return {'result':'complete','session_id':sid,'notice':'Explicit working-pane association saved; other surfaces preserved as extra work. Save setup to include shell placeholders.'}

    def link_vm(self,value,name):
        p=self.store.resolve(value)
        with self.store.lock(p['id']):
            p=self.store.resolve(p['id']); old=p['revision']; p['vm']=name; p['revision']+=1
            self.store.write(p,old)
        return p

    def vm_shell(self,value,vm):
        p=self.store.resolve(value)
        if p.get('vm')!=vm['vm_name']:
            raise Error('VM changed; refresh and explicitly link the intended VM first')
        return self.switch_vm(value,vm,focus=True)

    def toggle(self,value,role):
        from .toggles import toggle
        return toggle(self,value,role)

    def project_for_session(self,sid):
        matches=[p['id'] for p in self.store.projects() if self.store.observed(p['id']).get('session_id')==sid]
        if len(matches)!=1:
            raise Error('The selected Rex session has no unique project association in this profile. Open the intended project first.')
        return matches[0]

    def workspace_windows(self,p,runtime,view):
        """Unsaved owned VM-history tabs coexist with the deliberate workspace.

        A moved shell or a tab containing extra work is no longer exempt. An
        explicit save includes history tabs normally, with their known recipes.
        """
        wanted={r for tab in p['setup'] for r in leaves(tab['layout'])}
        archive_roles=set(runtime.get('vm_archives',{}).values())-wanted
        owned={runtime.get('vm_archive_windows',{}).get(role):runtime.get('roles',{}).get(role,{}).get('block_id')
               for role in archive_roles}
        workspace=[]; archived=[]
        for window in view['windows']:
            layers=window.get('layers',[])
            ids=[b['block_id'] for layer in layers for b in layer.get('blocks',[])]
            if len(layers)==1 and layers[0]['kind']=='tiled' and ids==[owned.get(window['window_id'])]:
                archived.append(window)
            else:
                workspace.append(window)
        return workspace,archived

    def switch_vm(self,value,vm,focus=False):
        """Activate a host in the stable VM slot; archive, never stop, its shell."""
        from .services import exe_shell_argv
        argv=exe_shell_argv(vm)
        name=vm['vm_name']
        p=self.store.resolve(value)
        with self.store.lock(p['id']):
            p=self.store.resolve(p['id']); identity,runtime,view=self.snapshot(p)
            if view is None:
                raise Error('Open the project before switching its VM shell')
            sid=view['session_id']; live=blocks(view)
            good,issues,_=self.classify(p,runtime,identity,view)
            slot=runtime.get('vm_slot_role')
            if slot and (slot not in p['roles'] or p['roles'][slot]['kind']!='vm-shell'):
                raise Error('Invalid VM slot mapping; inspect the runtime state before switching')
            if not slot:
                # Migrate the previously shipped per-host role without altering
                # any saved split/tab references or geometry.
                legacy='vm-'+uuid.uuid5(uuid.NAMESPACE_URL,p.get('vm') or '').hex[:12]
                if legacy in runtime.get('roles',{}) and p['roles'].get(legacy,{}).get('kind')=='vm-shell':
                    slot=legacy
                else:
                    main_window=live.get(good.get('work-main'),{}).get('window_id')
                    candidates=[r for r,b in runtime.get('roles',{}).items() if p['roles'].get(r,{}).get('kind')=='vm-shell'
                                and live.get(b['block_id'],{}).get('window_id')==main_window]
                    if len(candidates)>1:
                        raise Error('Multiple VM shells in the work area; cannot identify the replacement slot safely')
                    slot=candidates[0] if candidates else 'work-main'
            old_binding=runtime.get('roles',{}).get(slot)
            old_id=old_binding['block_id'] if old_binding and old_binding['block_id'] in live else None
            old_name=runtime.get('vm_slot_name') or p['roles'].get(slot,{}).get('vm_name')
            if not old_name and slot=='vm-'+uuid.uuid5(uuid.NAMESPACE_URL,p.get('vm') or '').hex[:12]:
                old_name=p.get('vm')
            old_healthy=slot in good
            if old_id and slot=='work-main' and p['roles'].get(slot,{}).get('kind')=='vm-shell':
                old_healthy,reason=self.healthy(p,slot,{**old_binding,'session_id':sid},identity,live,require_recipe=True)
                if not old_healthy:
                    issues[slot]=reason
            if old_id and not old_healthy and 'launch verification incomplete' in issues.get(slot,''):
                raise Error(issues[slot])
            if old_id and old_healthy and old_name==name:
                if p.get('vm')!=name:
                    candidate=copy.deepcopy(p); candidate['vm']=name; candidate['revision']+=1
                    self.store.write(candidate,p['revision'])
                runtime.update(vm_slot_role=slot,vm_slot_name=name)
                self.store.record(p['id'],runtime)
                if focus:
                    self.rex.select(sid,old_id)
                return {'result':'complete','created':False,'block_id':old_id,'vm':name,'archived_vm':None}
            candidate=copy.deepcopy(p)
            target_recipe=recipe('vm-shell',p['root'],argv); target_recipe['vm_name']=name
            archive_role=runtime.get('vm_archives',{}).get(name)
            target_binding=None; target_id=None; archive_window=None
            if archive_role:
                target_binding=runtime.get('roles',{}).get(archive_role)
                target_id=good.get(archive_role)
                archive_window=runtime.get('vm_archive_windows',{}).get(archive_role)
                if target_binding and target_binding.get('block_id') not in live:
                    archive_role=None; target_binding=None; target_id=None; archive_window=None
                elif not target_id or live[target_id]['window_id']!=archive_window:
                    raise Error('The selected host shell changed program or moved out of its archive tab; it was preserved. Inspect that tab before switching.')
                elif len(next(w for w in view['windows'] if w['window_id']==archive_window)['layers'])!=1 or sum(b['window_id']==archive_window for b in live.values())!=1:
                    raise Error('The selected host archive tab contains extra work; it was preserved. Move that work before switching.')
            if not old_id and not good.get('work-main'):
                raise Error('No verified working pane is available for the VM shell slot')
            staged_role=archive_role or 'vm-history-'+uuid.uuid4().hex[:12]
            candidate['roles'][staged_role]=target_recipe
            self.preflight(candidate,[staged_role])
            runtime['vm_switch_pending']={'phase':'prepare','target_vm':name,'slot_role':slot,
                'old_block_id':old_id,'target_block_id':target_id,'expected_revision':p['revision']}
            self.store.record(p['id'],runtime)
            try:
                created=not bool(target_id)
                if created:
                    label=f'rawr:{uuid.uuid4().hex[:12]}:{staged_role}'
                    result=self.rex.call('session.new_window',session_id=sid,window_label=f'VM · {name}',focus=False,
                        layout=self.block_node(candidate,staged_role,label))
                    ids=result.get('block_ids')
                    if not isinstance(ids,list) or len(ids)!=1 or not isinstance(ids[0],str) or not ids[0] or not isinstance(result.get('window_id'),str) or not result['window_id']:
                        raise Error('Malformed VM shell creation response; inspect native tabs before retrying')
                    target_id=ids[0]; archive_window=result['window_id']
                    runtime['vm_switch_pending'].update(target_block_id=target_id,archive_window_id=archive_window,phase='launched')
                    self.store.record(p['id'],runtime)
                    self.binding(candidate,runtime,identity,staged_role,target_id,label)
                    target_binding=runtime['roles'][staged_role]
                    if target_binding.get('pid') is None:
                        raise Error('The new SSH program did not remain running; its diagnostic tab and previous shell were preserved')
                if old_id:
                    runtime['vm_switch_pending']['phase']='swapping'
                    self.store.record(p['id'],runtime)
                    self.rex.call('session.swap_blocks',session_id=sid,block_id=old_id,other_block_id=target_id)
                    check=blocks(self.rex.view(sid))
                    if check.get(target_id,{}).get('window_id')!=live[old_id]['window_id'] or check.get(old_id,{}).get('window_id')!=archive_window:
                        raise Error('VM swap did not return the expected native placements; all shells were preserved')
                    candidate['roles'][staged_role]=copy.deepcopy(p['roles'][slot]) if old_healthy else recipe('shell',p['root'])
                    if old_name and old_healthy:
                        candidate['roles'][staged_role]['vm_name']=old_name
                    runtime['roles'][staged_role]=old_binding
                    archives=runtime.setdefault('vm_archives',{})
                    archives.pop(name,None)
                    if old_name and old_healthy:
                        archives[old_name]=staged_role
                    else:
                        archives['preserved-work-'+staged_role]=staged_role
                    runtime.setdefault('vm_archive_windows',{})[staged_role]=archive_window
                    archive_title=f'VM · {old_name}' if old_name and old_healthy else 'Work · previous'
                    self.rex.call('session.set_window_label',session_id=sid,window_id=archive_window,label=archive_title)
                else:
                    self.rex.call('session.move_block',session_id=sid,block_id=target_id,anchor_block_id=good['work-main'],direction='vertical',side='after',ratio=.6)
                    runtime['roles'].pop(staged_role,None)
                    if staged_role not in p['roles']:
                        candidate['roles'].pop(staged_role,None)
                    runtime.setdefault('vm_archives',{}).pop(name,None)
                    runtime.setdefault('vm_archive_windows',{}).pop(staged_role,None)
                # Rex's native swap preserves every divider. Do not rewrite
                # borders or race the Mac client's Autobalance splits policy;
                # that preference must be off for deliberately arranged panes.
                workspace_window=live[old_id or good['work-main']]['window_id']
                window=next(w for w in view['windows'] if w['window_id']==workspace_window)
                if old_id:
                    final=self.rex.view(sid)
                    final_window=next((w for w in final['windows'] if w['window_id']==workspace_window),None)
                    expected=[observed_layout(layer['layout'],{b:target_id if b==old_id else b for b in live}) for layer in window['layers']]
                    after_ids={b:b for b in blocks(final)}
                    actual=[observed_layout(layer['layout'],after_ids) for layer in final_window['layers']] if final_window else []
                    if len(actual)!=len(expected) or not all(equivalent(a,b) for a,b in zip(actual,expected)):
                        raise Error('VM slot geometry changed during switching; turn off Rex Settings > Interface > Pane Behavior > Autobalance splits. Native programs were preserved')
                candidate['roles'][slot]=target_recipe
                candidate['vm']=name; candidate['revision']+=1
                runtime['roles'][slot]=target_binding
                runtime.update(vm_slot_role=slot,vm_slot_name=name)
                if focus or runtime.get('last_work')==old_id:
                    runtime['last_work']=target_id
                self.store.write(candidate,p['revision'])
                runtime.pop('vm_switch_pending')
                self.store.record(p['id'],runtime)
            except Error as exc:
                raise Error(f'VM switch incomplete: {exc}. Previous programs were not stopped; inspect native VM tabs and the pending switch record before retrying.') from exc
            # A swap follows a focused old shell into its archive; return to the
            # picker unless the caller explicitly requested SSH focus.
            desired_focus=target_id if focus else good.get('exedev') or focused(view)
            final_view=self.rex.view(sid)
            if desired_focus in blocks(final_view):
                if focus:
                    self.rex.select(sid,desired_focus)
                elif focused(final_view)!=desired_focus:
                    # The picker already lives in this client/session. Re-selecting
                    # the app session can restore its stale pane focus after a swap.
                    self.rex.call('session.focus_block',session_id=sid,block_id=desired_focus)
            return {'result':'complete','created':created,'block_id':target_id,'vm':name,
                    'archived_vm':old_name if old_id else None,'archive_window_id':archive_window if old_id else None}
