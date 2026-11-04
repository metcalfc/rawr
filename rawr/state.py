"""Validated deliberately saved configuration and separately persisted observations."""
from contextlib import contextmanager
import fcntl
import json
import os
from pathlib import Path
import tempfile
import uuid


class Error(Exception):
    pass


def leaves(node):
    if 'role' in node:
        return [node['role']]
    return leaves(node['before']) + leaves(node['after'])


def validate(project):
    try:
        if project['schema'] != 1:
            raise Error('Unsupported project schema; this build supports schema 1. Upgrade or restore a backup.')
        uuid.UUID(project['id'])
        if not isinstance(project['name'], str) or not project['name']:
            raise ValueError('name must be nonempty')
        if not Path(project['root']).is_absolute() or project['host'] != 'local':
            raise ValueError('only explicit absolute local roots are supported')
        if type(project['revision']) is not int or project['revision'] < 0:
            raise ValueError('invalid revision')
        roles = project['roles']
        if not roles or 'work-main' not in roles:
            raise ValueError('work-main role required')
        for key, recipe in roles.items():
            if not isinstance(key, str) or not key or len(key) > 60:
                raise ValueError('invalid role ID')
            if recipe['kind'] not in ('shell', 'command', 'files', 'github', 'exedev', 'vm-shell'):
                raise ValueError('unknown tool kind')
            if recipe['host'] != 'local' or not Path(recipe['cwd']).is_absolute():
                raise ValueError('invalid recipe host/cwd')
            if not isinstance(recipe['argv'], list) or any(not isinstance(x,str) or '\0' in x for x in recipe['argv']):
                raise ValueError('argv must be a literal string array')
            if recipe['kind'] == 'command' and not recipe['argv']:
                raise ValueError('command requires argv')
            if recipe['reopen'] not in ('explicit', 'shell-placeholder'):
                raise ValueError('unknown reopening policy')
        def node(n, depth=0):
            if depth > 30 or not isinstance(n,dict):
                raise ValueError('layout nesting invalid')
            if set(n) == {'role'}:
                if n['role'] not in roles:
                    raise ValueError('layout references undeclared role')
            elif set(n) == {'direction','ratio','before','after'}:
                if n['direction'] not in ('horizontal','vertical') or type(n['ratio']) not in (float,int) or not .01 <= n['ratio'] <= .99:
                    raise ValueError('invalid split ratio/direction')
                node(n['before'],depth+1); node(n['after'],depth+1)
            else:
                raise ValueError('invalid layout node')
        seen = []
        if not isinstance(project['setup'],list) or not project['setup']:
            raise ValueError('setup needs at least one tab')
        for tab in project['setup']:
            if not isinstance(tab['name'],str):
                raise ValueError('invalid tab name')
            node(tab['layout']); seen += leaves(tab['layout'])
        if 'work-main' not in seen:
            raise ValueError('saved setup must include work-main; associate a working pane before saving')
        if len(seen) != len(set(seen)):
            raise ValueError('role appears twice')
        if not isinstance(project.get('repository'), (str,type(None))) or not isinstance(project.get('vm'), (str,type(None))):
            raise ValueError('repository/VM must be strings or null')
    except (KeyError, TypeError, ValueError, AttributeError) as exc:
        raise Error(f'Invalid project configuration: {exc}. Restore the .previous backup.') from exc
    return project


def read_json(file):
    try:
        return json.loads(file.read_text())
    except (OSError, ValueError) as exc:
        raise Error(f'Cannot read {file}: {exc}. Restore its .previous backup.') from exc


def atomic(file, value):
    file = Path(file)
    file.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    data = json.dumps(value, indent=2, ensure_ascii=False) + '\n'
    fd, temporary = tempfile.mkstemp(prefix='.'+file.name, dir=file.parent)
    try:
        with os.fdopen(fd,'w') as stream:
            stream.write(data); stream.flush(); os.fsync(stream.fileno())
        os.replace(temporary,file)
        directory = os.open(file.parent, os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


class Store:
    def __init__(self, config=None, runtime=None):
        self.config = Path(config or Path(os.environ.get('XDG_CONFIG_HOME', Path.home()/'.config'))/'rawr').expanduser().absolute()
        self.runtime = Path(runtime or Path(os.environ.get('XDG_STATE_HOME', Path.home()/'.local/state'))/'rawr').expanduser().absolute()

    @contextmanager
    def lock(self, project_id):
        # Lock lives beside config, so different runtime directories cannot evade it.
        self.config.mkdir(parents=True,exist_ok=True,mode=0o700)
        with (self.config / (project_id+'.lock')).open('a') as stream:
            fcntl.flock(stream,fcntl.LOCK_EX)
            # A single config namespace must keep one association namespace.
            # Changing runtime roots silently would lose live ownership and duplicate views.
            with (self.config/'.runtime-location').open('a+') as location:
                fcntl.flock(location,fcntl.LOCK_EX)
                location.seek(0); saved_location=location.read().strip()
                if saved_location and saved_location!=str(self.runtime):
                    raise Error(f'This config uses runtime state at {saved_location}; use that --state-dir or migrate it explicitly before opening projects.')
                if not saved_location:
                    location.write(str(self.runtime)+'\n'); location.flush(); os.fsync(location.fileno())
            yield

    def projects(self):
        return [validate(read_json(p)) for p in sorted(self.config.glob('*.json'))]

    def resolve(self, value):
        matches = [p for p in self.projects() if p['id']==value or p['name']==value]
        if len(matches)!=1:
            raise Error('Project is missing or name is ambiguous. Use an ID from `rawr list`.')
        return matches[0]

    def write(self, project, expected):
        validate(project)
        file = self.config / (project['id']+'.json')
        if file.exists():
            current = validate(read_json(file))
            if current['revision'] != expected:
                raise Error(f'Stale save: expected revision {expected}, found {current["revision"]}. Inspect then save again.')
            atomic(file.with_suffix('.previous'), current)
        elif expected is not None:
            raise Error('Project disappeared during save.')
        atomic(file, project)

    def observed(self, project_id):
        file = self.runtime/(project_id+'.json')
        if not file.exists():
            return {}
        value = read_json(file)
        if not isinstance(value,dict) or value.get('schema') != 1 or not isinstance(value.get('roles',{}),dict):
            raise Error('Invalid runtime mapping. Preserve it for diagnosis; explicitly associate the intended session.')
        for role,binding in value.get('roles',{}).items():
            if not isinstance(binding,dict) or not isinstance(binding.get('block_id'),str) or not isinstance(binding.get('label'),str):
                raise Error('Invalid runtime role binding; explicitly associate after inspecting Rex.')
        if 'pending' in value and (not isinstance(value['pending'],dict) or value['pending'].get('kind') not in ('session','split') or
                (value['pending'].get('kind')=='split' and not all(isinstance(value['pending'].get(k),str) for k in ('role','label')))):
            raise Error('Invalid pending creation record; preserve for diagnosis and explicitly associate the intended session.')
        return value

    def record(self, project_id, value):
        atomic(self.runtime/(project_id+'.json'), {'schema':1, **value})
