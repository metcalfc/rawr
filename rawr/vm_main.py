"""Explicitly promote the existing VM shell into the main workspace slot.

Only the known inert migration placeholder is closed. Existing work and SSH
processes move as native Rex blocks and retain their IDs and running programs.
"""
import copy
import os
import uuid

from .rex import blocks
from .state import Error, validate


def _without(node, removed, replacement=None):
    """Prune one leaf, collapse its split, and optionally replace another ID."""
    key = 'role' if 'role' in node else 'block_id'
    if key in node:
        value = node[key]
        if value == removed:
            return None
        return {key: replacement.get(value, value) if replacement else value}
    split = node.get('split', node)
    before = _without(split['before'], removed, replacement)
    after = _without(split['after'], removed, replacement)
    if before is None:
        return after
    if after is None:
        return before
    value = {'direction': split['direction'], 'ratio': split['ratio'],
             'before': before, 'after': after}
    return {'split': value} if 'split' in node else value


def _equivalent(first, second):
    if first is None or second is None:
        return first is second
    if 'block_id' in first or 'block_id' in second:
        return first == second
    a, b = first['split'], second['split']
    return (a['direction'] == b['direction'] and abs(a['ratio'] - b['ratio']) <= .005
            and _equivalent(a['before'], b['before'])
            and _equivalent(a['after'], b['after']))


def _live_tree(node):
    if node.get('block_id'):
        return {'block_id': node['block_id']}
    split = node['split']
    return {'split': {'direction': split['direction'], 'ratio': split['ratio'],
                      'before': _live_tree(split['before']),
                      'after': _live_tree(split['after'])}}


