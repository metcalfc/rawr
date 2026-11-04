"""Explicit hide/show of native tool surfaces, without closing their programs."""
import copy
from .rex import blocks
from .state import Error

TOOLS=('files','github','exedev')


def inventory(manager,sid):
    value=manager.rex.call('session.list_blocks',session_id=sid)
    entries=value.get('blocks')
    if not isinstance(entries,list) or any(not isinstance(b,dict) or not isinstance(b.get('block_id'),str) for b in entries):
        raise Error('Malformed Rex block inventory; hidden tools were preserved')
    return {b['block_id']:b for b in entries}


def ids(node):
    if node.get('block_id'):
        return [node['block_id']]
    s=node['split']
    return ids(s['before'])+ids(s['after'])


def clean(node):
    if node.get('block_id'):
        return {'block_id':node['block_id']}
    s=node['split']
    return {'split':{'direction':s['direction'],'ratio':s['ratio'],'before':clean(s['before']),'after':clean(s['after'])}}


def logical_view(view,runtime):
    """Compare live arrangements without treating a VM swap as a pane move."""
    roles={b['block_id']:r for r,b in runtime.get('roles',{}).items()}
    def node(n):
        if n.get('block_id'):
            return {'role':roles.get(n['block_id'],'extra:'+n['block_id'])}
        s=n['split']
        return {'direction':s['direction'],'ratio':s['ratio'],'before':node(s['before']),'after':node(s['after'])}
    relevant={runtime.get('roles',{}).get(role,{}).get('block_id') for role in (*TOOLS,runtime.get('vm_slot_role') or 'work-main')}
    return {w['window_id']:[node(layer['layout']) for layer in w['layers'] if layer.get('layout')] for w in view['windows']
            if any(b['block_id'] in relevant for layer in w['layers'] for b in layer.get('blocks',[]))}


def remember_placements(view,runtime):
    visible=blocks(view)
    current=logical_view(view,runtime)
    changed=current!=runtime.get('tool_visibility_view')
    if not runtime.get('hidden_tools') or not runtime.get('tool_placements'):
        runtime['tool_placements']={}
        changed=True
    if changed:
        # Preserve the full-tree placement across our own hide operations, but
        # respect native moves/resizes performed between explicit toggles.
        for role in TOOLS:
            bid=runtime.get('roles',{}).get(role,{}).get('block_id')
            if bid in visible:
                runtime['tool_placements'][role]=placement(view,bid,runtime)


def placement(view,bid,runtime):
    role_by_id={b['block_id']:r for r,b in runtime.get('roles',{}).items()}
    def ref(value):
        return {'role':role_by_id[value]} if value in role_by_id else {'block_id':value}
    def path(node,root=True):
        if node.get('block_id'):
            return [] if node['block_id']==bid else None
        s=node['split']
        for side,other in (('before','after'),('after','before')):
            below=path(s[side],False)
            if below is not None:
                return below+[{'direction':s['direction'],'side':side,'ratio':s['ratio'],
                    'sibling':[ref(value) for value in ids(s[other])],'whole_layer':root}]
        return None
    for window in view['windows']:
        for layer in window['layers']:
            chain=path(layer['layout']) if layer.get('layout') else None
            if chain is not None:
                if layer['kind']!='tiled' or not chain:
                    raise Error('This tool must share a tiled workspace before it can be toggled')
                return {'block_id':bid,'window_id':window['window_id'],'chain':chain}
    raise Error('Tool is not in a visible native layout')


def _lca_path(node,wanted,prefix=()):
    if not wanted or not wanted.issubset(set(ids(node))):
        return None
    if node.get('block_id'):
        return prefix
    for side in ('before','after'):
        found=_lca_path(node['split'][side],wanted,prefix+(side,))
        if found is not None:
            return found
    return prefix


def _insert(node,path,bid,where):
    if not path:
        tool={'block_id':bid}
        before,after=(tool,node) if where['side']=='before' else (node,tool)
        return {'split':{'direction':where['direction'],'ratio':where['ratio'],'before':before,'after':after}}
    result=copy.deepcopy(node)
    side=path[0]
    result['split'][side]=_insert(result['split'][side],path[1:],bid,where)
    return result


def show(manager,p,runtime,identity,view,role):
    """Called under the project lock; returns the same retained block ID."""
    hidden=runtime.get('hidden_tools',{}).get(role)
    if not hidden:
        raise Error('No retained hidden tool exists')
    binding=runtime.get('roles',{}).get(role,{})
    bid=binding.get('block_id');sid=view['session_id'];visible=blocks(view)
    if bid in visible:
        runtime['hidden_tools'].pop(role,None)
        manager.store.record(p['id'],runtime)
        manager.rex.call('session.focus_block',session_id=sid,block_id=bid)
        return bid
    entry=inventory(manager,sid).get(bid)
    if not entry:
        # An explicit open may recreate a genuinely closed hidden tool; never
        # call a retained but detached surface missing.
        runtime['hidden_tools'].pop(role,None)
        manager.store.record(p['id'],runtime)
        return None
    if entry.get('placement')!='detached' or entry.get('host_id')!=identity['host_id'] or entry.get('label')!=binding.get('label'):
        raise Error('Hidden tool ownership/placement changed; native work was preserved')
    okay,reason=manager.healthy(p,role,{**binding,'session_id':sid},identity,{bid:entry})
    if not okay:
        raise Error(f'Hidden tool cannot be reused: {reason}')
    window=next((w for w in view['windows'] if w['window_id']==hidden['window_id']),None)
    if window is None:
        anchor_role=runtime.get('vm_slot_role') or 'work-main'
        anchor=runtime.get('roles',{}).get(anchor_role,{}).get('block_id')
        window=next((w for w in view['windows'] if any(anchor in ids(l['layout']) for l in w['layers'] if l.get('layout'))),None)
    if window is None:
        raise Error('The original tool workspace is unavailable; retained process was preserved')
    layer=next((l for l in window['layers'] if l['kind']=='tiled' and l.get('layout')),None)
    if layer is None:
        raise Error('No tiled work area is available for the hidden tool')
    current=clean(layer['layout']);visible_ids=set(ids(current));desired=None
    for step in hidden['chain']:
        known={runtime.get('roles',{}).get(ref['role'],{}).get('block_id') if 'role' in ref else ref.get('block_id') for ref in step['sibling']}
        known &= visible_ids
        if not known:
            continue
        path=() if step['whole_layer'] else _lca_path(current,known)
        if path is not None:
            desired=_insert(current,path,bid,step)
            break
    if desired is None:
        raise Error('Tool placement is ambiguous after workspace changes; retained process was preserved')
    all_ids=ids(desired)
    old_focus=next((w.get('focused_block_id') for w in view['windows'] if w['window_id']==view.get('active_window_id')),None)
    runtime['tool_toggle_pending']={'operation':'show','role':role,'blocks':all_ids}
    manager.store.record(p['id'],runtime)
    # A retained block can only move beside a block, not a whole split. First
    # attach it, then rebuild this bounded live subtree from the same surfaces.
    manager.rex.call('session.move_block',session_id=sid,block_id=bid,anchor_block_id=ids(current)[0],direction='horizontal',side='before',ratio=.2)
    for other in all_ids[1:]:
        manager.rex.call('session.detach_block',session_id=sid,block_id=other)
    def build(node):
        if node.get('block_id'):
            return
        s=node['split']
        manager.rex.call('session.move_block',session_id=sid,block_id=ids(s['after'])[0],anchor_block_id=ids(s['before'])[0],direction=s['direction'],side='after',ratio=s['ratio'])
        build(s['before']);build(s['after'])
    build(desired)
    final=manager.rex.view(sid)
    final_window=next(w for w in final['windows'] if w['window_id']==window['window_id'])
    actual=next(l['layout'] for l in final_window['layers'] if l['kind']=='tiled')
    from .project import equivalent,observed_layout
    mapping={b:b for b in all_ids}
    if not equivalent(observed_layout(desired,mapping),observed_layout(actual,mapping)):
        raise Error('Tool topology changed during show; turn off native Autobalance splits and inspect the toggle journal')
    runtime['hidden_tools'].pop(role,None)
    runtime.pop('tool_toggle_pending',None)
    runtime['tool_visibility_view']=logical_view(final,runtime)
    if not runtime['hidden_tools']:
        runtime.pop('tool_placements',None)
        runtime.pop('tool_visibility_view',None)
    manager.store.record(p['id'],runtime)
    manager.rex.call('session.focus_block',session_id=sid,block_id=bid)
    return bid


def toggle(manager,value,role):
    if role not in TOOLS:
        raise Error('Only files, github and exedev have tool toggles')
    p=manager.store.resolve(value)
    with manager.store.lock(p['id']):
        p=manager.store.resolve(p['id']);identity,runtime,view=manager.snapshot(p)
        if role not in p['roles']:
            raise Error(f'{role} is not declared for this project')
        if view is None:
            raise Error('Open the project before toggling its tools')
        bid=runtime.get('roles',{}).get(role,{}).get('block_id')
        live=blocks(view)
        if bid in live:
            good,issues,_=manager.classify(p,runtime,identity,view)
            if role not in good:
                raise Error(f'Tool contains unverified work; preserved: {issues.get(role)}')
            remember_placements(view,runtime)
            where=copy.deepcopy(runtime['tool_placements'].get(role) or placement(view,bid,runtime))
            if where.get('block_id')!=bid:
                where=placement(view,bid,runtime)
            where['return_focus']=next((w.get('focused_block_id') for w in view['windows'] if w['window_id']==view.get('active_window_id')),None)
            runtime.setdefault('hidden_tools',{})[role]=where
            # Record the retained ID before hiding; a timeout cannot turn it
            # into an allegedly missing process on the next invocation.
            manager.store.record(p['id'],runtime)
            manager.rex.call('session.detach_block',session_id=view['session_id'],block_id=bid)
            after=manager.rex.view(view['session_id'])
            if bid in blocks(after) or inventory(manager,view['session_id']).get(bid,{}).get('placement')!='detached':
                raise Error('Rex did not hide the tool; native process was preserved')
            runtime['tool_visibility_view']=logical_view(after,runtime)
            manager.store.record(p['id'],runtime)
            anchor_role=runtime.get('vm_slot_role') or 'work-main'
            anchor=runtime.get('roles',{}).get(anchor_role,{}).get('block_id')
            if anchor in blocks(after):
                manager.rex.call('session.focus_block',session_id=view['session_id'],block_id=anchor)
            return {'result':'complete','visible':False,'role':role,'block_id':bid}
        if runtime.get('hidden_tools',{}).get(role):
            bid=show(manager,p,runtime,identity,view,role)
            if bid:
                return {'result':'complete','visible':True,'role':role,'block_id':bid,'reused':True}
        # A closed tool is opened only by this explicit action, using its recipe.
        good,_,_=manager.classify(p,runtime,identity,view)
        if bid and inventory(manager,view['session_id']).get(bid,{}).get('placement')=='detached':
            raise Error('Tool was detached outside the toggle controls; inspect it before relaunching')
        new=manager.launch_role(p,runtime,identity,view,role,good)
        manager.rex.call('session.focus_block',session_id=view['session_id'],block_id=new)
        return {'result':'complete','visible':True,'role':role,'block_id':new,'reused':False}