def migrate_main_slot(manager, value):
    """Apply the user's explicit main-area layout change to one open project.

    Caller need not hold the project lock. An interrupted operation is journaled
    and cannot be retried blindly. Saved geometry changes only to remove the
    retired lower VM slot; all other saved split choices are retained.
    """
    store, rex = manager.store, manager.rex
    project = store.resolve(value)
    with store.lock(project['id']):
        project = store.resolve(project['id'])
        identity, runtime, view = manager.snapshot(project)
        if 'vm_main_pending' in runtime:
            raise Error('Interrupted main-pane migration; inspect the pending IDs before retrying')
        if view is None:
            raise Error('Open the project before moving its VM into the main area')
        live = blocks(view)
        if runtime.get('vm_slot_role') == 'work-main':
            return {'result': 'complete', 'migrated': False,
                    'notice': 'The selected VM already uses the main work area'}
        slot = runtime.get('vm_slot_role')
        if not slot:
            legacy = 'vm-' + uuid.uuid5(uuid.NAMESPACE_URL, project.get('vm') or '').hex[:12]
            if legacy in runtime.get('roles', {}):
                slot = legacy
            else:
                candidates = [role for role, binding in runtime.get('roles', {}).items()
                              if project['roles'].get(role, {}).get('kind') == 'vm-shell'
                              and binding.get('block_id') in live]
                if len(candidates) > 1:
                    raise Error('Multiple live VM slots; cannot choose the main-area shell safely')
                slot = candidates[0] if candidates else None
        if not slot:
            return {'result': 'complete', 'migrated': False,
                    'notice': 'No live VM shell; the next selected VM will use the main area'}
        if project['roles'].get(slot, {}).get('kind') != 'vm-shell':
            raise Error('The selected VM slot has no verified SSH recipe')
        good, issues, _ = manager.classify(project, runtime, identity, view)
        if slot not in good or 'work-main' not in good:
            raise Error('Main-area migration requires the original work pane and verified VM shell: '
                        + '; '.join(f'{role}: {issues.get(role, "missing")}'
                                    for role in ('work-main', slot) if role not in good))
        old_work, vm = good['work-main'], good[slot]
        if old_work == vm:
            raise Error('Ambiguous main/VM block association; no layout change was made')
        if live[old_work]['window_id'] != live[vm]['window_id']:
            raise Error('Main work and selected VM are in different tabs; move them together before migration')
        if live[old_work]['layer_kind'] != 'tiled' or live[vm]['layer_kind'] != 'tiled':
            raise Error('Tile the main work and VM panes before main-area migration')
        associated = [binding['block_id'] for role, binding in runtime['roles'].items()
                      if role not in ('work-main', slot)]
        if old_work in associated or vm in associated:
            raise Error('Duplicate pane associations make main-area migration ambiguous')
        workspace = live[old_work]['window_id']
        window = next(w for w in view['windows'] if w['window_id'] == workspace)
        layer = next((layer for layer in window['layers']
                      if {old_work, vm} <= {block['block_id'] for block in layer['blocks']}), None)
        if layer is None:
            raise Error('Main work and selected VM are in different layers; both were preserved')
        expected = _without(_live_tree(layer['layout']), vm, {old_work: vm})
        prior_processes = {bid: rex.process(view['session_id'], bid).get('child', {}).get('pid')
                           for bid in (old_work, vm)}
        original_work_binding = copy.deepcopy(runtime['roles']['work-main'])
        vm_binding = copy.deepcopy(runtime['roles'][slot])
        candidate = copy.deepcopy(project)
        history = 'work-history-' + uuid.uuid4().hex[:12]
        candidate['roles'][history] = copy.deepcopy(project['roles']['work-main'])
        candidate['roles']['work-main'] = copy.deepcopy(project['roles'][slot])
        candidate['roles'].pop(slot)
        setup = []
        for tab in candidate['setup']:
            layout = _without(tab['layout'], slot)
            if layout is not None:
                setup.append({**tab, 'layout': layout})
        candidate['setup'] = setup
        candidate['revision'] += 1
        validate(candidate)
        if not os.access('/usr/bin/true', os.X_OK):
            raise Error('Main-area migration needs the local /usr/bin/true placeholder executable')
        token = 'rawr:' + uuid.uuid4().hex[:12] + ':main-promotion'
        sid = view['session_id']
        runtime['vm_main_pending'] = {'phase': 'prepare', 'old_work_block_id': old_work,
            'vm_block_id': vm, 'legacy_slot_role': slot, 'expected_revision': project['revision'],
            'placeholder_label': token}
        store.record(project['id'], runtime)
        placeholder = None
        try:
            created = rex.call('session.new_window', session_id=sid, window_label='Work · previous', focus=False,
                layout={'block': {'flavor': 'com.superlogical.terminal.shell', 'label': token,
                    'options': {'shell': 'none', 'login': False, 'command': ['/usr/bin/true'],
                        'cwd': project['root'], 'exit': {'on_completion': False}}}})
            ids = created.get('block_ids')
            if (not isinstance(ids, list) or len(ids) != 1 or not isinstance(ids[0], str) or not ids[0]
                    or not isinstance(created.get('window_id'), str) or not created['window_id']):
                raise Error('Malformed main-area placeholder response; inspect native tabs')
            placeholder, archive = ids[0], created['window_id']
            runtime['vm_main_pending'].update(phase='placeholder-created', placeholder_block_id=placeholder,
                                               archive_window_id=archive)
            store.record(project['id'], runtime)
            current = blocks(rex.view(sid))
            def verify_placeholder():
                current = blocks(rex.view(sid))
                block = current.get(placeholder, {})
                if block.get('label') != token or block.get('host_id') != identity['host_id']:
                    raise Error('Migration placeholder ownership changed; no surface was closed')
                return current
            verify_placeholder()
            if current.get(old_work, {}).get('window_id') != workspace or current.get(vm, {}).get('window_id') != workspace:
                raise Error('Workspace changed during migration preparation; all work was preserved')
            runtime['vm_main_pending']['phase'] = 'archiving-work'
            store.record(project['id'], runtime)
            rex.call('session.swap_blocks', session_id=sid, block_id=old_work, other_block_id=placeholder)
            current = verify_placeholder()
            if current.get(old_work, {}).get('window_id') != archive:
                raise Error('The original work pane did not reach the new archive tab')
            runtime['vm_main_pending']['phase'] = 'promoting-vm'
            store.record(project['id'], runtime)
            rex.call('session.swap_blocks', session_id=sid, block_id=vm, other_block_id=placeholder)
            current = verify_placeholder()
            if current.get(vm, {}).get('window_id') != workspace:
                raise Error('The selected VM did not reach the main work area')
            runtime['vm_main_pending']['phase'] = 'closing-owned-placeholder'
            store.record(project['id'], runtime)
            rex.call('block.close', session_id=sid, block_id=placeholder)
            final = rex.view(sid)
            final_live = blocks(final)
            work_tab = next((w for w in final['windows'] if w['window_id'] == archive), None)
            if (placeholder in final_live or final_live.get(old_work, {}).get('window_id') != archive
                    or final_live.get(vm, {}).get('window_id') != workspace or work_tab is None
                    or [b['block_id'] for l in work_tab['layers'] for b in l['blocks']] != [old_work]):
                raise Error('Main-area migration returned unexpected native placements')
            actual_layer = next((l for w in final['windows'] if w['window_id'] == workspace
                                 for l in w['layers'] if any(b['block_id'] == vm for b in l['blocks'])), None)
            if actual_layer is None or not _equivalent(expected, _live_tree(actual_layer['layout'])):
                raise Error('Main-area migration changed unrelated geometry; check native Autobalance splits is OFF')
            for bid, pid in prior_processes.items():
                if rex.process(sid, bid).get('child', {}).get('pid') != pid:
                    raise Error('A working process changed during migration; inspect the preserved tabs')
            rex.call('session.set_window_label', session_id=sid, window_id=archive, label='Work · previous')
            runtime['roles']['work-main'] = vm_binding
            runtime['roles'][history] = original_work_binding
            runtime['roles'].pop(slot)
            runtime.update(vm_slot_role='work-main', vm_slot_name=project.get('vm'))
            archives = runtime.setdefault('vm_archives', {})
            for name, role in list(archives.items()):
                if role == slot:
                    archives.pop(name)
            archives['preserved-work-' + history] = history
            runtime.setdefault('vm_archive_windows', {})[history] = archive
            runtime['last_work'] = vm
            store.write(candidate, project['revision'])
            runtime.pop('vm_main_pending')
            store.record(project['id'], runtime)
            focus = runtime['roles'].get('exedev', {}).get('block_id')
            if focus not in final_live:
                focus = vm
            if focus in final_live:
                rex.call('session.focus_block', session_id=sid, block_id=focus)
            return {'result': 'complete', 'migrated': True, 'block_id': vm,
                    'work_archive_window_id': archive, 'work_archive_role': history,
                    'revision': candidate['revision'], 'existing_processes_preserved': True}
        except Error as exc:
            raise Error(f'Main-area migration incomplete: {exc}. Existing programs were not stopped; '
                        'inspect vm_main_pending and native tabs before retrying.') from exc
